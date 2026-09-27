package model

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/schematest"
)

// candidates replace one field at a time in a valid file. They leave out what
// the schemas cannot express, which is tested elsewhere: integral numbers not
// written as integer literals (2.0), timestamps that are not real dates, an
// id other than the filename's, a task blocked by itself, and repeated keys.
var candidates = []string{
	`null`, `true`, `false`,
	`0`, `-0`, `1`, `-1`, `1.5`, `-0.5`, `1e400`,
	`999999999999999`, `1000000000000000`,
	`9007199254740991`, `9007199254740992`, `-9007199254740991`, `-9007199254740992`,
	`""`, `"x"`, `" x"`, `"x "`, `"\u00a0x"`, `"\u1680x"`, `"x\u200a"`, `"\u202fx"`, `"x\u205f"`, `"\u3000x"`,
	`"a\u2028b"`, `"a\u0085b"`, `"a\nb"`, `"a\tb"`, `"a\u007fb"`, `"a\u009fb"`, `"a\u200db"`,
	`"Travel"`, `"travel"`, `"a-"`, `"-a"`, `"a--b"`, `"` + strings.Repeat("a", 64) + `"`, `"` + strings.Repeat("a", 65) + `"`,
	`"2026-09-20T18:31:51Z"`, `"2026-09-20T18:31:51"`, `"2026-09-20 18:31:51Z"`, `"2026-09-20T18:31:51z"`,
	`"2026-09-20T18:31:51.5Z"`, `"2026-09-20T18:31:51+00:00"`,
	`"` + strings.Repeat("é", 200) + `"`, `"` + strings.Repeat("é", 201) + `"`,
	`"` + strings.Repeat(`\ud83d\ude00`, 200) + `"`, `"` + strings.Repeat(`\ud83d\ude00`, 201) + `"`, // 400, 402 UTF-16 units
	`[]`, `[1]`, `[1, 1]`, `[1, 1, 1]`, `[0]`, `[1, "x"]`, `[1.5]`, `[null]`, `[[]]`,
	`["travel"]`, `["travel", "travel"]`, `["a", "b", "a", "b"]`, `["Travel"]`, `["Travel", "Travel"]`, `["a", ""]`,
	`["` + strings.Repeat("a", 64) + `"]`, `["` + strings.Repeat("a", 65) + `"]`,
	`{}`, `{"a": 1}`, `{"status": "x", "n": 2.0, "deep": {"k": [1e2]}}`, `{"extra": []}`,
}

