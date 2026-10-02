package pick

import (
	"strconv"
	"strings"

	"github.com/phansen314/ftask/internal/errs"
)

// Prompt mode (pick-spec.md, Modes): an action asks for a value in the
// query line, in place of the search query, which comes back afterwards.
// Search is off meanwhile, so the list stays put. Enter applies the
// action, Esc cancels; either way, back to command mode. A value the
// operation refuses keeps the prompt open, with the value kept.

// modePrompt is the mode file's content in prompt mode.
const modePrompt = "prompt"

// promptFile holds the open prompt.
const promptFile = "prompt.json"

// promptState is an open prompt: the action asking, its targets, and the
// search query it borrowed the line from.
type promptState struct {
	Action  string      `json:"action"`
	Label   string      `json:"label"`
	Targets []shownLine `json:"targets"`
	Saved   string      `json:"saved"`
}

// openPrompt asks for a value for the running action, labelled e.g.
// "new> ", starting as value. The search query, which command mode keeps
// in the session, is saved to come back afterwards.
func (r *actionRun) openPrompt(key, label, value string, targets []shownLine) {
	saved, _, e := r.s.Read(queryFile)
	if e != nil {
		r.err = e
		return
	}
	st := promptState{Action: key, Label: label, Targets: targets, Saved: string(saved)}
	if targets == nil {
		st.Targets = []shownLine{}
	}
	for _, step := range []func() *errs.Error{
		func() *errs.Error { return writeJSON(r.s, promptFile, st) },
		func() *errs.Error { return r.s.Write(modeFile, []byte(modePrompt)) },
		func() *errs.Error { return r.s.Write(textPrefix+"prompt", []byte(label)) },
		func() *errs.Error { return r.s.Write(textPrefix+"query", []byte(value)) },
	} {
		if r.err = step(); r.err != nil {
			return
		}
	}
	header, e := writeHeader(r.s, r.env)
	if e != nil {
		r.err = e
		return
	}
	exe, err := r.env.Sys.Executable()
	if err != nil {
		r.err = errInternalExe(err)
		return
	}
	// The input is shown first: fzf ignores query edits while it is
	// hidden.
	r.next = "show-input+unbind(" + strings.Join(commandKeys(), ",") + ")+disable-search" +
		"+transform-prompt(" + helperLine(exe, "text", "prompt") + ")" +
		"+" + setQuery(exe, value) + "+" + header
}

// targetLabel is the label of a prompt for name on targets: "priority
// 42> " for one, "priority 3 tasks> " for several.
func targetLabel(name string, targets []shownLine) string {
	if len(targets) == 1 {
		return name + " " + string(idNumber(targets[0].ID)) + "> "
	}
	return name + " " + plural(len(targets), "task") + "> "
}

// promptStart is what a prompt on targets starts with: the one target's
// current value, or nothing for several (pick-spec.md, Actions: Several
// targets).
func promptStart(targets []shownLine, value func(shownLine) string) string {
	if len(targets) == 1 {
		return value(targets[0])
	}
	return ""
}

// applyEach applies a prompt's value to each target, one call each through
// call, in line order. With several targets, an empty value does nothing:
// it never clears them all, which takes an explicit value. It reports
// whether the prompt stays open: when the operation refused the value.
func (r *actionRun) applyEach(value string, targets []shownLine, call func(t shownLine)) bool {
	if value == "" && len(targets) > 1 {
		r.status = "no change"
		return false
	}
	for _, t := range targets {
		if r.err != nil {
			return false
		}
		call(t)
	}
	return r.refused()
}

// refused reports whether an operation the run called refused its input:
// invalid-input, a value to be fixed in the prompt, which stays open with
// it. Other failures close the prompt, reported in the status line.
func (r *actionRun) refused() bool {
	for _, o := range r.outcomes {
		if o.err != nil && o.err.Kind == errs.KindInvalidInput {
			return true
		}
	}
	return false
}

// applyPrompt is Enter in prompt mode: the action applies value. The
// prompt closes, unless the action keeps it open for the value to be fixed;
// either way, the status line says what happened.
func applyPrompt(s *Session, value string, env Env) ([]byte, *errs.Error) {
	var st promptState
	if e := readJSON(s, promptFile, &st); e != nil {
		return nil, e
	}
	a, ok := lookupAction(st.Action)
	if !ok || a.apply == nil {
		return nil, errs.Internal("prompt for an action with no value: " + st.Action)
	}
	r := &actionRun{s: s, env: env}
	keep := a.apply(r, value, st.Targets)
	if r.err != nil {
		return nil, r.err
	}
	status := r.status
	if status == "" {
		status = statusLine(r.outcomes)
	}
	if keep {
		var sh shown
		if e := readJSON(s, shownFile, &sh); e != nil {
			return nil, e
		}
		return setStatus(s, env, sh.Warnings, status)
	}
	query := st.Saved
	if r.clearQuery {
		query = ""
	}
	return backToTasks(s, env, r, query, status)
}

