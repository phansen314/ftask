package store

import (
	"cmp"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/model"
)

// Location is where a task file is: its folder and the ID in its filename.
type Location struct {
	Folder model.FolderPath
	ID     model.ID
}

// Rel is the task file's path relative to the root.
func (l Location) Rel() string { return l.file("json") }

// NotesRel is the path of the task's .md relative to the root.
func (l Location) NotesRel() string { return l.file("md") }

func (l Location) file(ext string) string {
	return joinPath(FolderRel(l.Folder), strconv.FormatInt(int64(l.ID), 10)+"."+ext)
}

// FolderRel is a folder's path relative to the root: "." for the root.
func FolderRel(f model.FolderPath) string {
	if f == model.RootFolder {
		return "."
	}
	return string(f)[1:]
}

func childFolder(f model.FolderPath, name string) model.FolderPath {
	if f == model.RootFolder {
		return model.FolderPath("/" + name)
	}
	return f + model.FolderPath("/"+name)
}

// WalkFolder runs the path walk (operations.md, Path walk) of f: each entry
// on its path, from the root down, must be a plain directory. It returns how
// many of f's segments exist, stopping at the first missing one; f exists
// when that is all of them. An entry that is present but not a directory, or
// is a symlink, is corrupt (unexpected-file).
func (tx *Tx) WalkFolder(f model.FolderPath) (int, *errs.Error) {
	segs := f.Segments()
	rel := "."
	for i, s := range segs {
		rel = joinPath(rel, s)
		fi, err := tx.root.Lstat(rel)
		switch {
		case isErrno(err, syscall.ENOENT):
			return i, nil
		case isErrno(err, syscall.ELOOP), isErrno(err, syscall.ENOTDIR):
			return i, errs.Corrupt(tx.Path(rel), errs.CorruptUnexpectedFile)
		case err != nil:
			return i, tx.OSError(rel, err)
		case !fi.IsDir() || fi.Mode()&fs.ModeSymlink != 0:
			return i, errs.Corrupt(tx.Path(rel), errs.CorruptUnexpectedFile)
		}
	}
	return len(segs), nil
}

// Index is the tree walked by name only, no file read (implementation-spec.md,
// The index). Folders and Tasks are in tree order.
type Index struct {
	Folders []model.FolderPath
	Tasks   []Location
	// Unreadable lists, in tree order, the folders that could not be listed;
	// their tasks and subfolders are missing from the index.
	Unreadable []UnreadableFolder
	byID       map[model.ID][]Location
}

// UnreadableFolder is a folder the walk could not list, with the OS error.
type UnreadableFolder struct {
	Folder model.FolderPath
	Err    error
}

// Locations returns every location of id's task files, in tree order.
func (x *Index) Locations(id model.ID) []Location { return x.byID[id] }

// Complete reports whether every folder was listed.
func (x *Index) Complete() bool { return len(x.Unreadable) == 0 }

// InScope returns the folders and tasks under f — in f itself, or anywhere
// below it when recursive — in tree order.
func (x *Index) InScope(f model.FolderPath, recursive bool) ([]model.FolderPath, []Location) {
	in := func(g model.FolderPath) bool {
		switch {
		case g == f:
			return true
		case !recursive:
			return false
		case f == model.RootFolder:
			return true
		}
		return strings.HasPrefix(string(g), string(f)+"/")
	}
	var folders []model.FolderPath
	for _, g := range x.Folders {
		if in(g) {
			folders = append(folders, g)
		}
	}
	var tasks []Location
	for _, l := range x.Tasks {
		if in(l.Folder) {
			tasks = append(tasks, l)
		}
	}
	return folders, tasks
}

var (
	folderName   = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
	taskFileName = regexp.MustCompile(`^[1-9][0-9]{0,14}\.json$`)
)

