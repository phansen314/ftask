package ops

import (
	"strings"

	"github.com/phansen314/ftask/internal/model"
)

// InitInput is init's input. Root is absolute, cleaned, and has no ".."
// segment. Whether anything at Root leads to a directory is checked by the
// operation, which must look at the filesystem.
type InitInput struct {
	Root          string
	ReplaceConfig bool
}

func decodeInit(f *model.Fields, p *model.Problems) any {
	var in InitInput
	if v, ok := f.Required("root"); ok {
		if s, ok := p.String(v, "/root"); ok {
			in.Root, _ = cleanRoot(s, p)
		}
	}
	in.ReplaceConfig = optionalBool(f, p, "replace_config", false)
	return in
}

// cleanRoot cleans root lexically — no trailing "/", no empty or "."
// segments — then checks that it is absolute and has no ".." segment, which
// cleaning never removes (see design-spec.md, Root path).
func cleanRoot(root string, p *model.Problems) (string, bool) {
	clean := model.CleanPath(root)
	switch {
	case !strings.HasPrefix(clean, "/"):
		p.AddAdditional("/root", "must be an absolute path (resolving ~ or a relative path is the caller's job)")
		return "", false
	case model.HasDotDot(clean):
		p.AddAdditional("/root", `must not contain ".." segments`)
		return "", false
	}
	return clean, true
}
