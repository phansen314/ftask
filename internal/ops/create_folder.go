package ops

import "github.com/phansen314/ftask/internal/model"

// CreateFolderInput is create-folder's input.
type CreateFolderInput struct {
	Folder  model.FolderPath
	Parents bool
}

func decodeCreateFolder(f *model.Fields, p *model.Problems) any {
	var in CreateFolderInput
	if v, ok := f.Required("folder"); ok {
		in.Folder, _ = p.FolderPath(v, "/folder")
	}
	in.Parents = optionalBool(f, p, "parents", false)
	return in
}
