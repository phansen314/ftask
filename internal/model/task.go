package model

import (
	"cmp"
	"slices"

	"github.com/phansen314/ftask/internal/jsonio"
)

// TaskFile is a task file's content (see design-spec.md, Task file schema).
// Field order is the schema's key order, which is the order written.
type TaskFile struct {
	Schema      int64          `json:"schema"`
	ID          ID             `json:"id"`
	Title       Title          `json:"title"`
	Priority    *int64         `json:"priority"`
	CreatedAt   Timestamp      `json:"created_at"`
	CompletedAt *Timestamp     `json:"completed_at"`
	BlockedBy   []ID           `json:"blocked_by"`
	Tags        []Tag          `json:"tags"`
	Extra       *jsonio.Object `json:"extra"`
}

// Open reports whether the task is open: completed_at is its sole source of
// truth.
func (t *TaskFile) Open() bool { return t.CompletedAt == nil }

// Normalize puts t in the form it is written and returned in: sets sorted
// (blocked_by ascending, tags by name) and never nil, extra never nil.
func (t *TaskFile) Normalize() {
	t.BlockedBy = slices.Clone(t.BlockedBy)
	if t.BlockedBy == nil {
		t.BlockedBy = []ID{}
	}
	slices.Sort(t.BlockedBy)
	t.Tags = slices.Clone(t.Tags)
	if t.Tags == nil {
		t.Tags = []Tag{}
	}
	slices.SortFunc(t.Tags, func(a, b Tag) int { return cmp.Compare(a, b) })
	if t.Extra == nil {
		t.Extra = &jsonio.Object{}
	}
}

// Encode returns the task file's bytes, per design-spec.md, File format.
func (t TaskFile) Encode() ([]byte, error) {
	t.Normalize()
	return jsonio.MarshalFile(t)
}

// Task is a task as operations return it: the task file's fields, plus where
// it lives (the shared task schema).
type Task struct {
	TaskFile
	Folder    FolderPath `json:"folder"`
	NotesPath string     `json:"notes_path"`
}

// Readiness is a task's derived state (see design-spec.md, Dependencies).
type Readiness string

const (
	Ready    Readiness = "ready"
	Blocked  Readiness = "blocked"
	Complete Readiness = "complete"
)

// TaskView is a task plus its derived readiness (the shared task-view
// schema). Blocking lists, ascending, the blocked_by IDs that currently
// block it; it is non-empty exactly when Readiness is Blocked.
type TaskView struct {
	Task
	Readiness Readiness `json:"readiness"`
	Blocking  []ID      `json:"blocking"`
}

// Normalize puts v in the form it is returned in: the task file's fields as
// TaskFile.Normalize leaves them, and Blocking sorted and never nil.
func (v *TaskView) Normalize() {
	v.TaskFile.Normalize()
	v.Blocking = slices.Clone(v.Blocking)
	if v.Blocking == nil {
		v.Blocking = []ID{}
	}
	slices.Sort(v.Blocking)
}

// RootFile is ftask.json's content (see design-spec.md, Root metadata).
type RootFile struct {
	Schema int64 `json:"schema"`
	LastID int64 `json:"last_id"`
}

// Encode returns ftask.json's bytes, per design-spec.md, File format.
func (r RootFile) Encode() ([]byte, error) {
	return jsonio.MarshalFile(r)
}