// mutations returns base with each top-level field removed, replaced by each
// candidate, and each array item of it replaced by each candidate; plus base
// with an unknown field.
func mutations(t *testing.T, base string) []string {
	obj, _, err := jsonio.ParseObject([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	encode := func(o *jsonio.Object) string {
		b, err := jsonio.MarshalLine(o)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	with := func(key string, v any) string {
		o := &jsonio.Object{Members: slices.Clone(obj.Members)}
		o.Set(key, v)
		return encode(o)
	}
	var out []string
	for _, m := range obj.Members {
		o := &jsonio.Object{Members: slices.Clone(obj.Members)}
		o.Delete(m.Key)
		out = append(out, encode(o))
		for _, c := range candidates {
			v, _, err := jsonio.ParseValue([]byte(c))
			if err != nil {
				t.Fatalf("candidate %s: %v", c, err)
			}
			out = append(out, with(m.Key, v))
			if arr, ok := m.Value.([]any); ok && len(arr) > 0 {
				items := slices.Clone(arr)
				items[len(items)-1] = v
				out = append(out, with(m.Key, items))
			}
		}
	}
	out = append(out, with("unknown", true), with("Schema", json.Number("1")))
	return out
}

// verdict is an adapter's outcome for one document. Early marks a file
// rejected at File validity steps 1–2 (its schema field), which stops every
// later check, so only that field is compared.
type verdict struct {
	ok    bool
	at    []string
	early bool
}

// agree runs doc through the schema library and the adapter: both must
// accept, or both reject at exactly the same fields.
func agree(t *testing.T, schemaID, doc string, adapter func(string) verdict) {
	t.Helper()
	libOK, libAt := schematest.Check(t, schemaID, []byte(doc))
	v := adapter(doc)
	adOK, adAt := v.ok, slices.Compact(slices.Sorted(slices.Values(v.at)))
	switch {
	case libOK != adOK:
		t.Errorf("%s: schema accepts %v (%q), adapter accepts %v (%q)\n  %s", schemaID, libOK, libAt, adOK, adAt, doc)
	case v.early && !slices.Contains(libAt, "/schema"):
		t.Errorf("%s: adapter stops at /schema, schema rejects only at %q\n  %s", schemaID, libAt, doc)
	case !libOK && !v.early && !slices.Equal(libAt, adAt):
		t.Errorf("%s: schema rejects at %q, adapter at %q\n  %s", schemaID, libAt, adAt, doc)
	}
}

// fileVerdict is where a file adapter rejected: a file failing step 1 or 2
// fails at its schema field alone.
func fileVerdict(r FileResult) verdict {
	switch {
	case r.Status == FileOK:
		return verdict{ok: true}
	case r.Status == FileUnsupported || !r.Versioned:
		return verdict{at: []string{"/schema"}, early: true}
	}
	var at []string
	for _, p := range r.Problems {
		at = append(at, p.Field)
	}
	return verdict{at: at}
}

func taskFileAdapter(t *testing.T) func(string) verdict {
	return func(doc string) verdict {
		obj, repeated, err := jsonio.ParseObject([]byte(doc))
		if err != nil {
			t.Fatalf("parse %s: %v", doc, err)
		}
		// The filename's ID is the file's own, when it has a usable one:
		// a mismatch is outside what the schema expresses.
		filenameID := ID(1)
		if n, ok := obj.Get("id"); ok {
			if i, err := strconv.ParseInt(string(asNumber(n)), 10, 64); err == nil {
				filenameID = ID(i)
			}
		}
		_, r := DecodeTaskFile(obj, repeated, filenameID)
		return fileVerdict(r)
	}
}

func asNumber(v any) json.Number {
	n, _ := v.(json.Number)
	return n
}

func TestTaskFileAgreesWithSchema(t *testing.T) {
	adapter := taskFileAdapter(t)
	completed := exampleTask
	for _, r := range [][2]string{
		{`"completed_at": null`, `"completed_at": "2026-09-21T08:00:00Z"`},
		{`"blocked_by": []`, `"blocked_by": [3, 7]`},
		{`"priority": 2`, `"priority": null`},
	} {
		if !strings.Contains(completed, r[0]) {
			t.Fatalf("exampleTask has no %s to replace", r[0])
		}
		completed = strings.Replace(completed, r[0], r[1], 1)
	}
	docs := append(mutations(t, exampleTask), mutations(t, completed)...)
	docs = append(docs,
		`{}`,
		`{"schema": 1}`,
		`{"schema": 2, "id": 1}`,
		`{"schema": "1", "id": 1}`,
	)
	for _, doc := range docs {
		agree(t, "task-file", doc, adapter)
	}
	t.Logf("%d documents", len(docs))
}

func TestRootFileAgreesWithSchema(t *testing.T) {
	adapter := func(doc string) verdict {
		obj, repeated, err := jsonio.ParseObject([]byte(doc))
		if err != nil {
			t.Fatalf("parse %s: %v", doc, err)
		}
		_, r := DecodeRootFile(obj, repeated)
		return fileVerdict(r)
	}
	docs := append(mutations(t, `{"schema": 1, "last_id": 42}`), `{}`, `{"last_id": 0}`)
	for _, doc := range docs {
		agree(t, "root-file", doc, adapter)
	}
	t.Logf("%d documents", len(docs))
}
