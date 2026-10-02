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
	pin := v.(ops.PickInput)
	fzf, e := findFzf(env.Sys)
	if e != nil {
		return ops.Failed(e)
	}
	l, failed := load(env.Ops, true)
	if failed != nil {
		return *failed
	}
	scope := scopeOf(pin)
	if e := l.checkFolder(scope.Folder); e != nil {
		return ops.Envelope{Error: e, Warnings: l.Warnings}
	}
	if pin.Folders {
		return ops.Envelope{Error: errs.Internal("the folder picker is not implemented yet"), Warnings: l.Warnings}
	}
	environ := env.Sys.Environ()
	views, missing := l.candidates(scope)
	lines := renderLines(views, !noColor(environ))
	opts, e := userOpts(environ)
	if e != nil {
		return ops.Envelope{Error: e, Warnings: l.Warnings}
	}
	exe, err := env.Sys.Executable()
	if err != nil {
		return ops.Envelope{Error: errs.Internal("locating the ftask binary: " + err.Error()), Warnings: l.Warnings}
	}
	pk := picker{exe: exe, scope: scope, query: pin.Query, missing: missing, warnings: len(l.Warnings), userOpts: opts}
	if pin.SelectOne || pin.ExitZero {
		matched, e := matchAtOnce(env, fzf, pk, lines)
		if e != nil {
			return ops.Envelope{Error: e, Warnings: l.Warnings}
		}
		if keys, done := decideAtOnce(matched, pin.SelectOne, pin.ExitZero); done {
			return emit(env, keys, pin.Fields, []any{})
		}
	}
	s, e := newSession(env.Ops.FS, sessionBase(environ))
	if e != nil {
		return ops.Envelope{Error: e, Warnings: l.Warnings}
	}
	defer s.Remove()
	if e := writeJSON(s, scopeFile, scope); e != nil {
		return ops.Envelope{Error: e, Warnings: l.Warnings}
	}
	if e := writeLoaded(s, l, views, lines, missing); e != nil {
		return ops.Envelope{Error: e, Warnings: l.Warnings}
	}
	status, e := show(env, fzf, pk, lines, s)
	if e != nil {
		return ops.Envelope{Error: e, Warnings: l.Warnings}
	}
	return finish(env, s, status, pin.Fields)
}

// noColor reports whether lines carry no color: NO_COLOR set, and not
// empty (pick-spec.md, Lines).
func noColor(environ []string) bool {
	return lookupEnv(environ, "NO_COLOR") != ""
}
