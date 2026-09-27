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
	clean := CleanPath(root)
	switch {
	case !strings.HasPrefix(clean, "/"):
		p.AddAdditional("/root", "must be an absolute path (resolving ~ or a relative path is the caller's job)")
		return "", false
	case strings.Contains(clean+"/", "/../"):
		p.AddAdditional("/root", `must not contain ".." segments`)
		return "", false
	}
	return clean, true
}

// CleanPath removes a path's empty and "." segments and any trailing "/",
// lexically: ".." segments are kept, and symlinks are not resolved.
func CleanPath(path string) string {
	abs := strings.HasPrefix(path, "/")
	var segs []string
	for _, s := range strings.Split(path, "/") {
		if s != "" && s != "." {
			segs = append(segs, s)
		}
	}
	joined := strings.Join(segs, "/")
	switch {
	case abs:
		return "/" + joined
	case joined == "":
		return "."
	}
	return joined
}
