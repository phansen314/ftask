package ops

import "github.com/phansen314/ftask/internal/model"

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
