package fsys

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"syscall"
)

// OS is the real filesystem.
type OS struct{}

var _ FS = OS{}

// OpenRoot on a path that leads to a non-directory fails with ENOTDIR.
// os.OpenRoot reports that case with an error holding no errno, which store
// could not classify, so it is replaced here.
func (OS) OpenRoot(p string) (Root, error) {
	r, err := os.OpenRoot(p)
	if err != nil {
		var errno syscall.Errno
		if !errors.As(err, &errno) {
			if fi, serr := os.Stat(p); serr == nil && !fi.IsDir() {
				return nil, &os.PathError{Op: "open", Path: p, Err: syscall.ENOTDIR}
			}
		}
		return nil, err
	}
	return &osRoot{r: r}, nil
}

func (OS) Mkdir(p string, perm fs.FileMode) error    { return os.Mkdir(p, perm) }
func (OS) MkdirAll(p string, perm fs.FileMode) error { return os.MkdirAll(p, perm) }
func (OS) ReadFile(p string) ([]byte, error)         { return os.ReadFile(p) }

type osRoot struct {
	r *os.Root
}

func (r *osRoot) Name() string                              { return r.r.Name() }
func (r *osRoot) Lstat(name string) (fs.FileInfo, error)    { return r.r.Lstat(name) }
func (r *osRoot) Mkdir(name string, perm fs.FileMode) error { return r.r.Mkdir(name, perm) }
func (r *osRoot) Link(oldname, newname string) error        { return r.r.Link(oldname, newname) }
func (r *osRoot) Rename(oldname, newname string) error      { return r.r.Rename(oldname, newname) }
func (r *osRoot) Remove(name string) error                  { return r.r.Remove(name) }
func (r *osRoot) Close() error                              { return r.r.Close() }

func (r *osRoot) ReadFile(name string) ([]byte, error) {
	f, err := r.openNoFollow(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func (r *osRoot) ReadDir(name string) ([]fs.DirEntry, error) {
	f, err := r.openNoFollow(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}

// beforeOpen, when set by a test, runs between openNoFollow's Lstat and its
// open, so the test can swap the entry.
var beforeOpen func(name string)

// openNoFollow opens name for reading without following a symlink in its
// last component. os.Root ignores O_NOFOLLOW: it opens with O_NOFOLLOW
// itself, and on ELOOP follows the symlink when its target stays inside the
// root. So the entry is Lstat'ed first — a symlink fails with ELOOP — and
// the opened file must be the one Lstat saw; an entry swapped for a symlink
// in between fails with ELOOP too.
//
// O_NONBLOCK keeps a FIFO from blocking the open; reading one with no writer
// gives end of file.
func (r *osRoot) openNoFollow(name string) (*os.File, error) {
	before, err := r.r.Lstat(name)
	if err != nil {
		return nil, err
	}
	if before.Mode()&fs.ModeSymlink != 0 {
		return nil, &os.PathError{Op: "open", Path: name, Err: syscall.ELOOP}
	}
	if beforeOpen != nil {
		beforeOpen(name)
	}
	f, err := r.r.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !os.SameFile(before, after) {
		f.Close()
		return nil, &os.PathError{Op: "open", Path: name, Err: syscall.ELOOP}
	}
	return f, nil
}

// TempPrefix begins the name of every temp file ftask creates.
const TempPrefix = ".ftask-tmp-"

func (r *osRoot) CreateTemp(dir string) (File, string, error) {
	var b [12]byte
	rand.Read(b[:])
	name := path.Join(dir, TempPrefix+hex.EncodeToString(b[:]))
	f, err := r.r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, "", err
	}
	return f, name, nil
}
