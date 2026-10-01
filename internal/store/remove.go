package store

import (
	"io/fs"
	"path"
	"syscall"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/fsys"
)

// Lstat reports the entry at rel without following a final symlink.
func (tx *Tx) Lstat(rel string) (fs.FileInfo, error) { return tx.root.Lstat(rel) }

// ReadFile reads the file at rel as it is, without checking it: doctor's
// comparison of duplicate copies.
func (tx *Tx) ReadFile(rel string) ([]byte, error) { return tx.root.ReadFile(rel) }

// Remove removes the file at rel, returning the OS error: whether one is an
// error is the caller's call.
func (tx *Tx) Remove(rel string) error {
	if e := tx.mustWrite("remove " + rel); e != nil {
		return e
	}
	return tx.root.Remove(rel)
}

// Move moves the file or folder at oldRel to newRel, which must be free:
// anything already there is corrupt (unexpected-file), since the caller
// checked under the lock that nothing is, so only an outside change can have
// put it there.
func (tx *Tx) Move(oldRel, newRel string) *errs.Error {
	if e := tx.mustWrite("move " + oldRel); e != nil {
		return e
	}
	err := tx.root.RenameNoReplace(oldRel, newRel)
	switch {
	case err == nil:
		return nil
	case isErrno(err, syscall.EEXIST), isErrno(err, syscall.ENOTEMPTY):
		return errs.Corrupt(tx.Path(newRel), errs.CorruptUnexpectedFile)
	}
	return tx.OSError(oldRel, err)
}

// LinkOver makes newRel a hard link to the file at oldRel, replacing any
// file already at newRel: linked to a temp name in newRel's folder, then
// renamed over it, so newRel is never missing and never partial. The temp
// link is removed if the rename fails.
func (tx *Tx) LinkOver(oldRel, newRel string) *errs.Error {
	if e := tx.mustWrite("link " + oldRel); e != nil {
		return e
	}
	tmp := fsys.TempName(path.Dir(newRel))
	if err := tx.root.Link(oldRel, tmp); err != nil {
		return tx.OSError(oldRel, err)
	}
	if err := tx.root.Rename(tmp, newRel); err != nil {
		tx.root.Remove(tmp)
		return tx.OSError(newRel, err)
	}
	return nil
}

// Discard removes a folder in one step as far as the tree is concerned: it
// is renamed aside, to a hidden temp name in the root, then removed with
// everything under it. Once renamed, it is gone from the tree, and Discard
// cannot fail: a removal that fails or is interrupted leaves a hidden
// leftover, which reads ignore and doctor reports.
func (tx *Tx) Discard(rel string) *errs.Error {
	tmp := fsys.TempName(".")
	if e := tx.Move(rel, tmp); e != nil {
		return e
	}
	tx.root.RemoveAll(tmp)
	return nil
}
