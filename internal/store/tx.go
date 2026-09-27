package store

import (
	"fmt"
	"syscall"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/fsys"
	"github.com/phansen314/ftask/internal/model"
)

// Tx is one operation's — or one composed command's — access to a usable
// root: every call goes through the one fsys.Root opened for it
// (implementation-spec.md, Filesystem access). It is not safe for concurrent
// use.
type Tx struct {
	root     fsys.Root
	rootPath string // the root as reported: as stored, cleaned, "~/" expanded
	meta     model.RootFile
	write    bool
	warn     *errs.Collector
	index    *Index
	cache    map[Location]*Loaded
}

// Read runs fn over the root without the lock, after the Root states checks
// (operations.md, Precedence step 2). Warnings go to w.
func Read(env Env, w *errs.Collector, fn func(*Tx) *errs.Error) *errs.Error {
	tx, e := begin(env, w)
	if e != nil {
		return e
	}
	defer tx.root.Close()
	return fn(tx)
}

// Write runs fn holding the write lock: after the Root states checks, it
// takes the lock (busy if it is held) and re-reads ftask.json, since a write
// decides on current state. The lock is released when fn returns.
func Write(env Env, w *errs.Collector, fn func(*Tx) *errs.Error) *errs.Error {
	tx, e := begin(env, w)
	if e != nil {
		return e
	}
	defer tx.root.Close()
	lock, err := tx.root.Lock()
	if err != nil {
		if isErrno(err, syscall.EAGAIN) {
			return errs.Busy()
		}
		return errs.FromOS(tx.rootPath, err)
	}
	defer lock.Unlock()
	ms := readMeta(tx.root)
	if e := metaError(ms, tx.rootPath); e != nil {
		return e
	}
	tx.meta, tx.write = ms.meta, true
	return fn(tx)
}

func begin(env Env, w *errs.Collector) (*Tx, *errs.Error) {
	if w == nil {
		w = &errs.Collector{}
	}
	rootPath, e := locateRoot(env)
	if e != nil {
		return nil, e
	}
	r, e := openRoot(env, rootPath)
	if e != nil {
		return nil, e
	}
	ms := readMeta(r)
	if e := metaError(ms, rootPath); e != nil {
		r.Close()
		return nil, e
	}
	return &Tx{root: r, rootPath: rootPath, meta: ms.meta, warn: w, cache: map[Location]*Loaded{}}, nil
}

// Meta is ftask.json's content as last read or written.
func (tx *Tx) Meta() model.RootFile { return tx.meta }

// Path is the filesystem path ftask reports for rel, a slash-separated path
// relative to the root ("." for the root itself): the root as stored, with
// "~/" expanded, joined with rel (design-spec.md, Root path).
func (tx *Tx) Path(rel string) string { return joinPath(tx.rootPath, rel) }

// Warn records a warning in the invocation's collector.
func (tx *Tx) Warn(w errs.Warning) { tx.warn.Add(w) }

// OSError is the io error for err, an OS error on rel, from a call site
// that gives its errno no other meaning.
func (tx *Tx) OSError(rel string, err error) *errs.Error {
	return errs.FromOS(tx.Path(rel), err)
}

// code is err's symbolic OS error name, for a warning; an error with no name
// is internal.
func (tx *Tx) code(rel string, err error) (string, *errs.Error) {
	e := tx.OSError(rel, err)
	if d, ok := e.Details.(errs.IODetails); ok {
		return d.Code, nil
	}
	return "", e
}

func (tx *Tx) mustWrite(what string) *errs.Error {
	if !tx.write {
		return errs.Internal(fmt.Sprintf("%s outside a write", what))
	}
	return nil
}

func joinPath(dir, rel string) string {
	switch {
	case rel == ".":
		return dir
	case dir == "/":
		return "/" + rel
	case dir == ".":
		return rel
	}
	return dir + "/" + rel
}
