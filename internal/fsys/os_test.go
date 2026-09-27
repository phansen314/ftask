package fsys

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// newRoot creates a temp directory, runs setup in it, and opens it as a Root.
func newRoot(t *testing.T, setup func(dir string)) (Root, string) {
	t.Helper()
	dir := t.TempDir()
	if setup != nil {
		setup(dir)
	}
	r, err := OS{}.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r, dir
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func wantErrno(t *testing.T, err error, want syscall.Errno) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

// symlinkTree has a regular file, a directory, and a symlink to each, all
// inside the root: the symlinks os.Root itself would follow.
func symlinkTree(t *testing.T) func(string) {
	return func(dir string) {
		must(t, os.WriteFile(filepath.Join(dir, "1.json"), []byte("{}"), 0o644))
		must(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))
		must(t, os.Symlink("1.json", filepath.Join(dir, "2.json")))
		must(t, os.Symlink("sub", filepath.Join(dir, "link")))
	}
}

func TestReadFileRejectsSymlink(t *testing.T) {
	r, _ := newRoot(t, symlinkTree(t))
	got, err := r.ReadFile("1.json")
	must(t, err)
	if string(got) != "{}" {
		t.Fatalf("got %q", got)
	}
	_, err = r.ReadFile("2.json")
	wantErrno(t, err, syscall.ELOOP)
}

func TestReadDirRejectsSymlink(t *testing.T) {
	r, _ := newRoot(t, symlinkTree(t))
	if _, err := r.ReadDir("sub"); err != nil {
		t.Fatal(err)
	}
	_, err := r.ReadDir("link")
	wantErrno(t, err, syscall.ELOOP)
}

func TestLstatReportsSymlink(t *testing.T) {
	r, _ := newRoot(t, symlinkTree(t))
	fi, err := r.Lstat("link")
	must(t, err)
	if fi.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("mode %v, want a symlink", fi.Mode())
	}
}

// An entry swapped for a symlink between the Lstat and the open is caught by
// comparing the opened file with the one Lstat saw.
func TestReadFileRejectsSwap(t *testing.T) {
	r, dir := newRoot(t, func(dir string) {
		must(t, os.WriteFile(filepath.Join(dir, "1.json"), []byte("a"), 0o644))
		must(t, os.WriteFile(filepath.Join(dir, "2.json"), []byte("b"), 0o644))
	})
	beforeOpen = func(name string) {
		p := filepath.Join(dir, name)
		must(t, os.Remove(p))
		must(t, os.Symlink("2.json", p))
	}
	t.Cleanup(func() { beforeOpen = nil })
	_, err := r.ReadFile("1.json")
	wantErrno(t, err, syscall.ELOOP)
}

func TestReadFileDirectory(t *testing.T) {
	r, _ := newRoot(t, symlinkTree(t))
	_, err := r.ReadFile("sub")
	wantErrno(t, err, syscall.EISDIR)
}

func TestReadFileFIFODoesNotBlock(t *testing.T) {
	r, _ := newRoot(t, func(dir string) {
		must(t, syscall.Mkfifo(filepath.Join(dir, "1.json"), 0o644))
	})
	got, err := r.ReadFile("1.json")
	must(t, err)
	if len(got) != 0 {
		t.Fatalf("got %q", got)
	}
}

func TestReadFileMissing(t *testing.T) {
	r, _ := newRoot(t, nil)
	_, err := r.ReadFile("1.json")
	wantErrno(t, err, syscall.ENOENT)
}

func TestCreateTemp(t *testing.T) {
	old := syscall.Umask(0o027)
	t.Cleanup(func() { syscall.Umask(old) })

	r, dir := newRoot(t, func(dir string) {
		must(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))
	})
	f, name, err := r.CreateTemp("sub")
	must(t, err)
	if !strings.HasPrefix(name, "sub/"+TempPrefix) {
		t.Fatalf("name %q", name)
	}
	_, err = f.Write([]byte("hello"))
	must(t, err)
	must(t, f.Close())

	fi, err := os.Lstat(filepath.Join(dir, name))
	must(t, err)
	if got := fi.Mode(); got != 0o640 {
		t.Errorf("mode %v, want 0640 (0644 under umask 027)", got)
	}
	got, err := r.ReadFile(name)
	must(t, err)
	if string(got) != "hello" {
		t.Errorf("got %q", got)
	}

	_, name2, err := r.CreateTemp("sub")
	must(t, err)
	if name2 == name {
		t.Errorf("two temp files named %q", name)
	}
	_, root, err := r.CreateTemp(".")
	must(t, err)
	if !strings.HasPrefix(root, TempPrefix) {
		t.Errorf("name %q", root)
	}
}

func TestMkdirMode(t *testing.T) {
	old := syscall.Umask(0o027)
	t.Cleanup(func() { syscall.Umask(old) })

	r, dir := newRoot(t, nil)
	must(t, r.Mkdir("proj", 0o755))
	fi, err := os.Lstat(filepath.Join(dir, "proj"))
	must(t, err)
	if got := fi.Mode().Perm(); got != 0o750 {
		t.Errorf("mode %v, want 0750", got)
	}
	wantErrno(t, r.Mkdir("proj", 0o755), syscall.EEXIST)
}

func TestLinkNeverClobbers(t *testing.T) {
	r, _ := newRoot(t, func(dir string) {
		must(t, os.WriteFile(filepath.Join(dir, "tmp"), []byte("new"), 0o644))
		must(t, os.WriteFile(filepath.Join(dir, "1.json"), []byte("old"), 0o644))
	})
	err := r.Link("tmp", "1.json")
	wantErrno(t, err, syscall.EEXIST)
	var le *os.LinkError
	if !errors.As(err, &le) {
		t.Errorf("got %T, want *os.LinkError", err)
	}
	got, _ := r.ReadFile("1.json")
	if string(got) != "old" {
		t.Errorf("1.json is %q", got)
	}
	must(t, r.Link("tmp", "2.json"))
	got, _ = r.ReadFile("2.json")
	if string(got) != "new" {
		t.Errorf("2.json is %q", got)
	}
}

func TestRenameReplaces(t *testing.T) {
	r, _ := newRoot(t, func(dir string) {
		must(t, os.WriteFile(filepath.Join(dir, "tmp"), []byte("new"), 0o644))
		must(t, os.WriteFile(filepath.Join(dir, "1.json"), []byte("old"), 0o644))
	})
	must(t, r.Rename("tmp", "1.json"))
	got, _ := r.ReadFile("1.json")
	if string(got) != "new" {
		t.Errorf("1.json is %q", got)
	}
	_, err := r.Lstat("tmp")
	wantErrno(t, err, syscall.ENOENT)
}

func TestOpenRootErrors(t *testing.T) {
	dir := t.TempDir()
	_, err := OS{}.OpenRoot(filepath.Join(dir, "missing"))
	wantErrno(t, err, syscall.ENOENT)
	file := filepath.Join(dir, "file")
	must(t, os.WriteFile(file, nil, 0o644))
	_, err = OS{}.OpenRoot(file)
	wantErrno(t, err, syscall.ENOTDIR)
}

func TestFSReadFileFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "real.toml"), []byte("x"), 0o644))
	must(t, os.Symlink("real.toml", filepath.Join(dir, "config.toml")))
	got, err := OS{}.ReadFile(filepath.Join(dir, "config.toml"))
	must(t, err)
	if string(got) != "x" {
		t.Errorf("got %q", got)
	}
}
