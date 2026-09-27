package fsys

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/phansen314/ftask/internal/errs"
)

func TestFaultPassesThrough(t *testing.T) {
	dir := t.TempDir()
	var ops []Op
	fsys := Fault{FS: OS{}, Hook: Record(&ops)}
	r, err := fsys.OpenRoot(dir)
	must(t, err)
	defer r.Close()

	f, name, err := r.CreateTemp(".")
	must(t, err)
	_, err = f.Write([]byte("x"))
	must(t, err)
	must(t, f.Close())
	must(t, r.Link(name, "1.json"))
	must(t, r.Remove(name))
	got, err := r.ReadFile("1.json")
	must(t, err)
	if string(got) != "x" {
		t.Errorf("got %q", got)
	}

	want := []Op{
		{Name: OpOpenRoot, Path: dir},
		{Root: dir, Name: OpCreateTemp, Path: ".", Mutating: true},
		{Root: dir, Name: OpWrite, Path: name, Mutating: true},
		{Root: dir, Name: OpCloseFile, Path: name},
		{Root: dir, Name: OpLink, Path: name, NewPath: "1.json", Mutating: true},
		{Root: dir, Name: OpRemove, Path: name, Mutating: true},
		{Root: dir, Name: OpReadFile, Path: "1.json"},
	}
	if len(ops) != len(want) {
		t.Fatalf("got %d ops %+v, want %d", len(ops), ops, len(want))
	}
	for i := range want {
		if ops[i] != want[i] {
			t.Errorf("op %d: got %+v, want %+v", i, ops[i], want[i])
		}
	}
}

func TestErrnoAt(t *testing.T) {
	dir := t.TempDir()
	fsys := Fault{FS: OS{}, Hook: ErrnoAt(OpWrite, "", 2, syscall.ENOSPC)}
	r, err := fsys.OpenRoot(dir)
	must(t, err)
	defer r.Close()

	f, name, err := r.CreateTemp(".")
	must(t, err)
	_, err = f.Write([]byte("a"))
	must(t, err)
	_, err = f.Write([]byte("b"))
	wantErrno(t, err, syscall.ENOSPC)
	if e := errs.FromOS(name, err); e.Kind != errs.KindIO {
		t.Errorf("FromOS gave %v", e)
	}
	_, err = f.Write([]byte("c"))
	must(t, err)
	must(t, f.Close())
	got, _ := os.ReadFile(filepath.Join(dir, name))
	if string(got) != "ac" {
		t.Errorf("file holds %q; the failed write must not happen", got)
	}
}

func TestErrnoAtPath(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "1.json"), nil, 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "2.json"), nil, 0o644))
	fsys := Fault{FS: OS{}, Hook: ErrnoAt(OpReadFile, "2.json", 1, syscall.EACCES)}
	r, err := fsys.OpenRoot(dir)
	must(t, err)
	defer r.Close()

	_, err = r.ReadFile("1.json")
	must(t, err)
	_, err = r.ReadFile("2.json")
	wantErrno(t, err, syscall.EACCES)
	var pe *os.PathError
	if !errors.As(err, &pe) || pe.Path != "2.json" {
		t.Errorf("got %#v, want *os.PathError on 2.json", err)
	}
}

func TestFaultLinkError(t *testing.T) {
	dir := t.TempDir()
	fsys := Fault{FS: OS{}, Hook: ErrnoAt(OpLink, "", 1, syscall.EEXIST)}
	r, err := fsys.OpenRoot(dir)
	must(t, err)
	defer r.Close()

	err = r.Link("a", "b")
	var le *os.LinkError
	if !errors.As(err, &le) || le.Old != "a" || le.New != "b" {
		t.Fatalf("got %#v, want *os.LinkError a → b", err)
	}
	wantErrno(t, err, syscall.EEXIST)
}

// An injected unlock fault still releases the real lock.
func TestFaultUnlockReleases(t *testing.T) {
	dir := t.TempDir()
	fsys := Fault{FS: OS{}, Hook: ErrnoAt(OpUnlock, "", 1, syscall.EIO)}
	r, err := fsys.OpenRoot(dir)
	must(t, err)
	defer r.Close()

	l, err := r.Lock()
	must(t, err)
	wantErrno(t, l.Unlock(), syscall.EIO)
	l, err = r.Lock()
	must(t, err)
	must(t, l.Unlock())
}

func TestHooksFirstErrorWins(t *testing.T) {
	h := Hooks(
		ErrnoAt(OpRemove, "", 1, syscall.EACCES),
		ErrnoAt(OpRemove, "", 1, syscall.EIO),
	)
	if err := h(Op{Name: OpRemove}); err != syscall.EACCES {
		t.Errorf("got %v", err)
	}
}

// CrashBefore kills the process, so it runs in a child: the test binary
// re-executed with FSYS_CRASH_DIR set.
func TestCrashBefore(t *testing.T) {
	if dir := os.Getenv("FSYS_CRASH_DIR"); dir != "" {
		fsys := Fault{FS: OS{}, Hook: CrashBefore(2)}
		r, _ := fsys.OpenRoot(dir)
		r.Mkdir("a", 0o755)
		r.Mkdir("b", 0o755)
		os.Exit(0)
	}
	dir := t.TempDir()
	exe, err := os.Executable()
	must(t, err)
	p, err := os.StartProcess(exe, []string{exe, "-test.run=^TestCrashBefore$"}, &os.ProcAttr{
		Env:   append(os.Environ(), "FSYS_CRASH_DIR="+dir),
		Files: []*os.File{nil, nil, os.Stderr},
	})
	must(t, err)
	st, err := p.Wait()
	must(t, err)
	ws := st.Sys().(syscall.WaitStatus)
	if !ws.Signaled() || ws.Signal() != syscall.SIGKILL {
		t.Fatalf("child ended %v, want killed by SIGKILL", st)
	}
	if _, err := os.Lstat(filepath.Join(dir, "a")); err != nil {
		t.Errorf("step 1 did not happen: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "b")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("step 2 happened: %v", err)
	}
}