// backToTasks leaves a prompt or choose list for command mode with the
// task list, after the run r applied something: with query as the search
// query, the list reloaded, and status in the status line.
func backToTasks(s *Session, env Env, r *actionRun, query, status string) ([]byte, *errs.Error) {
	leave, e := leaveMode(s, env, query)
	if e != nil {
		return nil, e
	}
	out, warnings, failed := reload(s, env)
	if failed != nil {
		var sh shown
		if e := readJSON(s, shownFile, &sh); e != nil {
			return nil, e
		}
		status = joinStatus(status, "✗ reload: "+errText(failed))
		warnings = sh.Warnings
		// The same lines again: the reload's load event hides the input.
		exe, err := env.Sys.Executable()
		if err != nil {
			return nil, errInternalExe(err)
		}
		out = "clear-selection+reload-sync(" + helperLine(exe, "lines") + ")"
	} else if r.cursorTo != "" {
		moved, e := armCursor(s, r.cursorTo)
		switch {
		case e != nil:
			return nil, e
		case !moved:
			status += " (not in this list)"
		}
	} else if r.returnTo != "" {
		if _, e := armCursor(s, r.returnTo); e != nil {
			return nil, e
		}
	}
	footer, e := setStatus(s, env, warnings, status)
	if e != nil {
		return nil, e
	}
	// load is armed before the reload starts: a quick one can fire it
	// before a rebind later in the chain takes effect (fzf 0.63.0).
	b := leave + "+rebind(load)+" + out + "+" + string(footer)
	if r.also != "" {
		b += "+" + r.also
	}
	return []byte(b), nil
}

// cancelToTasks leaves a prompt or choose list for command mode with the
// task list as it was, and query as the search query.
func cancelToTasks(s *Session, env Env, query string) ([]byte, *errs.Error) {
	leave, e := leaveMode(s, env, query)
	if e != nil {
		return nil, e
	}
	exe, err := env.Sys.Executable()
	if err != nil {
		return nil, errInternalExe(err)
	}
	// The task lines again: the reload's load event hides the input. load
	// is armed before the reload starts, as in backToTasks.
	return []byte(leave + "+rebind(load)+clear-selection+reload-sync(" + helperLine(exe, "lines") + ")"), nil
}

// armCursor records the position of the line with key in the last load,
// for on-load to move the cursor to once fzf shows it (pick-spec.md,
// Actions). It reports false if the line isn't there.
func armCursor(s *Session, key string) (bool, *errs.Error) {
	var sh shown
	if e := readJSON(s, shownFile, &sh); e != nil {
		return false, e
	}
	for i, l := range sh.Lines {
		if l.Key == key {
			return true, s.Write(cursorFile, []byte(strconv.Itoa(i+1)))
		}
	}
	return false, nil
}

// cancelPrompt is Esc in prompt mode: back to command mode, with the
// search query as it was.
func cancelPrompt(s *Session, env Env) ([]byte, *errs.Error) {
	var st promptState
	if e := readJSON(s, promptFile, &st); e != nil {
		return nil, e
	}
	return cancelToTasks(s, env, st.Saved)
}

// leaveMode closes a prompt or choose list for command mode, with query as
// the search query, and returns the actions that do it, but for hiding the
// input: a query change and hide-input in one transform's output lose the
// query change (fzf 0.63.0 to 0.74.4), so the input is hidden on the next
// load event, which the caller brings about with a reload and
// rebind(load). Tab, unbound in a single-choice list, comes back.
func leaveMode(s *Session, env Env, query string) (string, *errs.Error) {
	var scope Scope
	if e := readJSON(s, scopeFile, &scope); e != nil {
		return "", e
	}
	for _, step := range []func() *errs.Error{
		func() *errs.Error { return s.Delete(promptFile) },
		func() *errs.Error { return s.Delete(chooseFile) },
		func() *errs.Error { return s.Write(modeFile, []byte(modeCommand)) },
		func() *errs.Error { return s.Write(queryFile, []byte(query)) },
		func() *errs.Error { return s.Write(textPrefix+"query", []byte(query)) },
		func() *errs.Error { return s.Write(textPrefix+"prompt", []byte(promptOf(scope))) },
		func() *errs.Error { return s.Write(hideFile, nil) },
	} {
		if e := step(); e != nil {
			return "", e
		}
	}
	header, e := writeHeader(s, env)
	if e != nil {
		return "", e
	}
	exe, err := env.Sys.Executable()
	if err != nil {
		return "", errInternalExe(err)
	}
	return "enable-search" +
		"+" + setQuery(exe, query) +
		"+transform-prompt(" + helperLine(exe, "text", "prompt") + ")" +
		"+rebind(" + strings.Join(commandKeys(), ",") + ",tab)+" + header, nil
}

// setQuery is the action that sets the query to the session's text-query,
// which holds value: transform-query, which shows it literally, or, for an
// empty one, change-query(), since fzf leaves the query as it is when
// transform-query's output is empty.
func setQuery(exe, value string) string {
	if value == "" {
		return "change-query()"
	}
	return "transform-query(" + helperLine(exe, "text", "query") + ")"
}

// promptLabel is the open prompt's label, for the header.
func promptLabel(s *Session) (string, *errs.Error) {
	var st promptState
	if e := readJSON(s, promptFile, &st); e != nil {
		return "", e
	}
	return st.Label, nil
}
