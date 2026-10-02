package pick

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/fsys"
	"github.com/phansen314/ftask/internal/ops"
)

func TestSessionBase(t *testing.T) {
	for _, tc := range []struct {
		env  []string
		want string
	}{
		{[]string{"XDG_RUNTIME_DIR=/run/user/1", "TMPDIR=/t"}, "/run/user/1"},
		{[]string{"XDG_RUNTIME_DIR=", "TMPDIR=/t"}, "/t"},
		{[]string{"TMPDIR=/t", "TMPDIR=/u"}, "/u"},
		{[]string{"HOME=/h"}, "/tmp"},
		{nil, "/tmp"},
	} {
		if got := sessionBase(tc.env); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.env, got, tc.want)
		}
	}
}

// entries is what dir holds, by name.
func entries(t *testing.T, dir string) []string {
	t.Helper()
	des, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, de := range des {
		names = append(names, de.Name())
	}
	return names
}

func TestSession(t *testing.T) {
	base := t.TempDir()
	s, e := newSession(fsys.OS{}, base)
	if e != nil {
		t.Fatal(e)
	}
	if filepath.Dir(s.Dir) != base || !strings.HasPrefix(filepath.Base(s.Dir), sessionPrefix) {
		t.Errorf("session at %s, want %s/%s…", s.Dir, base, sessionPrefix)
	}
	fi, err := os.Stat(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("mode %v, want 0700", fi.Mode().Perm())
	}

	if _, ok, e := s.Read("mode"); ok || e != nil {
		t.Errorf("a file never written: ok %v, %v", ok, e)
	}
	for _, v := range []string{"insert", "command"} {
		if e := s.Write("mode", []byte(v)); e != nil {
			t.Fatal(e)
		}
		if b, ok, e := s.Read("mode"); string(b) != v || !ok || e != nil {
			t.Errorf("read %q, %v, %v; want %q", b, ok, e, v)
		}
	}
	// Writes leave no temp file behind.
	if got := entries(t, s.Dir); !slices.Equal(got, []string{"mode", markerName}) {
		t.Errorf("session holds %q", got)
	}

	// The helper finds it through the environment.
	h, e := openSession(fsys.OS{}, []string{SessionVar + "=" + s.Dir})
	if e != nil {
		t.Fatal(e)
	}
	if b, _, _ := h.Read("mode"); string(b) != "command" {
		t.Errorf("helper read %q", b)
	}
	h.Close()

	s.Remove()
	if got := entries(t, base); len(got) != 0 {
		t.Errorf("after Remove, base holds %q", got)
	}
}

// Two sessions in one base never share a directory.
func TestSessionsApart(t *testing.T) {
	base := t.TempDir()
	a, e := newSession(fsys.OS{}, base)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Remove()
	b, e := newSession(fsys.OS{}, base)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Remove()
	if a.Dir == b.Dir {
		t.Errorf("both at %s", a.Dir)
	}
}

func TestNewSessionFails(t *testing.T) {
	base := t.TempDir()
	if _, e := newSession(fsys.OS{}, filepath.Join(base, "missing")); e == nil || e.Kind != errs.KindIO {
		t.Errorf("missing base: %v, want io", e)
	}
	// The marker can't be written: nothing is left behind.
	f := fsys.Fault{FS: fsys.OS{}, Hook: fsys.ErrnoAt(fsys.OpRename, "", 1, syscall.ENOSPC)}
	if _, e := newSession(f, base); e == nil || e.Kind != errs.KindIO {
		t.Errorf("failed marker: %v, want io", e)
	}
	if got := entries(t, base); len(got) != 0 {
		t.Errorf("base holds %q", got)
	}
}

func TestSessionWriteFails(t *testing.T) {
	base := t.TempDir()
	var hook fsys.Hook
	f := fsys.Fault{FS: fsys.OS{}, Hook: func(op fsys.Op) error {
		if hook != nil {
			return hook(op)
		}
		return nil
	}}
	s, e := newSession(f, base)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Remove()
	if e := s.Write("mode", []byte("insert")); e != nil {
		t.Fatal(e)
	}
	hook = fsys.ErrnoAt(fsys.OpRename, "", 1, syscall.ENOSPC)
	if e := s.Write("mode", []byte("command")); e == nil || e.Kind != errs.KindIO {
		t.Fatalf("got %v, want io", e)
	}
	// The old contents stay, and the temp file goes.
	if b, _, _ := s.Read("mode"); string(b) != "insert" {
		t.Errorf("read %q, want the old contents", b)
	}
	if got := entries(t, s.Dir); !slices.Equal(got, []string{"mode", markerName}) {
		t.Errorf("session holds %q", got)
	}
}

func TestOpenSessionRefuses(t *testing.T) {
	dir := t.TempDir()
	notSession := filepath.Join(dir, "plain")
	wrongMarker := filepath.Join(dir, "wrong")
	file := filepath.Join(dir, "file")
	for _, d := range []string{notSession, wrongMarker} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(wrongMarker, markerName), []byte("something else\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(markerText), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, env := range [][]string{
		nil,
		{SessionVar + "="},
		{SessionVar + "=relative/dir"},
		{SessionVar + "=" + filepath.Join(dir, "missing")},
		{SessionVar + "=" + file},
		{SessionVar + "=" + notSession},
		{SessionVar + "=" + wrongMarker},
	} {
		if _, e := openSession(fsys.OS{}, env); e == nil || e.Kind != errs.KindUsage {
			t.Errorf("%q: %v, want usage", env, e)
		}
	}
}

func TestHelper(t *testing.T) {
	s, e := newSession(fsys.OS{}, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Remove()
	var got []string
	verbs["echo"] = func(hs *Session, args []string, _ Env) ([]byte, *errs.Error) {
		if hs.Dir != s.Dir {
			t.Errorf("verb ran in %s, want %s", hs.Dir, s.Dir)
		}
		got = args
		return []byte("accept"), nil
	}
	defer delete(verbs, "echo")
	env := func(environ ...string) Env {
		return Env{Ops: ops.Env{}, Sys: System{Environ: func() []string { return environ }}}
	}
	env0 := env(SessionVar + "=" + s.Dir)
	env0.Ops.FS = fsys.OS{}

	out, e := Helper([]string{"echo", "a", "b c"}, env0)
	if e != nil || string(out) != "accept" || !slices.Equal(got, []string{"a", "b c"}) {
		t.Errorf("got %q, %v, args %q", out, e, got)
	}

	for _, tc := range []struct {
		name string
		args []string
		env  Env
		arg  *string
	}{
		{"no session", []string{"echo"}, func() Env { e := env(); e.Ops.FS = fsys.OS{}; return e }(), nil},
		{"missing verb", nil, env0, nil},
		{"unknown verb", []string{"nope", "x"}, env0, ptr("nope")},
	} {
		_, e := Helper(tc.args, tc.env)
		if e == nil || e.Kind != errs.KindUsage {
			t.Errorf("%s: %v, want usage", tc.name, e)
			continue
		}
		p := e.Details.(errs.UsageDetails).Problems[0]
		if (p.Argument == nil) != (tc.arg == nil) || (p.Argument != nil && *p.Argument != *tc.arg) {
			t.Errorf("%s: problem %+v", tc.name, p)
		}
	}
}

func ptr[T any](v T) *T { return &v }
