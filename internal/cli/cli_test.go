package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/fsys"
	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/ops"
	"github.com/phansen314/ftask/internal/schematest"
	"github.com/phansen314/ftask/internal/store"
)

func testEnv(stdin string) (Env, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return Env{
		Ops:    ops.Env{Env: store.Env{FS: fsys.OS{}}},
		Stdin:  strings.NewReader(stdin),
		Stdout: &out,
		Stderr: &errOut,
	}, &out, &errOut
}

// result is one invocation's output, decoded when it is an envelope.
type result struct {
	code     int
	raw      string
	envelope map[string]any
}

func (r result) kind() string {
	e, _ := r.envelope["error"].(map[string]any)
	k, _ := e["kind"].(string)
	return k
}

// usageProblem is the one usage problem's argument ("" when absent) and reason.
func (r result) usageProblem(t *testing.T) (string, string) {
	t.Helper()
	ps := r.envelope["error"].(map[string]any)["details"].(map[string]any)["problems"].([]any)
	if len(ps) != 1 {
		t.Fatalf("got %d problems, want 1: %s", len(ps), r.raw)
	}
	p := ps[0].(map[string]any)
	arg, _ := p["argument"].(string)
	return arg, p["reason"].(string)
}

func run(t *testing.T, cmds []Command, stdin string, args ...string) result {
	t.Helper()
	env, out, _ := testEnv(stdin)
	b, code := execute(cmds, args, env)
	code = deliver(env, b, code)
	r := result{code: code, raw: out.String()}
	if !strings.HasPrefix(r.raw, "{") {
		return r // help text
	}
	if strings.Count(r.raw, "\n") != 1 || !strings.HasSuffix(r.raw, "\n") {
		t.Errorf("%q: not one line: %q", args, r.raw)
	}
	if ok, f := schematest.Check(t, "envelope", out.Bytes()); !ok {
		t.Errorf("%q: envelope schema rejects at %s: %s", args, f, r.raw)
	}
	if err := json.Unmarshal(out.Bytes(), &r.envelope); err != nil {
		t.Fatal(err)
	}
	if r.kind() == "usage" {
		d, _ := json.Marshal(r.envelope["error"].(map[string]any)["details"])
		if ok, f := schematest.Check(t, "usage-details", d); !ok {
			t.Errorf("%q: usage-details rejects at %s: %s", args, f, d)
		}
	}
	return r
}

func TestVersion(t *testing.T) {
	r := run(t, commands, "", "version")
	if r.code != ExitOK || r.envelope["ok"] != true {
		t.Fatalf("exit %d: %s", r.code, r.raw)
	}
	res, _ := json.Marshal(r.envelope["result"])
	if ok, f := schematest.Check(t, "version-output", res); !ok {
		t.Errorf("version-output rejects at %s: %s", f, res)
	}
}

// info reports an unlocatable config as state: ok, exit 0.
func TestInfo(t *testing.T) {
	r := run(t, commands, "", "info")
	if r.code != ExitOK || r.envelope["ok"] != true {
		t.Fatalf("exit %d: %s", r.code, r.raw)
	}
	res, _ := json.Marshal(r.envelope["result"])
	if ok, f := schematest.Check(t, "info-output", res); !ok {
		t.Errorf("info-output rejects at %s: %s", f, res)
	}
	config := r.envelope["result"].(map[string]any)["config"].(map[string]any)
	if config["path"] != nil || config["state"] != "missing" {
		t.Errorf("config %v, want path null and state missing", config)
	}
}

// show's argument reaches /id as an integer: a non-integer fails there, a
// valid ID gets as far as locating the config (none in testEnv).
func TestShow(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
		kind string
	}{
		{[]string{"show"}, ExitUsage, "usage"},
		{[]string{"show", "abc"}, ExitError, "invalid-input"},
		{[]string{"show", "042"}, ExitError, "invalid-input"},
		{[]string{"show", "42"}, ExitError, "environment"},
		{[]string{"show", "-i", "-"}, ExitError, "environment"},
	} {
		r := run(t, commands, `{"id": 42}`, tc.args...)
		if r.code != tc.code || r.kind() != tc.kind {
			t.Errorf("%q: exit %d kind %q, want %d %s: %s", tc.args, r.code, r.kind(), tc.code, tc.kind, r.raw)
		}
	}
	if arg, reason := run(t, commands, "", "show").usageProblem(t); arg != "" || reason != "missing argument <id>" {
		t.Errorf("bare show: %q %q", arg, reason)
	}
	if r := run(t, commands, "", "show", "abc"); !strings.Contains(r.raw, `"field":"/id"`) {
		t.Errorf("show abc: want a problem at /id: %s", r.raw)
	}
}

func TestInputFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, tc := range []struct {
		name  string
		args  []string
		stdin string
		code  int
		kind  string
		field string // the first invalid-input problem's field
	}{
		{"file", []string{"version", "-i", write("ok.json", `{}`)}, "", ExitOK, "", ""},
		{"stdin", []string{"version", "--input", "-"}, " {} ", ExitOK, "", ""},
		{"attached", []string{"version", "--input=-"}, "{}", ExitOK, "", ""},
		{"field", []string{"version", "-i", write("x.json", `{"x": 1}`)}, "", ExitError, "invalid-input", "/x"},
		{"not json", []string{"version", "-i", "-"}, "{", ExitError, "invalid-input", ""},
		{"two values", []string{"version", "-i", "-"}, "{} {}", ExitError, "invalid-input", ""},
		{"repeated key", []string{"version", "-i", "-"}, `{"a": 1, "a": 2}`, ExitError, "invalid-input", "/a"},
		{"missing file", []string{"version", "-i", filepath.Join(dir, "nope.json")}, "", ExitError, "io", ""},
		{"directory", []string{"version", "-i", dir}, "", ExitError, "io", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := run(t, commands, tc.stdin, tc.args...)
			if r.code != tc.code || r.kind() != tc.kind {
				t.Fatalf("exit %d kind %q, want %d %q: %s", r.code, r.kind(), tc.code, tc.kind, r.raw)
			}
			if tc.kind == "invalid-input" {
				ps := r.envelope["error"].(map[string]any)["details"].(map[string]any)["problems"].([]any)
				if f := ps[0].(map[string]any)["field"]; f != tc.field {
					t.Errorf("field %q, want %q", f, tc.field)
				}
			}
		})
	}
}

func TestUsage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		arg    string // "" when the problem names no token
		reason string // a substring
	}{
		{"bare", nil, "", "missing command"},
		{"unknown command", []string{"nosuch"}, "nosuch", "unknown command"},
		{"suggestion", []string{"verison"}, "verison", "did you mean version"},
		{"case", []string{"Version"}, "Version", "unknown command"},
		{"extra argument", []string{"version", "extra"}, "extra", "unexpected argument"},
		{"unknown option", []string{"version", "--bogus"}, "--bogus", "unknown flag"},
		{"unknown short", []string{"version", "-x"}, "-x", "unknown shorthand"},
		{"unknown before command", []string{"--bogus", "version"}, "--bogus", "unknown flag"},
		{"missing value", []string{"version", "--input"}, "--input", "needs an argument"},
		{"missing short value", []string{"version", "-i"}, "-i", "needs an argument"},
		{"input with argument", []string{"version", "-i", "-", "x"}, "x", "--input cannot be combined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := run(t, commands, "{}", tc.args...)
			if r.code != ExitUsage || r.kind() != "usage" {
				t.Fatalf("exit %d kind %q, want usage: %s", r.code, r.kind(), r.raw)
			}
			arg, reason := r.usageProblem(t)
			if arg != tc.arg || !strings.Contains(reason, tc.reason) {
				t.Errorf("problem (%q, %q), want (%q, ...%q...)", arg, reason, tc.arg, tc.reason)
			}
		})
	}
}

func TestHelp(t *testing.T) {
	for _, args := range [][]string{
		{"--help"}, {"-h"}, {"help"}, {"help", "version"}, {"version", "--help"}, {"version", "-h"},
	} {
		r := run(t, commands, "", args...)
		if r.code != ExitOK || r.envelope != nil || !strings.Contains(r.raw, "Usage:") {
			t.Errorf("%q: exit %d: %s", args, r.code, r.raw)
		}
	}
}

