package pick

import (
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
var verbs = map[string]verb{}

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