// Index walks the whole tree once, on first use, and returns the same index
// for the rest of the transaction. Folders that cannot be listed are
// recorded, not returned: whether one is an error or a warning is the
// operation's call (see RequireWholeTree, WarnUnreadable).
func (tx *Tx) Index() *Index {
	if tx.index == nil {
		x := &Index{byID: map[model.ID][]Location{}}
		tx.walk(x, model.RootFolder)
		tx.index = x
	}
	return tx.index
}

// walk lists f and descends into its subfolders: a folder before its
// subfolders, subfolders by name, tasks by numeric ID — tree order.
func (tx *Tx) walk(x *Index, f model.FolderPath) {
	entries, err := tx.root.ReadDir(FolderRel(f))
	if err != nil {
		// A folder other than the root that is gone, or is no longer a
		// directory, was moved or removed mid-read: skipped silently, like a
		// vanished file.
		if f != model.RootFolder && (isErrno(err, syscall.ENOENT) || isErrno(err, syscall.ENOTDIR) || isErrno(err, syscall.ELOOP)) {
			return
		}
		x.Unreadable = append(x.Unreadable, UnreadableFolder{Folder: f, Err: err})
		return
	}
	var ids []model.ID
	var subs []string
	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasPrefix(name, "."):
		case folderName.MatchString(name) && e.Type().IsDir():
			subs = append(subs, name)
		case taskFileName.MatchString(name) && e.Type().IsRegular():
			id, _ := strconv.ParseInt(strings.TrimSuffix(name, ".json"), 10, 64)
			ids = append(ids, model.ID(id))
		}
	}
	slices.Sort(ids)
	slices.SortFunc(subs, strings.Compare)
	x.Folders = append(x.Folders, f)
	for _, id := range ids {
		l := Location{Folder: f, ID: id}
		x.Tasks = append(x.Tasks, l)
		x.byID[id] = append(x.byID[id], l)
	}
	for _, s := range subs {
		tx.walk(x, childFolder(f, s))
	}
}

// RequireWholeTree is the error for an operation that must see the whole
// tree — to find an ID, or prove it absent or unique — when a folder could
// not be listed: io, for the first in tree order.
func (tx *Tx) RequireWholeTree(x *Index) *errs.Error {
	if x.Complete() {
		return nil
	}
	u := x.Unreadable[0]
	return tx.OSError(FolderRel(u.Folder), u.Err)
}

// WarnUnreadable records an unreadable-folder warning for each folder that
// could not be listed, for an operation that returns a collection.
func (tx *Tx) WarnUnreadable(x *Index) *errs.Error {
	for _, u := range x.Unreadable {
		rel := FolderRel(u.Folder)
		code, e := tx.code(rel, u.Err)
		if e != nil {
			return e
		}
		tx.Warn(errs.UnreadableFolder(tx.Path(rel), code))
	}
	return nil
}

// Copies returns the locations of id's task files that still exist, in tree
// order. When the index has several, each is checked again with Lstat and
// those gone are dropped: a task moved mid-read is then counted once, at the
// location seen last, not reported as a duplicate (implementation-spec.md,
// Concurrent writes during a read).
func (tx *Tx) Copies(id model.ID) ([]Location, *errs.Error) {
	locs := tx.Index().Locations(id)
	if len(locs) <= 1 {
		return locs, nil
	}
	var out []Location
	for _, l := range locs {
		_, err := tx.root.Lstat(l.Rel())
		switch {
		case isErrno(err, syscall.ENOENT):
			continue
		case err != nil:
			return nil, tx.OSError(l.Rel(), err)
		}
		out = append(out, l)
	}
	return out, nil
}

// CompareLocations orders locations in tree order: by folder (a parent before
// its children, siblings by name), then by ID.
func CompareLocations(a, b Location) int {
	return cmp.Or(CompareFolders(a.Folder, b.Folder), cmp.Compare(a.ID, b.ID))
}

// CompareFolders orders folder paths in tree order (operations.md, Tree
// order): segment by segment, so /proj/travel comes before /proj-b.
func CompareFolders(a, b model.FolderPath) int {
	return slices.Compare(a.Segments(), b.Segments())
}
