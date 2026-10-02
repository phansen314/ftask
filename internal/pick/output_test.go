package pick

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/model"
	"github.com/phansen314/ftask/internal/ops"
	"github.com/phansen314/ftask/internal/schematest"
)

func TestSelected(t *testing.T) {
	vs := views(
		tv{id: 1, folder: "/", r: model.Ready},
		tv{id: 2, folder: "/moved", r: model.Complete, done: "2026-09-01T00:00:00Z"},
		tv{id: 3, folder: "/a", r: model.Ready, priority: p(5)},
		tv{id: 3, folder: "/b", r: model.Ready, priority: p(5)},
		tv{id: 4, folder: "/c", r: model.Ready},
	)
	for _, tc := range []struct {
		name    string
		keys    []string
		tasks   []string
		missing []model.ID
	}{
		{"none", nil, []string{}, []model.ID{}},
		// Line order, not the order marked; a task found wherever it now is.
		{"order", []string{"1@/", "2@/", "3@/b"}, []string{"3@/b", "1@/", "2@/moved"}, []model.ID{}},
		// Copies: the one in the key's folder, or missing if none is there.
		{"copies", []string{"3@/a", "3@/z"}, []string{"3@/a"}, []model.ID{3}},
		// A copy elsewhere still counts when it's the only one left.
		{"one copy left", []string{"4@/gone"}, []string{"4@/c"}, []model.ID{}},
		{"deleted", []string{"9@/", "8@/", "9@/x"}, []string{}, []model.ID{8, 9}},
		{"twice", []string{"1@/", "1@/old"}, []string{"1@/"}, []model.ID{}},
		{"not a key", []string{"x", "1"}, []string{}, []model.ID{}},
	} {
		tasks, missing := selected(vs, tc.keys)
		if !slices.Equal(keys(tasks), tc.tasks) || !slices.Equal(missing, tc.missing) {
			t.Errorf("%s: got %q, missing %v", tc.name, keys(tasks), missing)
		}
	}
}

// tree is an initialized ftask tree in a home of its own, for running pick
// whole.
type tree struct {
	t   *testing.T
	env ops.Env
}

func newTestTree(t *testing.T) *tree {
	home := t.TempDir()
	getenv := func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}
	tr := &tree{t: t, env: ops.NewEnv(getenv, runtime.GOOS)}
	tr.run("init", map[string]any{"root": filepath.Join(home, "tasks")})
	return tr
}

// run runs an operation, which must succeed.
func (tr *tree) run(op string, in map[string]any) ops.Envelope {
	tr.t.Helper()
	b, _ := json.Marshal(in)
	obj, _, err := jsonio.ParseObject(b)
	if err != nil {
		tr.t.Fatal(err)
	}
	out := ops.Run(op, obj, nil, tr.env)
	if !out.OK {
		tr.t.Fatalf("%s: %+v", op, out.Error)
	}
	return out
}

// fzfDoes is a fake fzf: it runs do, which plays the person's keys by
// calling the helper as fzf's callbacks would, in fzf's environment, and
// exits with status.
type fzfDoes struct {
	do     func(t *testing.T, helper func(args ...string) string)
	status int
}

func (tr *tree) pick(in map[string]any, fzf fzfDoes) (ops.Envelope, []byte) {
	tr.t.Helper()
	runtimeDir := tr.t.TempDir()
	sys := System{
		LookPath:   func(string) (string, error) { return "/bin/fzf", nil },
		Output:     func(string, []string, []string) ([]byte, []byte, int, error) { return []byte("0.63.0\n"), nil, 0, nil },
		Environ:    func() []string { return []string{"XDG_RUNTIME_DIR=" + runtimeDir} },
		Executable: func() (string, error) { return "/bin/ftask", nil },
		OpenTTY:    func() error { return nil },
	}
	sys.RunFzf = func(_ string, _ []string, env []string, _ []byte) (int, error) {
		helper := func(args ...string) string {
			hsys := sys
			hsys.Environ = func() []string { return env }
			out, e := Helper(args, Env{Ops: tr.env, Sys: hsys})
			if e != nil {
				tr.t.Fatalf("helper %q: %v", args, e)
			}
			return string(out)
		}
		if fzf.do != nil {
			fzf.do(tr.t, helper)
		}
		return fzf.status, nil
	}
	b, _ := json.Marshal(in)
	obj, _, _ := jsonio.ParseObject(b)
	out := Run(obj, nil, Env{Ops: tr.env, Sys: sys})
	line, err := jsonio.MarshalLine(out)
	if err != nil {
		tr.t.Fatal(err)
	}
	if ok, f := schematest.Check(tr.t, "envelope", line); !ok {
		tr.t.Errorf("envelope rejected at %s: %s", f, line)
	}
	if des, _ := os.ReadDir(runtimeDir); len(des) != 0 {
		tr.t.Errorf("session left behind")
	}
	return out, line
}

