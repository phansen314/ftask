package ops

import (
	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/model"
	"github.com/phansen314/ftask/internal/store"
)

func decodeList(f *model.Fields, p *model.Problems) any {
	return ScopeInput{
		Folder:          optionalFolder(f, p, "folder"),
		Recursive:       optionalBool(f, p, "recursive", true),
		IncludeComplete: optionalBool(f, p, "include_complete", false),
		IncludeFolders:  optionalBool(f, p, "include_folders", false),
	}
}

// ListOutput is list's result (list-output). Folders is present only when
// include_folders is set.
type ListOutput struct {
	Folders *[]model.FolderPath `json:"folders,omitempty"`
	Tasks   []model.TaskView    `json:"tasks"`
}

// runList returns every task in scope, whatever its readiness — complete
// ones only on request — and, on request, the folders in scope.
func runList(env Env, in ScopeInput, w *errs.Collector) (any, *errs.Error) {
	var out ListOutput
	e := store.Read(env.Env, w, func(tx *store.Tx) *errs.Error {
		folders, views, e := inScope(tx, in)
		if e != nil {
			return e
		}
		if in.IncludeFolders {
			out.Folders = &folders
		}
		out.Tasks = views
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}
