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

// OpenRoot on a path that leads to a non-directory fails with ENOTDIR. The
// path is Stat'ed first: os.OpenRoot opens it before checking its type, and
// opening a FIFO blocks. os.OpenRoot's own report of a non-directory — for
// one swapped in after the Stat — holds no errno, which store could not
// classify, so it is replaced too.
func (OS) OpenRoot(p string) (Root, error) {
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
		return nil, &os.PathError{Op: "open", Path: p, Err: syscall.ENOTDIR}
	}
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

// ReadFile reads anything that is neither a regular file nor a directory —
// a FIFO, a socket, a device — as empty, without reading it: a FIFO with a
// writer would block on Linux and fail with EAGAIN on macOS, and a device
// may never end.
func (r *osRoot) ReadFile(name string) ([]byte, error) {
	f, fi, err := r.openNoFollow(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if !fi.Mode().IsRegular() && !fi.IsDir() {
		return []byte{}, nil
	}
	return io.ReadAll(f)
}

func (r *osRoot) ReadDir(name string) ([]fs.DirEntry, error) {
	f, _, err := r.openNoFollow(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}

// beforeOpen, when set by a test, runs between openNoFollow's Lstat and its
// open, so the test can swap the entry.
var beforeOpen func(name string)

// openAttempts bounds openNoFollow's retries when the entry keeps changing
// between its Lstat and its open.
const openAttempts = 3

// openNoFollow opens name for reading without following a symlink in its
// last component. os.Root ignores O_NOFOLLOW: it opens with O_NOFOLLOW
// itself, and on ELOOP follows the symlink when its target stays inside the
// root. So the entry is Lstat'ed first — a symlink fails with ELOOP — and
// the opened file must be the one Lstat saw. If it is not, the entry changed
// in between and is examined again: a symlink swapped in then fails with
// ELOOP, and a file replaced by a concurrent write is read in its new
// version. An entry that changes on every attempt fails with EAGAIN.
//
// os.SameFile compares device and inode only, so an inode reused within the
// window — a file removed, another created with its number, and a symlink to
// it swapped in — passes. That is an outside change, outside the contract,
// and os.Root still keeps the open inside the root.
//
// O_NONBLOCK keeps a FIFO from blocking the open. The returned FileInfo is
// the opened file's.
func (r *osRoot) openNoFollow(name string) (*os.File, fs.FileInfo, error) {
	for range openAttempts {
		before, err := r.r.Lstat(name)
		if err != nil {
			return nil, nil, err
		}
		if before.Mode()&fs.ModeSymlink != 0 {
			return nil, nil, &os.PathError{Op: "open", Path: name, Err: syscall.ELOOP}
		}
		if beforeOpen != nil {
			beforeOpen(name)
		}
		f, err := r.r.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, nil, err
		}
		after, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, nil, err
		}
		if os.SameFile(before, after) {
			return f, after, nil
		}
		f.Close()
	}
	return nil, nil, &os.PathError{Op: "open", Path: name, Err: syscall.EAGAIN}
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