// result is the result object of an ok envelope line, checked against
// pick-output.
func result(t *testing.T, line []byte) []byte {
	t.Helper()
	var env struct {
		Result json.RawMessage `json:"result"`
	}
	json.Unmarshal(line, &env)
	if ok, f := schematest.Check(t, "pick-output", env.Result); !ok {
		t.Errorf("pick-output rejected at %s: %s", f, env.Result)
	}
	return env.Result
}

func TestRunOutcomes(t *testing.T) {
	tr := newTestTree(t)
	tr.run("create-folder", map[string]any{"folder": "/a"})
	for _, title := range []string{"one", "two", "three"} {
		tr.run("create", map[string]any{"title": title, "folder": "/a"})
	}
	tr.run("update", map[string]any{"id": 3, "priority": 1})

	// Enter on marked lines: line order, as the final read has them.
	out, line := tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		if got := helper("enter", "1@/a", "3@/a"); got != "accept" {
			t.Errorf("enter printed %q", got)
		}
	}})
	if !out.OK || !strings.Contains(string(result(t, line)), `"title":"three"`) {
		t.Fatalf("enter: %s", line)
	}
	if got := keys(out.Result.(Output).Tasks.Views); !slices.Equal(got, []string{"3@/a", "1@/a"}) {
		t.Errorf("enter: %q", got)
	}

	// Fields shape the tasks; actions and notes_edited are empty.
	_, line = tr.pick(map[string]any{"fields": []string{"title"}}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		helper("enter", "2@/a")
	}})
	if got := string(result(t, line)); got != `{"tasks":[{"id":2,"title":"two"}],"missing":[],"actions":[],"notes_edited":[]}` {
		t.Errorf("fields: %s", got)
	}

	// Changed meanwhile: a moved task as it now is, a deleted one missing.
	// Enter's status is 0, or 1 when the query matches nothing: both are
	// Enter, as the selection is recorded.
	tr.run("create-folder", map[string]any{"folder": "/b"})
	_, line = tr.pick(map[string]any{}, fzfDoes{status: 1, do: func(t *testing.T, helper func(...string) string) {
		tr.run("move", map[string]any{"id": 1, "to": "/b"})
		tr.run("delete", map[string]any{"id": 2})
		helper("enter", "1@/a", "2@/a")
	}})
	if got := string(result(t, line)); !strings.Contains(got, `"folder":"/b"`) || !strings.Contains(got, `"missing":[2]`) {
		t.Errorf("changed meanwhile: %s", got)
	}

	// Quit, and Enter with nothing under the cursor: an empty selection.
	for _, do := range []func(t *testing.T, helper func(...string) string){
		func(t *testing.T, helper func(...string) string) { helper("quit") },
		func(t *testing.T, helper func(...string) string) { helper("enter") },
	} {
		_, line := tr.pick(map[string]any{}, fzfDoes{do: do, status: 1})
		if got := string(result(t, line)); got != `{"tasks":[],"missing":[],"actions":[],"notes_edited":[]}` {
			t.Errorf("empty selection: %s", got)
		}
	}

	// Without a selection: 130 is cancel, anything else fzf-failed.
	for status, want := range map[int]string{
		130: `{"kind":"cancelled","message":"cancelled","details":{"actions":[]}}`,
		0:   `"details":{"reason":"fzf-failed","status":0,"actions":[]}`,
		2:   `"details":{"reason":"fzf-failed","status":2,"actions":[]}`,
	} {
		out, line := tr.pick(map[string]any{}, fzfDoes{status: status})
		if out.OK || !strings.Contains(string(line), want) {
			t.Errorf("status %d: %s", status, line)
		}
	}
}

// The final read fails: incomplete, with its error whole.
func TestRunIncomplete(t *testing.T) {
	tr := newTestTree(t)
	tr.run("create", map[string]any{"title": "one"})
	out, line := tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		helper("enter", "1@/")
		cfg := filepath.Join(tr.env.ConfigDir, "config.toml")
		if err := os.Remove(cfg); err != nil {
			t.Fatal(err)
		}
	}})
	if out.OK || out.Error.Kind != errs.KindIncomplete {
		t.Fatalf("got %s", line)
	}
	d, _ := json.Marshal(out.Error.Details)
	if ok, f := schematest.Check(t, "pick-error-details#/$defs/incomplete", d); !ok {
		t.Errorf("details rejected at %s: %s", f, d)
	}
	if !strings.Contains(string(d), `"actions":[],"error":{"kind":"not-initialized"`) {
		t.Errorf("details %s", d)
	}
}
