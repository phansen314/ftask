package pick

import (
	"slices"
	"strconv"

	"github.com/phansen314/ftask/internal/errs"
)

// HelperCommand is the hidden command fzf's callbacks run: ftask __pick
// <verb> …. It is internal: not in help, not part of the contract, and it
// may change in any release (pick-spec.md, Session).
const HelperCommand = "__pick"

// verb is one of the helper's verbs. It runs in s with args, the words
// after the verb, and returns what to print: fzf actions, or text that fzf
// shows as it is.
type verb func(s *Session, args []string, env Env) ([]byte, *errs.Error)

// verbs are the helper's verbs, by name.
var verbs = map[string]verb{
	"on-load": onLoad,
	"preview": previewVerb,
	"text":    text,
}

// Helper runs ftask __pick with args, the words after it, and returns what
// to print. Its failures are envelopes, like any command's; one run with no
// valid session, or with no verb it knows, is usage.
func Helper(args []string, env Env) ([]byte, *errs.Error) {
	s, e := openSession(env.Ops.FS, env.Sys.Environ())
	if e != nil {
		return nil, e
	}
	defer s.Close()
	if len(args) == 0 {
		return nil, errs.Usage([]errs.UsageProblem{{Reason: "missing verb"}})
	}
	v, ok := verbs[args[0]]
	if !ok {
		return nil, errs.Usage([]errs.UsageProblem{{Argument: &args[0], Reason: "unknown verb"}})
	}
	return v(s, args[1:], env)
}

// Session files the verbs share.
const (
	// cursorFile holds the line a callback wants the cursor on once its
	// reload is in: on-load's arming (pick-spec.md, Actions).
	cursorFile = "cursor"
	// textPrefix begins the files that hold text fzf shows, by name:
	// text-footer, text-header, text-prompt, text-query.
	textPrefix = "text-"
)

// textNames are the texts the text verb shows.
var textNames = []string{"footer", "header", "prompt", "query"}

// onLoad runs on fzf's load event, when it is bound: it moves the cursor to
// the line recorded, if any, and unbinds load again. fzf may run it once,
// unarmed, for the first list, before start's unbind takes effect.
func onLoad(s *Session, args []string, _ Env) ([]byte, *errs.Error) {
	if len(args) != 0 {
		return nil, errs.Usage([]errs.UsageProblem{{Argument: &args[0], Reason: "unexpected argument"}})
	}
	b, ok, e := s.Read(cursorFile)
	switch {
	case e != nil:
		return nil, e
	case !ok:
		return []byte("unbind(load)"), nil
	}
	if e := s.Delete(cursorFile); e != nil {
		return nil, e
	}
	n, err := strconv.Atoi(string(b))
	if err != nil || n < 1 {
		return nil, errs.Internal("session cursor file holds " + strconv.Quote(string(b)))
	}
	return []byte("pos(" + strconv.Itoa(n) + ")+unbind(load)"), nil
}

// text prints one of the session's texts, which fzf shows as it is
// (pick-spec.md, No data in action text): one line, its control characters
// escaped as on the CLI's stderr line. A text never written is empty.
func text(s *Session, args []string, _ Env) ([]byte, *errs.Error) {
	switch {
	case len(args) == 0:
		return nil, errs.Usage([]errs.UsageProblem{{Reason: "missing text name"}})
	case !slices.Contains(textNames, args[0]):
		return nil, errs.Usage([]errs.UsageProblem{{Argument: &args[0], Reason: "unknown text"}})
	case len(args) > 1:
		return nil, errs.Usage([]errs.UsageProblem{{Argument: &args[1], Reason: "unexpected argument"}})
	}
	b, _, e := s.Read(textPrefix + args[0])
	if e != nil {
		return nil, e
	}
	return []byte(errs.OneLine(string(b))), nil
}
