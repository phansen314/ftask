package ops

import (
	"maps"
	"slices"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/model"
)

// decoder is an operation's input adapter: it turns the input object into the
// operation's input type, recording every problem in p. The input type is
// meaningful only when p ends up OK.
type decoder func(f *model.Fields, p *model.Problems) any

// decoders holds each operation's input adapter, by operation name.
var decoders = map[string]decoder{
	"version":       decodeVersion,
	"info":          decodeInfo,
	"init":          decodeInit,
	"create-folder": decodeCreateFolder,
	"create":        decodeCreate,
	"show":          decodeShow,
	"complete":      decodeComplete,
	"reopen":        decodeReopen,
	"block":         decodeBlock,
	"unblock":       decodeUnblock,
	"update":        decodeUpdate,
	"frontier":      decodeFrontier,
	"list":          decodeList,
}

// Operations returns the name of every operation, sorted.
func Operations() []string {
	return slices.Sorted(maps.Keys(decoders))
}

// Decode checks the input of operation op, already read by jsonio (repeated
// keys rejected there), and returns its typed form and every problem found.
// The input is meaningful only when there are none. The caller reports
// problems as one invalid-input error, after adding any from checks that
// need the filesystem (init's; see implementation-spec.md, Where it happens).
func Decode(op string, input *jsonio.Object) (any, *model.Problems, *errs.Error) {
	d, ok := decoders[op]
	if !ok {
		return nil, nil, errs.Internal("no operation " + op)
	}
	var p model.Problems
	in := decode(d, input, &p)
	return in, &p, nil
}

func decode(d decoder, input *jsonio.Object, p *model.Problems) any {
	f, _ := p.Object(input, "")
	in := d(f, p)
	f.Done()
	return in
}

// optionalBool returns the boolean field key, or def when it is absent.
func optionalBool(f *model.Fields, p *model.Problems, key string, def bool) bool {
	if v, ok := f.Optional(key); ok {
		b, _ := p.Bool(v, f.Ptr(key))
		return b
	}
	return def
}

// optionalFolder returns the folder path field key, or the root when absent.
func optionalFolder(f *model.Fields, p *model.Problems, key string) model.FolderPath {
	if v, ok := f.Optional(key); ok {
		fp, _ := p.FolderPath(v, f.Ptr(key))
		return fp
	}
	return model.RootFolder
}

// requiredID returns the task ID field key.
func requiredID(f *model.Fields, p *model.Problems, key string) model.ID {
	if v, ok := f.Required(key); ok {
		id, _ := p.ID(v, f.Ptr(key))
		return id
	}
	return 0
}

// blockerList returns the field key as a non-empty set of task IDs — block's
// and unblock's blockers — and whether it is valid.
func blockerList(f *model.Fields, p *model.Problems, key string) ([]model.ID, bool) {
	v, ok := f.Required(key)
	if !ok {
		return nil, false
	}
	ptr := f.Ptr(key)
	if a, isArr := v.([]any); isArr && len(a) == 0 {
		p.Add(ptr, "must list at least one ID")
		return nil, false
	}
	return p.IDs(v, ptr)
}

// IDInput is the input of show, complete, and reopen: one task.
type IDInput struct {
	ID model.ID
}

func decodeID(f *model.Fields, p *model.Problems) any {
	return IDInput{ID: requiredID(f, p, "id")}
}
