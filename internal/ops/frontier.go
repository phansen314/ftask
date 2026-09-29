package ops

import (
	"cmp"
	"slices"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/model"
	"github.com/phansen314/ftask/internal/store"
)

// ScopeInput is the input of frontier and list, defaults applied.
// IncludeComplete and IncludeFolders are list's only.
type ScopeInput struct {
	Folder          model.FolderPath
	Recursive       bool
	IncludeComplete bool
	IncludeFolders  bool
}

func decodeFrontier(f *model.Fields, p *model.Problems) any {
	return ScopeInput{
		Folder:    optionalFolder(f, p, "folder"),
		Recursive: optionalBool(f, p, "recursive", true),
	}
}

// FrontierOutput is frontier's result (frontier-output).
type FrontierOutput struct {
	Tasks []model.TaskView `json:"tasks"`
}

// runFrontier returns the ready tasks in scope in the order to work on them
// (frontierOrder). Blockers are looked up anywhere in the
// tree, and every task-file problem is a warning, so one bad file never
// fails the frontier.
func runFrontier(env Env, in ScopeInput, w *errs.Collector) (any, *errs.Error) {
	out := FrontierOutput{Tasks: []model.TaskView{}}
	e := store.Read(env.Env, w, func(tx *store.Tx) *errs.Error {
		_, views, e := inScope(tx, ScopeInput{Folder: in.Folder, Recursive: in.Recursive})
		if e != nil {
			return e
		}
		for _, v := range views {
			if v.Readiness == model.Ready {
				out.Tasks = append(out.Tasks, v)
			}
		}
		slices.SortFunc(out.Tasks, frontierOrder)
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}

// frontierOrder is frontier order (operations.md, frontier, Order):
// priority, highest first, no priority after every prioritized task; then
// ID, lowest first; then, for copies of a duplicated ID — each in its own
// folder — tree order. It is a total order, so the sort needs no stability.
func frontierOrder(a, b model.TaskView) int {
	switch {
	case a.Priority == nil && b.Priority != nil:
		return 1
	case a.Priority != nil && b.Priority == nil:
		return -1
	case a.Priority != nil && *a.Priority != *b.Priority:
		return cmp.Compare(*b.Priority, *a.Priority)
	}
	return cmp.Or(cmp.Compare(a.ID, b.ID), store.CompareFolders(a.Folder, b.Folder))
}
