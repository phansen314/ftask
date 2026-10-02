package pick

import (
	"github.com/phansen314/ftask/internal/ops"
)

// scopeAction is s (pick-spec.md, Actions): it cycles the readiness scope,
// ready → open → all → ready, and reloads. The prompt names the scope.
var scopeAction = action{key: "s", arity: noTargets, run: cycleScope}

// reloadAction is r: reload.
var reloadAction = action{key: "r", arity: noTargets, run: func(r *actionRun, _ []shownLine) {
	r.reload, r.ifReloaded = true, "✓ reloaded"
}}

// nextScope is the scope s goes to from each.
var nextScope = map[ops.PickScope]ops.PickScope{
	ops.PickReady: ops.PickOpen,
	ops.PickOpen:  ops.PickAll,
	ops.PickAll:   ops.PickReady,
}

func cycleScope(r *actionRun, _ []shownLine) {
	var scope Scope
	if r.err = readJSON(r.s, scopeFile, &scope); r.err != nil {
		return
	}
	scope.Readiness = nextScope[scope.Readiness]
	if r.err = writeJSON(r.s, scopeFile, scope); r.err != nil {
		return
	}
	if r.err = r.s.Write(textPrefix+"prompt", []byte(promptOf(scope))); r.err != nil {
		return
	}
	exe, err := r.env.Sys.Executable()
	if err != nil {
		r.err = errInternalExe(err)
		return
	}
	r.reload, r.status = true, "✓ scope: "+string(scope.Readiness)
	r.also = "transform-prompt(" + helperLine(exe, "text", "prompt") + ")"
}

// promptOf is the prompt for a scope: it names it.
func promptOf(s Scope) string { return string(s.Readiness) + "> " }
