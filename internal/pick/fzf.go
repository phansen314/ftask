package pick

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/phansen314/ftask/internal/errs"
)

// MinFzf is the oldest fzf pick runs with (pick-spec.md, Requirements).
var MinFzf = version{0, 63, 0}

// System is what pick needs from the process besides ops.Env: finding and
// running programs. Tests replace it.
type System struct {
	LookPath func(file string) (string, error)
	// Output runs path with args and env (a list of KEY=value), and returns
	// its stdout, its stderr, and its exit status; err only when it could
	// not be started or did not exit normally.
	Output  func(path string, args, env []string) (stdout, stderr []byte, status int, err error)
	Environ func() []string
}

// OSSystem is the process's own.
func OSSystem() System {
	return System{LookPath: exec.LookPath, Output: output, Environ: os.Environ}
}

func output(path string, args, env []string) ([]byte, []byte, int, error) {
	var out, errOut bytes.Buffer
	cmd := exec.Command(path, args...)
	cmd.Env, cmd.Stdout, cmd.Stderr = env, &out, &errOut
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.Exited() {
		return out.Bytes(), errOut.Bytes(), exit.ExitCode(), nil
	}
	return out.Bytes(), errOut.Bytes(), 0, err
}

// UnavailableReason is why the picker cannot run.
type UnavailableReason string

const (
	NoTerminal UnavailableReason = "no-terminal"
	FzfMissing UnavailableReason = "fzf-missing"
	FzfTooOld  UnavailableReason = "fzf-too-old"
	FzfFailed  UnavailableReason = "fzf-failed"
)

// UnavailableDetails is unavailable's details (pick-error-details).
type UnavailableDetails struct {
	Reason   UnavailableReason `json:"reason"`
	Found    *string           `json:"found,omitempty"`
	Required string            `json:"required,omitempty"`
	Status   *int              `json:"status,omitempty"`
	Actions  []any             `json:"actions,omitempty"`
}

func unavailable(msg string, d UnavailableDetails) *errs.Error {
	return &errs.Error{Kind: errs.KindUnavailable, Message: msg, Details: d}
}

// findFzf returns the path of an fzf on PATH whose version is MinFzf or
// later, or unavailable. The version is asked for without the person's
// FZF_DEFAULT_OPTS and FZF_DEFAULT_OPTS_FILE, since a bad option in either
// makes fzf --version fail: a bad option is reported where fzf really
// starts, as fzf-failed.
func findFzf(sys System) (string, *errs.Error) {
	need := MinFzf.String()
	path, err := sys.LookPath("fzf")
	if err != nil {
		return "", unavailable("fzf not found on PATH: pick needs fzf "+need+" or later", UnavailableDetails{Reason: FzfMissing})
	}
	out, errOut, status, err := sys.Output(path, []string{"--version"}, withoutOpts(sys.Environ()))
	switch {
	case err != nil:
		return "", unavailable(fmt.Sprintf("fzf --version failed: %v", err), UnavailableDetails{Reason: FzfFailed, Actions: []any{}})
	case status != 0:
		msg := fmt.Sprintf("fzf --version exited with status %d", status)
		if line := firstLine(errOut); line != "" {
			msg += ": " + line
		}
		return "", unavailable(msg, UnavailableDetails{Reason: FzfFailed, Status: &status, Actions: []any{}})
	}
	found, v, ok := parseVersion(out)
	d := UnavailableDetails{Reason: FzfTooOld, Found: &found, Required: need}
	switch {
	case !ok:
		return "", unavailable(fmt.Sprintf("fzf --version printed %q, not a version: pick needs fzf %s or later", found, need), d)
	case v.less(MinFzf):
		return "", unavailable(fmt.Sprintf("fzf %s is too old: pick needs fzf %s or later", found, need), d)
	}
	return path, nil
}

// withoutOpts is env without FZF_DEFAULT_OPTS and FZF_DEFAULT_OPTS_FILE.
// It is never nil, which exec would take as the whole of this process's
// environment, options included.
func withoutOpts(env []string) []string {
	out := []string{}
	for _, kv := range env {
		if !strings.HasPrefix(kv, "FZF_DEFAULT_OPTS=") && !strings.HasPrefix(kv, "FZF_DEFAULT_OPTS_FILE=") {
			out = append(out, kv)
		}
	}
	return out
}

type version [3]int

func (v version) String() string { return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]) }

func (v version) less(w version) bool {
	for i := range v {
		if v[i] != w[i] {
			return v[i] < w[i]
		}
	}
	return false
}

// parseVersion reads fzf --version's output: the first whitespace-separated
// word, without any suffix from the first "-" ("0.75.0-dev" is 0.75.0), as
// three numbers. found is that word, or, when it doesn't parse, the first
// line as printed.
func parseVersion(out []byte) (found string, v version, ok bool) {
	words := strings.Fields(string(out))
	if len(words) == 0 {
		return firstLine(out), v, false
	}
	core, _, _ := strings.Cut(words[0], "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return firstLine(out), v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" || p[0] == '+' {
			return firstLine(out), v, false
		}
		v[i] = n
	}
	return words[0], v, true
}

func firstLine(b []byte) string {
	line, _, _ := strings.Cut(string(b), "\n")
	return strings.TrimSuffix(line, "\r")
}
