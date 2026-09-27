package ops

import "github.com/phansen314/ftask/internal/model"

func decodeList(f *model.Fields, p *model.Problems) any {
	return ScopeInput{
		Folder:          optionalFolder(f, p, "folder"),
		Recursive:       optionalBool(f, p, "recursive", true),
		IncludeComplete: optionalBool(f, p, "include_complete", false),
		IncludeFolders:  optionalBool(f, p, "include_folders", false),
	}
}
