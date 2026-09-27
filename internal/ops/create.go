package ops

import (
	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/model"
)

// CreateInput is create's input, defaults applied. Title is trimmed.
type CreateInput struct {
	Title     model.Title
	Folder    model.FolderPath
	Priority  *int64
	Tags      []model.Tag
	BlockedBy []model.ID
	Extra     *jsonio.Object
	Notes     string
}

func decodeCreate(f *model.Fields, p *model.Problems) any {
	in := CreateInput{Tags: []model.Tag{}, BlockedBy: []model.ID{}, Extra: &jsonio.Object{}}
	if v, ok := f.Required("title"); ok {
		if s, ok := p.String(v, "/title"); ok {
			in.Title, _ = p.TitleInput(s, "/title")
		}
	}
	in.Folder = optionalFolder(f, p, "folder")
	if v, ok := f.Optional("priority"); ok {
		in.Priority, _ = p.Priority(v, "/priority")
	}
	if v, ok := f.Optional("tags"); ok {
		in.Tags, _ = p.Tags(v, "/tags")
	}
	if v, ok := f.Optional("blocked_by"); ok {
		in.BlockedBy, _ = p.IDs(v, "/blocked_by")
	}
	if v, ok := f.Optional("extra"); ok {
		if _, ok := p.Object(v, "/extra"); ok {
			in.Extra = v.(*jsonio.Object)
		}
	}
	if v, ok := f.Optional("notes"); ok {
		in.Notes, _ = p.String(v, "/notes")
	}
	return in
}
