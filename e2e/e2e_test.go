// Package e2e tests the built binary: what only a real process shows — exit
// codes, delivery to stdout, signals, and crashes (implementation-spec.md,
// Where each kind of test runs).
package e2e

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/ftask/internal/schematest"
)

// binary is ftask built for the tests, with the e2e_hooks test hooks.
var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ftask-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binary = filepath.Join(dir, "ftask")
	build := exec.Command("go", "build", "-tags", "e2e_hooks", "-o", binary, "../cmd/ftask")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "building ftask:", err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// ftask prepares the binary with args in its own home and config directory.
func ftask(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	home := t.TempDir()
	cmd := exec.Command(binary, args...)
	cmd.Env = []string{"HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "PATH=" + os.Getenv("PATH")}
	return cmd
}

type result struct {
	code           int
	stdout, stderr string
}

func run(t *testing.T, cmd *exec.Cmd) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if cmd.Stdout == nil {
		cmd.Stdout = &stdout
	}
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatal(err)
	}
	return result{code: cmd.ProcessState.ExitCode(), stdout: stdout.String(), stderr: stderr.String()}
}

// envelope checks that r delivered exactly one complete envelope line, as
// every exit 0, 1, or 2 must.
func envelope(t *testing.T, r result) {
	t.Helper()
	if strings.Count(r.stdout, "\n") != 1 || !strings.HasSuffix(r.stdout, "\n") {
		t.Fatalf("exit %d: not one line: %q", r.code, r.stdout)
	}
	if ok, f := schematest.Check(t, "envelope", []byte(r.stdout)); !ok {
		t.Errorf("envelope schema rejects at %s: %s", f, r.stdout)
	}
	if r.stderr != "" {
		t.Errorf("stderr is not empty: %q", r.stderr)
	}
}

func TestExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		stdin string
		code  int
		kind  string
	}{
		{"success", []string{"version"}, "", 0, ""},
		{"stdin input", []string{"version", "-i", "-"}, "{}", 0, ""},
		{"invalid input", []string{"version", "-i", "-"}, `{"x": 1}`, 1, `"kind":"invalid-input"`},
		{"unreadable input", []string{"version", "-i", "/nonexistent/in.json"}, "", 1, `"kind":"io"`},
		{"usage", []string{"nosuch"}, "", 2, `"kind":"usage"`},
		{"bare", nil, "", 2, `"kind":"usage"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := ftask(t, tc.args...)
			cmd.Stdin = strings.NewReader(tc.stdin)
			r := run(t, cmd)
			if r.code != tc.code {
				t.Fatalf("exit %d, want %d: %s%s", r.code, tc.code, r.stdout, r.stderr)
			}
			envelope(t, r)
			if !strings.Contains(r.stdout, tc.kind) {
				t.Errorf("want %s: %s", tc.kind, r.stdout)
			}
		})
	}
}

// stdin is never read unless named: a terminal-less, never-closed stdin
// must not block a command that does not ask for it.
func TestStdinNotRead(t *testing.T) {
	cmd := ftask(t, "version")
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pw.Close()
	cmd.Stdin = pr
	if r := run(t, cmd); r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
}

func TestHelp(t *testing.T) {
	r := run(t, ftask(t, "version", "--help"))
	if r.code != 0 || !strings.Contains(r.stdout, "Usage:") || r.stderr != "" {
		t.Errorf("exit %d: %q %q", r.code, r.stdout, r.stderr)
	}
}

// A closed pipe: EPIPE, not death by SIGPIPE, so exit 3 with the notice.
func TestClosedPipe(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	pr.Close()
	defer pw.Close()
	cmd := ftask(t, "version")
	cmd.Stdout = pw
	r := run(t, cmd)
	if r.code != 3 || !strings.HasPrefix(r.stderr, "ftask: result not delivered: ") {
		t.Errorf("exit %d, stderr %q; want 3 and the notice", r.code, r.stderr)
	}
}

func TestFullDisk(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/dev/full is Linux-only")
	}
	full, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
	if err != nil {
		t.Skip(err)
	}
	defer full.Close()
	cmd := ftask(t, "version")
	cmd.Stdout = full
	r := run(t, cmd)
	if r.code != 3 || !strings.HasPrefix(r.stderr, "ftask: result not delivered: ") {
		t.Errorf("exit %d, stderr %q; want 3 and the notice", r.code, r.stderr)
	}
}

// A panic is a crash: SIGABRT, exit 134, never 2 (a usage error).
func TestCrash(t *testing.T) {
	cmd := ftask(t, "version")
	cmd.Env = append(cmd.Env, "FTASK_E2E_PANIC=1")
	r := run(t, cmd)
	ws, _ := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ws.Signaled() || ws.Signal() != syscall.SIGABRT {
		t.Errorf("exit %d (%v), want death by SIGABRT", r.code, cmd.ProcessState)
	}
	if r.stdout != "" {
		t.Errorf("stdout %q, want nothing", r.stdout)
	}
}