// synthetic exercises every value type and the shape checks.
var synthetic = []Command{{
	Name: "t",
	Op:   "t",
	Args: []Arg{{Name: "id", Field: "/id", Type: Int}, {Name: "title", Field: "/title", Type: String}},
	Options: []Option{
		{Name: "folder", Field: "/folder", Type: String},
		{Name: "priority", Field: "/priority", Type: NullableInt},
		{Name: "count", Field: "/count", Type: Int},
		{Name: "recursive", Field: "/recursive", Type: Bool},
		{Name: "parents", Short: "p", Field: "/parents", Type: Bool},
		{Name: "tags", Field: "/tags", Type: TagList},
		{Name: "tags-add", Field: "/tags/add", Type: TagList},
		{Name: "blocked-by", Field: "/blocked_by", Type: IDList},
		{Name: "extra", Field: "/extra", Type: JSON},
		{Name: "extra-merge", Field: "/extra/merge", Type: JSON},
		{Name: "extra-remove", Field: "/extra/remove", Type: Repeated},
		{Name: "mode", Field: "/mode", Type: String, Required: true},
	},
}}

// captured runs args against synthetic, capturing what reaches ops.Run.
func captured(t *testing.T, args ...string) (string, []errs.Problem) {
	t.Helper()
	saved := runOp
	t.Cleanup(func() { runOp = saved })
	var got *jsonio.Object
	var problems []errs.Problem
	runOp = func(_ string, in *jsonio.Object, ps []errs.Problem, _ ops.Env) ops.Envelope {
		got, problems = in, ps
		return ops.Envelope{OK: true, Result: struct{}{}, Warnings: []errs.Warning{}}
	}
	r := run(t, synthetic, "{}", append([]string{"t"}, args...)...)
	if r.code != ExitOK {
		t.Fatalf("%q: exit %d: %s", args, r.code, r.raw)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), problems
}

func TestBuildInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"minimal", []string{"42", "x", "--mode", "m"}, `{"id":42,"title":"x","mode":"m"}`},
		{"options anywhere", []string{"--mode=m", "42", "--folder", "/a", "x"}, `{"id":42,"title":"x","folder":"/a","mode":"m"}`},
		{"not an integer", []string{"abc", "x", "--mode", "m", "--count", "2.0"}, `{"id":"abc","title":"x","count":"2.0","mode":"m"}`},
		{"null", []string{"1", "x", "--mode", "m", "--priority", "null", "--count", "null"}, `{"id":1,"title":"x","priority":null,"count":"null","mode":"m"}`},
		{"negative value", []string{"1", "x", "--mode", "m", "--priority", "-3"}, `{"id":1,"title":"x","priority":-3,"mode":"m"}`},
		{"booleans", []string{"1", "x", "--mode", "m", "--recursive=false", "-p"}, `{"id":1,"title":"x","recursive":false,"parents":true,"mode":"m"}`},
		{"empty list", []string{"1", "x", "--mode", "m", "--tags", ""}, `{"id":1,"title":"x","tags":[],"mode":"m"}`},
		{"lists join", []string{"1", "x", "--mode", "m", "--tags", "a, b", "--tags", "a,,c", "--blocked-by", "3,x,-0"}, `{"id":1,"title":"x","tags":["a"," b","a","","c"],"blocked_by":[3,"x",-0],"mode":"m"}`},
		{"nested", []string{"1", "x", "--mode", "m", "--tags-add", "a", "--extra-merge", `{"k":2.0}`, "--extra-remove", "a,b", "--extra-remove", "c"}, `{"id":1,"title":"x","tags":{"add":["a"]},"extra":{"merge":{"k":2.0},"remove":["a,b","c"]},"mode":"m"}`},
		{"last wins", []string{"1", "x", "--mode", "a", "--mode", "b"}, `{"id":1,"title":"x","mode":"b"}`},
		{"after --", []string{"--mode", "m", "--", "1", "-urgent"}, `{"id":1,"title":"-urgent","mode":"m"}`},
		{"lone dash", []string{"--mode", "m", "1", "-"}, `{"id":1,"title":"-","mode":"m"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ps := captured(t, tc.args...)
			if got != tc.want || len(ps) != 0 {
				t.Errorf("got  %s %v\nwant %s", got, ps, tc.want)
			}
		})
	}
}

func TestBuildInputProblems(t *testing.T) {
	deep := strings.Repeat("[", 9990) + strings.Repeat("]", 9990)
	for _, tc := range []struct {
		name   string
		args   []string
		want   string
		fields []string
	}{
		{"bad json", []string{"1", "x", "--mode", "m", "--extra", "{"}, `{"id":1,"title":"x","mode":"m"}`, []string{"/extra"}},
		{"repeated key", []string{"1", "x", "--mode", "m", "--extra-merge", `{"a":1,"a":2}`}, `{"id":1,"title":"x","mode":"m"}`, []string{"/extra/merge/a"}},
		// 9,990 levels fit the input, but not at /extra.
		{"too deep", []string{"1", "x", "--mode", "m", "--extra", deep}, `{"id":1,"title":"x","mode":"m"}`, []string{"/extra"}},
		{"not utf-8", []string{"1", "x\xff", "--mode", "m", "--tags", "a\xff"}, `{"id":1,"mode":"m"}`, []string{"/title", "/tags"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ps := captured(t, tc.args...)
			var fields []string
			for _, p := range ps {
				fields = append(fields, p.Field)
			}
			if got != tc.want || !slices.Equal(fields, tc.fields) {
				t.Errorf("got  %s %q\nwant %s %q", got, fields, tc.want, tc.fields)
			}
		})
	}
}

func TestShape(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		arg    string
		reason string
	}{
		{"missing argument", []string{"1", "--mode", "m"}, "", "missing argument <title>"},
		{"missing required option", []string{"1", "x"}, "", "missing required option --mode"},
		{"input with option", []string{"-i", "-", "--folder", "/a"}, "--folder", "--input cannot be combined"},
		{"bool with value token", []string{"1", "x", "--mode", "m", "--recursive", "false"}, "false", "unexpected argument"},
		{"bool with bad value", []string{"1", "x", "--mode", "m", "--recursive=maybe"}, "--recursive", "invalid argument"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := run(t, synthetic, "{}", append([]string{"t"}, tc.args...)...)
			if r.code != ExitUsage {
				t.Fatalf("exit %d: %s", r.code, r.raw)
			}
			arg, reason := r.usageProblem(t)
			if arg != tc.arg || !strings.Contains(reason, tc.reason) {
				t.Errorf("problem (%q, %q), want (%q, ...%q...)", arg, reason, tc.arg, tc.reason)
			}
		})
	}
	// --input alone satisfies required arguments and options.
	if got, _ := captured(t, "-i", "-"); got != `{}` {
		t.Errorf("--input: got %s", got)
	}
}

type failWriter struct{ writeErr, closeErr error }

func (w failWriter) Write(p []byte) (int, error) { return len(p), w.writeErr }
func (w failWriter) Close() error                { return w.closeErr }

func TestDeliver(t *testing.T) {
	for _, tc := range []struct {
		name string
		w    failWriter
		want int
	}{
		{"ok", failWriter{}, ExitUsage},
		{"write fails", failWriter{writeErr: errors.New("EPIPE")}, ExitNotDelivered},
		{"close fails", failWriter{closeErr: errors.New("EIO")}, ExitNotDelivered},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			got := deliver(Env{Stdout: tc.w, Stderr: &stderr}, []byte("x\n"), ExitUsage)
			if got != tc.want || (got == ExitNotDelivered) != strings.HasPrefix(stderr.String(), "ftask: result not delivered: ") {
				t.Errorf("exit %d, stderr %q", got, stderr.String())
			}
		})
	}
}

func TestCommandsRunOperations(t *testing.T) {
	for _, c := range commands {
		if !slices.Contains(ops.Operations(), c.Op) {
			t.Errorf("command %s runs unknown operation %s", c.Name, c.Op)
		}
	}
}
