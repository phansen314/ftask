// Package schematest compiles the specs' JSON Schemas (schemas/, generated
// by schemagen) for tests, and reports where a value fails one. It is
// imported only by tests, so the schema library never reaches the binary.
package schematest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

// base is the fixed URI the schemas are loaded under; each $id resolves
// against it, so "task-file#/properties/id" finds task-file.
const base = "https://ftask.invalid/schemas/"

var (
	mu       sync.Mutex
	compiler *jsonschema.Compiler
	compiled = map[string]*jsonschema.Schema{}
)

// Dir returns the schemas/ directory.
func Dir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "schemas")
}

// IDs returns the $id of every schema, sorted.
func IDs() ([]string, error) {
	files, err := filepath.Glob(filepath.Join(Dir(), "*.json"))
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, f := range files {
		ids = append(ids, strings.TrimSuffix(filepath.Base(f), ".json"))
	}
	slices.Sort(ids)
	return ids, nil
}

// Schema returns the compiled schema with the given $id.
func Schema(t testing.TB, id string) *jsonschema.Schema {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	if s, ok := compiled[id]; ok {
		return s
	}
	if compiler == nil {
		c, err := newCompiler()
		if err != nil {
			t.Fatal(err)
		}
		compiler = c
	}
	s, err := compiler.Compile(base + id)
	if err != nil {
		t.Fatalf("compile %s: %v", id, err)
	}
	compiled[id] = s
	return s
}

func newCompiler() (*jsonschema.Compiler, error) {
	ids, err := IDs()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no schemas in %s: run go generate ./...", Dir())
	}
	c := jsonschema.NewCompiler()
	c.UseRegexpEngine(ecmaEngine)
	for _, id := range ids {
		f, err := os.Open(filepath.Join(Dir(), id+".json"))
		if err != nil {
			return nil, err
		}
		doc, err := jsonschema.UnmarshalJSON(f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %v", id, err)
		}
		if err := c.AddResource(base+id, doc); err != nil {
			return nil, fmt.Errorf("%s: %v", id, err)
		}
	}
	return c, nil
}

// Check validates data, one JSON value, against the schema with the given
// $id. On failure it returns where, per Locations.
func Check(t testing.TB, id string, data []byte) (ok bool, locations []string) {
	t.Helper()
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unmarshal %s: %v", data, err)
	}
	err = Schema(t, id).Validate(v)
	if err == nil {
		return true, nil
	}
	ve, isVE := err.(*jsonschema.ValidationError)
	if !isVE {
		t.Fatalf("validate %s against %s: %v", data, id, err)
	}
	return false, Locations(ve, v)
}

// Locations returns the JSON Pointers of a validation error's leaves,
// sorted and without repeats. Errors a parent reports about one of its
// children — a missing required property, a disallowed additional property,
// a duplicate array item — are placed at that child, where ftask's adapters
// report them. instance is the value validated, as jsonschema.UnmarshalJSON
// returns it: the library names only the first duplicate in an array, so
// every item equal to an earlier one is found there.
func Locations(ve *jsonschema.ValidationError, instance any) []string {
	var out []string
	var walk func(*jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) > 0 {
			for _, c := range e.Causes {
				walk(c)
			}
			return
		}
		at := pointer(e.InstanceLocation)
		switch k := e.ErrorKind.(type) {
		case *kind.Required:
			for _, name := range k.Missing {
				out = append(out, child(at, name))
			}
		case *kind.AdditionalProperties:
			for _, name := range k.Properties {
				out = append(out, child(at, name))
			}
		case *kind.UniqueItems:
			for _, i := range duplicates(at, lookup(instance, e.InstanceLocation)) {
				out = append(out, child(at, strconv.Itoa(i)))
			}
		default:
			out = append(out, at)
		}
	}
	walk(ve)
	slices.Sort(out)
	return slices.Compact(out)
}

// lookup returns the value at tokens within v.
func lookup(v any, tokens []string) any {
	for _, tok := range tokens {
		switch x := v.(type) {
		case map[string]any:
			v = x[tok]
		case []any:
			i, _ := strconv.Atoi(tok)
			v = x[i]
		}
	}
	return v
}

// duplicates returns the index of every item of arr, at ptr, equal to an
// earlier one.
func duplicates(ptr string, arr any) []int {
	a, ok := arr.([]any)
	if !ok {
		panic(fmt.Sprintf("uniqueItems error at %q, not an array", ptr))
	}
	var out []int
	for i := range a {
		for j := range i {
			if equal(a[i], a[j]) {
				out = append(out, i)
				break
			}
		}
	}
	return out
}

// equal is JSON Schema equality: numbers by value, the rest structurally.
func equal(a, b any) bool {
	switch a := a.(type) {
	case json.Number:
		b, ok := b.(json.Number)
		if !ok {
			return false
		}
		x, okA := new(big.Rat).SetString(string(a))
		y, okB := new(big.Rat).SetString(string(b))
		return okA && okB && x.Cmp(y) == 0
	case []any:
		b, ok := b.([]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for i := range a {
			if !equal(a[i], b[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		b, ok := b.(map[string]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for k, x := range a {
			y, ok := b[k]
			if !ok || !equal(x, y) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

func pointer(tokens []string) string {
	var b strings.Builder
	for _, tok := range tokens {
		b.WriteString(child("", tok))
	}
	return b.String()
}

func child(ptr, token string) string {
	token = strings.ReplaceAll(token, "~", "~0")
	token = strings.ReplaceAll(token, "/", "~1")
	return ptr + "/" + token
}

// ecmaEngine compiles a JSON Schema pattern, written in ECMA-262 syntax, with
// Go's regexp: the specs' patterns use nothing RE2 lacks except the \uXXXX
// escape, which becomes \x{XXXX}. Syntax both accept with different meanings
// — "." outside a class, \s, \S — is refused, so a pattern using it fails to
// compile rather than being checked wrongly.
func ecmaEngine(pattern string) (jsonschema.Regexp, error) {
	var b strings.Builder
	inClass := false
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c != '\\' || i+1 >= len(pattern) {
			switch {
			case c == '.' && !inClass:
				return nil, fmt.Errorf("%q: '.' differs between ECMA-262 and RE2", pattern)
			case c == '[':
				inClass = true
			case c == ']':
				inClass = false
			}
			b.WriteByte(c)
			continue
		}
		switch e := pattern[i+1]; {
		case e == 'u' && i+6 <= len(pattern) && isHex4(pattern[i+2:i+6]):
			b.WriteString(`\x{` + pattern[i+2:i+6] + `}`)
			i += 5
			continue
		case e == 's' || e == 'S':
			return nil, fmt.Errorf("%q: \\%c differs between ECMA-262 and RE2", pattern, e)
		}
		b.WriteString(pattern[i : i+2]) // any other escape, e.g. \\, copied whole
		i++
	}
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, err
	}
	return re, nil
}

func isHex4(s string) bool {
	for _, c := range []byte(s) {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F') {
			return false
		}
	}
	return true
}
