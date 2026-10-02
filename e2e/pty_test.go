package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
)

// The end-to-end picker tests drive a real fzf in a pseudo-terminal
// (pick-spec.md, Testing): keys go in through the terminal or fzf's
// --listen server, the screen is rendered from what fzf draws, and fzf's
// state is read back through --listen.

// fzfVar lists the fzf binaries to run the picker tests against, separated
// as PATH is, e.g. FTASK_E2E_FZF=/opt/fzf-0.63.0/fzf:/usr/bin/fzf. Unset,
// they run against fzf on PATH, if any.
const fzfVar = "FTASK_E2E_FZF"

// Keys as the terminal sends them.
const (
	keyEnter = "\r"
	keyTab   = "\t"
	keyEsc   = "\x1b"
	keyCtrlC = "\x03"
	keyCtrlD = "\x04"
	keyCtrlU = "\x15"
	keyUp    = "\x1b[A"
	keyDown  = "\x1b[B"
)

// waitTimeout bounds every wait for the picker; fzf usually answers in
// milliseconds.
const waitTimeout = 10 * time.Second

// eachFzf runs f as a subtest for each fzf binary under test, named by its
// version, with a directory holding it as fzf, to put first on PATH. With
// none, the test skips.
func eachFzf(t *testing.T, f func(t *testing.T, fzfDir string)) {
	t.Helper()
	var bins []string
	if v := os.Getenv(fzfVar); v != "" {
		bins = filepath.SplitList(v)
	} else if p, err := exec.LookPath("fzf"); err == nil {
		bins = []string{p}
	} else {
		t.Skip("no fzf installed; set " + fzfVar)
	}
	for _, bin := range bins {
		out, err := exec.Command(bin, "--version").Output()
		if err != nil {
			t.Fatalf("%s --version: %v", bin, err)
		}
		version, _, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
		t.Run("fzf-"+version, func(t *testing.T) {
			bin, err := filepath.Abs(bin)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if err := os.Symlink(bin, filepath.Join(dir, "fzf")); err != nil {
				t.Fatal(err)
			}
			f(t, dir)
		})
	}
}

// term is a process running with a pseudo-terminal as its controlling
// terminal, and the screen it draws there.
type term struct {
	t      *testing.T
	cmd    *exec.Cmd
	ptmx   *os.File
	vt     vt10x.Terminal
	exited chan struct{}
}

// startTerm starts cmd in a session of its own whose controlling terminal
// is a new pseudo-terminal of rows by cols. Its stdin, stdout and stderr
// are the terminal unless cmd already sets them. The process group is
// killed when the test ends.
func startTerm(t *testing.T, cmd *exec.Cmd, rows, cols int) *term {
	t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)}); err != nil {
		t.Fatal(err)
	}
	// The controlling terminal is set from one of the child's descriptors:
	// a standard one that is the terminal, else one passed for the purpose.
	ctty := -1
	if cmd.Stderr == nil {
		cmd.Stderr, ctty = tty, 2
	}
	if cmd.Stdout == nil {
		cmd.Stdout, ctty = tty, 1
	}
	if cmd.Stdin == nil {
		cmd.Stdin, ctty = tty, 0
	}
	if ctty < 0 {
		cmd.ExtraFiles = append(cmd.ExtraFiles, tty)
		ctty = 2 + len(cmd.ExtraFiles)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: ctty}
	if !slices.ContainsFunc(cmd.Env, func(kv string) bool { return strings.HasPrefix(kv, "TERM=") }) {
		cmd.Env = append(cmd.Env, "TERM=xterm-256color")
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	tty.Close()

	tm := &term{t: t, cmd: cmd, ptmx: ptmx, exited: make(chan struct{})}
	// The emulator answers the terminal's queries, e.g. the cursor
	// position, back through the terminal.
	tm.vt = vt10x.New(vt10x.WithSize(cols, rows), vt10x.WithWriter(ptmx))
	read := make(chan struct{})
	go func() {
		defer close(read)
		buf := make([]byte, 32*1024)
		var pending []byte
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				// The emulator takes whole characters only, and leaves
				// a split one for the next read.
				pending = append(pending, buf[:n]...)
				w, _ := tm.vt.Write(pending)
				pending = append(pending[:0], pending[w:]...)
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		cmd.Wait()
		close(tm.exited)
	}()
	t.Cleanup(func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-tm.exited
		ptmx.Close()
		<-read
	})
	return tm
}

// send writes keys to the terminal, as typed.
func (tm *term) send(keys string) {
	tm.t.Helper()
	if _, err := tm.ptmx.WriteString(keys); err != nil {
		tm.t.Fatal(err)
	}
}

