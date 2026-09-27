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
// os.OpenRoot opens the path without O_DIRECTORY and checks its type only
// afterwards, so a FIFO would block the open, and a regular file is reported
// by an error with no errno. It is therefore handed p + "/.": resolving that
// requires p to be a directory, so the kernel refuses a FIFO or regular file
// with ENOTDIR before opening anything, with no window for a swap. Error
// paths are restored to p. An empty path, which would become "/.", fails
// with ENOENT as open(2) fails on it.
func (OS) OpenRoot(p string) (Root, error) {
	if p == "" {
		return nil, &os.PathError{Op: "open", Path: p, Err: syscall.ENOENT}
	}
	r, err := os.OpenRoot(p + "/.")
	if err != nil {
		var pe *os.PathError
		if errors.As(err, &pe) {
			pe.Path = p
		}
		return nil, err
	}
	return &osRoot{r: r, name: p}, nil
}

func (OS) Mkdir(p string, perm fs.FileMode) error    { return os.Mkdir(p, perm) }
func (OS) MkdirAll(p string, perm fs.FileMode) error { return os.MkdirAll(p, perm) }
func (OS) ReadFile(p string) ([]byte, error)         { return os.ReadFile(p) }

type osRoot struct {
	r    *os.Root
	name string
}

func (r *osRoot) Name() string                              { return r.name }
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
// Lstat'ed again after the open. The opened file must be the one both saw,
// and the second Lstat must not see a symlink: a regular file renamed away
// and replaced by a symlink to it passes the first comparison, since the open
// follows the symlink to the very inode the first Lstat saw, but the second
// Lstat sees the symlink and fails with ELOOP. If the files differ, the entry
// changed in between and is examined again: a file replaced by a concurrent
// write is read in its new version. An entry that changes on every attempt
// fails with EAGAIN.
//
// os.SameFile compares device and inode only, so an inode reused within the
// window — a file removed, another created with its number, and a symlink to
// it swapped in and out again — passes. That is an outside change, outside
// the contract, and os.Root still keeps the open inside the root.
//
// O_NONBLOCK keeps a FIFO from blocking the open. The returned FileInfo is
// the opened file's.
func (r *osRoot) openNoFollow(name string) (*os.File, fs.FileInfo, error) {
	for range openAttempts {
		before, err := r.lstatNoFollow(name)
		if err != nil {
			return nil, nil, err
		}
		if beforeOpen != nil {
			beforeOpen(name)
		}
		f, err := r.r.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, nil, err
		}
		opened, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, nil, err
		}
		if !os.SameFile(before, opened) {
			f.Close()
			continue
		}
		after, err := r.lstatNoFollow(name)
		if err != nil {
			f.Close()
			return nil, nil, err
		}
		if os.SameFile(after, opened) {
			return f, opened, nil
		}
		f.Close()
	}
	return nil, nil, &os.PathError{Op: "open", Path: name, Err: syscall.EAGAIN}
}

// lstatNoFollow Lstats name, failing with ELOOP on a symlink.
func (r *osRoot) lstatNoFollow(name string) (fs.FileInfo, error) {
	fi, err := r.r.Lstat(name)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return nil, &os.PathError{Op: "open", Path: name, Err: syscall.ELOOP}
	}
	return fi, nil
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
