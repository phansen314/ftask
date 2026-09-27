package fsys

import (
	"io"
	"io/fs"
)

// FS opens roots, and makes the few calls that happen outside one: init's
// creation of the root and the config directory, and reading the config.
type FS interface {
	// OpenRoot opens the directory at path, following symlinks. Every later
	// call through the Root is relative to this one resolution.
	OpenRoot(path string) (Root, error)
	// Mkdir creates one directory; init never creates the root's parent.
	Mkdir(path string, perm fs.FileMode) error
	// MkdirAll creates the config directory and any missing parents.
	MkdirAll(path string, perm fs.FileMode) error
	// ReadFile reads the config file, following symlinks: a config is often
	// a symlink into a dotfiles checkout.
	ReadFile(path string) ([]byte, error)
}

// Root is a directory opened once, through which every call is made. Names
// are slash-separated and relative to the root; "." is the root itself.
//
// No call follows a symlink in a name's last component: ReadFile and ReadDir
// fail with ELOOP on one, and Lstat reports it. Symlinks in earlier
// components that stay inside the root are followed, so callers that must
// not follow them (the path walk) Lstat each component in turn.
//
// A name that escapes the root — through "..", or a symlink in an earlier
// component that leads outside — fails with os.Root's error, which holds no
// errno, so store reports it as internal. Only a caller bug or an outside
// change mid-operation reaches it: the path walk sees such a symlink first.
type Root interface {
	// Name is the path the root was opened with.
	Name() string
	Lstat(name string) (fs.FileInfo, error)
	// ReadFile reads a whole file. A directory fails with EISDIR; a FIFO,
	// socket, or device reads as empty.
	ReadFile(name string) ([]byte, error)
	// ReadDir lists a directory in the order the OS gives; callers sort.
	ReadDir(name string) ([]fs.DirEntry, error)
	Mkdir(name string, perm fs.FileMode) error
	// CreateTemp creates a new, empty temp file in dir, exclusively, with
	// mode 0644 before the umask. Its name, .ftask-tmp-<random>, is hidden
	// from reads and recognizable to doctor. The returned name includes dir.
	CreateTemp(dir string) (f File, name string, err error)
	// Link publishes a new file: it fails with EEXIST rather than replace one.
	Link(oldname, newname string) error
	// Rename publishes a file over an existing one.
	Rename(oldname, newname string) error
	Remove(name string) error
	// Lock takes the write lock: flock(LOCK_EX|LOCK_NB) on the root
	// directory itself. A held lock fails with EAGAIN; EINTR is retried.
	Lock() (Lock, error)
	Close() error
}

// File is a temp file being written.
type File interface {
	io.Writer
	Close() error
}

// Lock is a held write lock. It keeps the locked descriptor referenced until
// Unlock, which closes it and so releases the lock.
type Lock interface {
	Unlock() error
}