// screen is the terminal's text, one line per row, trailing spaces
// trimmed, with no trailing empty rows.
func (tm *term) screen() string {
	// String locks the emulator itself.
	lines := strings.Split(tm.vt.String(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// done reports whether the process has exited.
func (tm *term) done() bool {
	select {
	case <-tm.exited:
		return true
	default:
		return false
	}
}

// waitFor waits until cond holds, failing the test, with the screen, if it
// doesn't within waitTimeout.
func (tm *term) waitFor(what string, cond func() bool) {
	tm.t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			tm.t.Fatalf("timed out waiting for %s; screen:\n%s", what, tm.screen())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitScreen waits until the screen contains s.
func (tm *term) waitScreen(s string) {
	tm.t.Helper()
	tm.waitFor(fmt.Sprintf("%q on screen", s), func() bool { return strings.Contains(tm.screen(), s) })
}

// wait waits for the process to exit and returns its exit code.
func (tm *term) wait() int {
	tm.t.Helper()
	select {
	case <-tm.exited:
	case <-time.After(waitTimeout):
		tm.t.Fatalf("timed out waiting for exit; screen:\n%s", tm.screen())
	}
	return tm.cmd.ProcessState.ExitCode()
}

// picker is ftask pick running in a terminal, with fzf listening for
// requests on a local port.
type picker struct {
	*term
	port           int
	stdout, stderr bytes.Buffer
}

// startPick starts cmd, an ftask pick command, in a 24 by 100 terminal,
// with the fzf in fzfDir. Its stdout and stderr are captured, unless cmd
// sets them, and its stdin is the terminal, unless cmd sets it. startPick
// returns once fzf answers, or pick has exited.
func startPick(t *testing.T, fzfDir string, cmd *exec.Cmd) *picker {
	t.Helper()
	p := &picker{}
	cmd.Env, p.port = pickEnv(t, fzfDir, cmd.Env)
	if cmd.Stdout == nil {
		cmd.Stdout = &p.stdout
	}
	if cmd.Stderr == nil {
		cmd.Stderr = &p.stderr
	}
	p.term = startTerm(t, cmd, 24, 100)
	p.listening()
	return p
}

// pickEnv is env for running pick with the fzf in fzfDir first on PATH, a
// runtime directory of its own unless env sets one, and fzf listening on
// the port returned, after any FTASK_PICK_OPTS in env.
func pickEnv(t *testing.T, fzfDir string, env []string) ([]string, int) {
	t.Helper()
	port := freePort(t)
	opts := "--listen=127.0.0.1:" + fmt.Sprint(port)
	out := make([]string, 0, len(env)+2)
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "PATH="):
			kv = "PATH=" + fzfDir + string(filepath.ListSeparator) + strings.TrimPrefix(kv, "PATH=")
		case strings.HasPrefix(kv, "FTASK_PICK_OPTS="):
			opts = strings.TrimPrefix(kv, "FTASK_PICK_OPTS=") + " " + opts
			continue
		}
		out = append(out, kv)
	}
	if !slices.ContainsFunc(out, func(kv string) bool { return strings.HasPrefix(kv, "XDG_RUNTIME_DIR=") }) {
		out = append(out, "XDG_RUNTIME_DIR="+t.TempDir())
	}
	return append(out, "FTASK_PICK_OPTS="+opts), port
}

// listening waits until fzf answers on its port, or the process has
// exited.
func (p *picker) listening() {
	p.t.Helper()
	p.waitFor("fzf to listen", func() bool {
		if p.done() {
			return true
		}
		_, err := p.get()
		return err == nil
	})
}

// freePort is a local TCP port free a moment ago.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// fzfItem is a line in fzf's state: its index in the list, and its whole
// text, hidden key included.
type fzfItem struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
}

// fzfState is what fzf's GET / returns, in part. Selected includes marks on
// lines the query hides; the prompt, header and footer are only on screen.
type fzfState struct {
	Query      string    `json:"query"`
	TotalCount int       `json:"totalCount"`
	MatchCount int       `json:"matchCount"`
	Reading    bool      `json:"reading"`
	Current    *fzfItem  `json:"current"`
	Matches    []fzfItem `json:"matches"`
	Selected   []fzfItem `json:"selected"`
}

var httpClient = &http.Client{Timeout: 2 * time.Second}

func (p *picker) get() (fzfState, error) {
	var st fzfState
	resp, err := httpClient.Get(fmt.Sprintf("http://127.0.0.1:%d/?limit=1000", p.port))
	if err != nil {
		return st, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return st, fmt.Errorf("GET /: %s", resp.Status)
	}
	return st, json.NewDecoder(resp.Body).Decode(&st)
}

// state is fzf's state now.
func (p *picker) state() fzfState {
	p.t.Helper()
	st, err := p.get()
	if err != nil {
		p.t.Fatalf("fzf state: %v; screen:\n%s", err, p.screen())
	}
	return st
}

// waitState waits until fzf's state satisfies cond, and returns it.
func (p *picker) waitState(what string, cond func(fzfState) bool) fzfState {
	p.t.Helper()
	var st fzfState
	p.waitFor(what, func() bool {
		var err error
		st, err = p.get()
		return err == nil && cond(st)
	})
	return st
}

// loaded waits until fzf has read its whole list and matched it, and
// returns the state.
func (p *picker) loaded() fzfState {
	p.t.Helper()
	return p.waitState("the list to load", func(st fzfState) bool { return !st.Reading && st.Current != nil || st.TotalCount == 0 && !st.Reading })
}

// post sends fzf actions, as a key binding would run them.
func (p *picker) post(actions string) {
	p.t.Helper()
	resp, err := httpClient.Post(fmt.Sprintf("http://127.0.0.1:%d", p.port), "text/plain", strings.NewReader(actions))
	if err != nil {
		p.t.Fatalf("fzf %q: %v", actions, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		p.t.Fatalf("fzf %q: %s", actions, resp.Status)
	}
}

// result waits for pick to exit and returns what it wrote, from the
// buffers startPick gave it.
func (p *picker) result() result {
	p.t.Helper()
	code := p.wait()
	return result{code: code, stdout: p.stdout.String(), stderr: p.stderr.String()}
}

// lineKey is the hidden key of a line in fzf's state.
func lineKey(it *fzfItem) string {
	if it == nil {
		return ""
	}
	k, _, _ := strings.Cut(it.Text, "\t")
	return k
}
