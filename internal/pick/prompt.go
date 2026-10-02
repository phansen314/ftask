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

// applyPrompt is Enter in prompt mode: the action applies value. If it
// succeeded, the prompt closes; if not, it stays open with the value, and
// the status line says why.
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
	leave, e := leavePrompt(s, env, query)
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
		out = "reload-sync(" + helperLine(exe, "lines") + ")"
	} else if r.cursorTo != "" {
		moved, e := armCursor(s, r.cursorTo)
		switch {
		case e != nil:
			return nil, e
		case !moved:
			status += " (not in this list)"
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
	leave, e := leavePrompt(s, env, st.Saved)
	if e != nil {
		return nil, e
	}
	exe, err := env.Sys.Executable()
	if err != nil {
		return nil, errInternalExe(err)
	}
	// The same lines again: the reload's load event hides the input.
	// load is armed before the reload starts, as in applyPrompt.
	return []byte(leave + "+rebind(load)+reload-sync(" + helperLine(exe, "lines") + ")"), nil
}

// leavePrompt closes the prompt for command mode, with query as the search
// query, and returns the actions that do it, but for hiding the input: a
// query change and hide-input in one transform's output lose the query
// change (fzf 0.63.0 to 0.74.4), so the input is hidden on the next load
// event, which the caller brings about with a reload and rebind(load).
func leavePrompt(s *Session, env Env, query string) (string, *errs.Error) {
	var scope Scope
	if e := readJSON(s, scopeFile, &scope); e != nil {
		return "", e
	}
	for _, step := range []func() *errs.Error{
		func() *errs.Error { return s.Delete(promptFile) },
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
		"+rebind(" + strings.Join(commandKeys(), ",") + ")+" + header, nil
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
