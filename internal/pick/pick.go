// Package pick is ftask pick, the interactive picker built on fzf
// (pick-spec.md). It runs no operation of its own: it validates its input
// through ops, like any command, and composes list and the write operations
// its keys run, each as its own call.
package pick

import (
	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/ops"
)

// Env is pick's environment: the operations' and the process's.
type Env struct {
	Ops ops.Env
	Sys System
}

// Run runs pick on in, the input the CLI built, with problems the CLI found
// while building it, and returns the one envelope pick writes. fzf is
// checked after the input and before anything is read from the tree
// (pick-spec.md, Errors).
func Run(in *jsonio.Object, problems []errs.Problem, env Env) ops.Envelope {
	v, e := ops.Validate("pick", in, problems)
	if e != nil {
		return ops.Failed(e)
	}
	_ = v.(ops.PickInput)
	if _, e := findFzf(env.Sys); e != nil {
		return ops.Failed(e)
	}
	s, e := newSession(env.Ops.FS, sessionBase(env.Sys.Environ()))
	if e != nil {
		return ops.Failed(e)
	}
	defer s.Remove()
	return ops.Failed(errs.Internal("pick is not implemented yet"))
}
