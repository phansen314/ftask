// Package e2e tests the built binary: what only a real process shows — exit
// codes, delivery to stdout, signals, and crashes (implementation-spec.md,
// Where each kind of test runs).
package e2e

import (
	"bytes"
	"encoding/json"
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

// info locates the config from the real environment, and reports a machine
// with nothing set up, or no usable HOME, as state: exit 0.
func TestInfo(t *testing.T) {
	for _, tc := range []struct {
		name  string
		unset bool   // no HOME and no XDG_CONFIG_HOME
		path  string // want config.path, relative to XDG_CONFIG_HOME
	}{
		{"fresh home", false, "ftask/config.toml"},
		{"no home", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := ftask(t, "info")
			var want any // config.path: null when it can't be located
			for _, kv := range cmd.Env {
				if x, ok := strings.CutPrefix(kv, "XDG_CONFIG_HOME="); ok && !tc.unset {
					want = filepath.Join(x, tc.path)
				}
			}
			if tc.unset {
				cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
			}
			r := run(t, cmd)
			if r.code != 0 {
				t.Fatalf("exit %d: %s%s", r.code, r.stdout, r.stderr)
			}
			envelope(t, r)
			var env struct {
				Result struct {
					Config      map[string]any `json:"config"`
					Initialized bool           `json:"initialized"`
				} `json:"result"`
			}
			if err := json.Unmarshal([]byte(r.stdout), &env); err != nil {
				t.Fatal(err)
			}
			if c := env.Result.Config; c["path"] != want || c["state"] != "missing" || env.Result.Initialized {
				t.Errorf("got %s; want config.path %v, state missing, not initialized", r.stdout, want)
			}
		})
	}
}

// show against the real filesystem: not-initialized in a fresh home (exit
// 1), and a task found in a tree written by hand (init comes later).
func TestShow(t *testing.T) {
	cmd := ftask(t, "show", "1")
	r := run(t, cmd)
	envelope(t, r)
	if r.code != 1 || !strings.Contains(r.stdout, `"missing":"config"`) {
		t.Fatalf("fresh home: exit %d: %s", r.code, r.stdout)
	}

	cmd = ftask(t, "show", "1")
	var xdg string
	for _, kv := range cmd.Env {
		if x, ok := strings.CutPrefix(kv, "XDG_CONFIG_HOME="); ok {
			xdg = x
		}
	}
	root := filepath.Join(filepath.Dir(xdg), "tasks")
	for p, content := range map[string]string{
		filepath.Join(xdg, "ftask", "config.toml"): `root = "` + root + "\"\n",
		filepath.Join(root, "ftask.json"):          `{"schema": 1, "last_id": 1}`,
		filepath.Join(root, "proj", "1.json"):      `{"schema": 1, "id": 1, "title": "t", "priority": null, "created_at": "2026-09-27T00:00:00Z", "completed_at": null, "blocked_by": [2], "tags": [], "extra": {}}`,
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r = run(t, cmd)
	envelope(t, r)
	for _, want := range []string{`"folder":"/proj"`, `"readiness":"blocked"`, `"kind":"dangling-reference"`} {
		if r.code != 0 || !strings.Contains(r.stdout, want) {
			t.Errorf("exit %d, want %s: %s", r.code, want, r.stdout)
		}
	}
}

// envHome is the home directory ftask gives cmd.
func envHome(cmd *exec.Cmd) string {
	for _, kv := range cmd.Env {
		if h, ok := strings.CutPrefix(kv, "HOME="); ok {
			return h
		}
	}
	return ""
}

// init creates a tree the other commands then use; a second init refuses.
// ~/ is expanded by ftask itself, as with no shell in between.
func TestInit(t *testing.T) {
	first := ftask(t, "init", "~/tasks")
	home := envHome(first)
	same := func(args ...string) *exec.Cmd {
		cmd := exec.Command(binary, args...)
		cmd.Env = first.Env
		return cmd
	}
	for _, step := range []struct {
		cmd  *exec.Cmd
		code int
		want string
	}{
		{first, 0, `"result":{"root":"` + home + `/tasks","action":"created","last_id":0}`},
		{same("info"), 0, `"usable":true`},
		{same("show", "1"), 1, `"kind":"not-found"`},
		{same("init", home+"/other"), 1, `"rule":"config-exists"`},
	} {
		r := run(t, step.cmd)
		envelope(t, r)
		if r.code != step.code || !strings.Contains(r.stdout, step.want) {
			t.Fatalf("%q: exit %d, want %d and %s: %s", step.cmd.Args[1:], r.code, step.code, step.want, r.stdout)
		}
	}
}

// create writes tasks that show then reads, with notes piped in on stdin.
func TestCreate(t *testing.T) {
	first := ftask(t, "init", "~/tasks")
	home := envHome(first)
	same := func(stdin string, args ...string) *exec.Cmd {
		cmd := exec.Command(binary, args...)
		cmd.Env = first.Env
		cmd.Stdin = strings.NewReader(stdin)
		return cmd
	}
	for _, step := range []struct {
		cmd  *exec.Cmd
		code int
		want string
	}{
		{first, 0, `"action":"created"`},
		{same("", "create", "Deploy"), 0, `"id":1,"title":"Deploy"`},
		{same("call first\n", "create", "Fix login bug", "--blocked-by", "1", "--notes-file", "-"), 0, `"id":2,`},
		{same("", "show", "2"), 0, `"blocked_by":[1],"tags":[],"extra":{},"folder":"/","notes_path":"` + home + `/tasks/2.md","readiness":"blocked","blocking":[1]`},
		{same("", "create", "x", "--folder", "/nope", "--blocked-by", "9"), 1, `"details":{"folders":["/nope"],"ids":[9],"paths":[]}`},
		{same("", "create", "x", "--notes", "a", "--notes-file", "-"), 2, `"kind":"usage"`},
		{same("", "info"), 0, `"last_id":2`},
	} {
		r := run(t, step.cmd)
		envelope(t, r)
		if r.code != step.code || !strings.Contains(r.stdout, step.want) {
			t.Fatalf("%q: exit %d, want %d and %s: %s", step.cmd.Args[1:], r.code, step.code, step.want, r.stdout)
		}
	}
	if b, err := os.ReadFile(filepath.Join(home, "tasks", "2.md")); err != nil || string(b) != "call first\n" {
		t.Errorf("2.md: %q %v", b, err)
	}
}

// create-folder makes the folders create then files tasks in.
func TestCreateFolder(t *testing.T) {
	first := ftask(t, "init", "~/tasks")
	same := func(args ...string) *exec.Cmd {
		cmd := exec.Command(binary, args...)
		cmd.Env = first.Env
		return cmd
	}
	for _, step := range []struct {
		cmd  *exec.Cmd
		code int
		want string
	}{
		{first, 0, `"action":"created"`},
		{same("create-folder", "/proj/travel"), 1, `"folders":["/proj"]`},
		{same("create-folder", "-p", "/proj/travel"), 0, `"result":{"folder":"/proj/travel","created":["/proj","/proj/travel"]}`},
		{same("create-folder", "-p", "/proj/travel"), 0, `"created":[]`},
		{same("create", "Book flights", "--folder", "/proj/travel"), 0, `"id":1,`},
		{same("show", "1"), 0, `"folder":"/proj/travel"`},
	} {
		r := run(t, step.cmd)
		envelope(t, r)
		if r.code != step.code || !strings.Contains(r.stdout, step.want) {
			t.Fatalf("%q: exit %d, want %d and %s: %s", step.cmd.Args[1:], r.code, step.code, step.want, r.stdout)
		}
	}
}

// complete makes a dependent ready, and reopen blocks it again; doing
// either twice changes nothing.
func TestCompleteReopen(t *testing.T) {
	first := ftask(t, "init", "~/tasks")
	same := func(args ...string) *exec.Cmd {
		cmd := exec.Command(binary, args...)
		cmd.Env = first.Env
		return cmd
	}
	for _, step := range []struct {
		cmd  *exec.Cmd
		code int
		want string
	}{
		{first, 0, `"action":"created"`},
		{same("create", "Book flights"), 0, `"id":1,`},
		{same("create", "Pack bags", "--blocked-by", "1"), 0, `"id":2,`},
		{same("show", "2"), 0, `"readiness":"blocked","blocking":[1]`},
		{same("complete", "1"), 0, `"changed":true`},
		{same("show", "2"), 0, `"readiness":"ready","blocking":[]`},
		{same("complete", "1"), 0, `"changed":false`},
		{same("complete", "9"), 1, `"ids":[9]`},
		{same("reopen", "1"), 0, `"completed_at":null,`},
		{same("show", "2"), 0, `"readiness":"blocked","blocking":[1]`},
		{same("reopen", "1"), 0, `"changed":false`},
	} {
		r := run(t, step.cmd)
		envelope(t, r)
		if r.code != step.code || !strings.Contains(r.stdout, step.want) {
			t.Fatalf("%q: exit %d, want %d and %s: %s", step.cmd.Args[1:], r.code, step.code, step.want, r.stdout)
		}
	}
}

// update changes only what it names; the same update again changes nothing.
func TestUpdate(t *testing.T) {
	first := ftask(t, "init", "~/tasks")
	same := func(args ...string) *exec.Cmd {
		cmd := exec.Command(binary, args...)
		cmd.Env = first.Env
		return cmd
	}
	for _, step := range []struct {
		cmd  *exec.Cmd
		code int
		want string
	}{
		{first, 0, `"action":"created"`},
		{same("create", "Book flights", "--tags", "travel", "--extra", `{"status":"new"}`), 0, `"id":1,`},
		{same("update", "1", "--priority", "2", "--tags-add", "urgent", "--extra-merge", `{"status":"waiting"}`), 0,
			`"priority":2,"created_at":`},
		{same("show", "1"), 0, `"tags":["travel","urgent"],"extra":{"status":"waiting"}`},
		{same("update", "1", "--priority", "2", "--tags-add", "urgent", "--extra-merge", `{"status":"waiting"}`), 0, `"changed":[]`},
		{same("update", "1"), 1, `"kind":"invalid-input"`},
		{same("update", "1", "--tags-add", "x", "--tags-replace-all", "y"), 1, `"field":"/tags/add"`},
		{same("update", "9", "--title", "x"), 1, `"ids":[9]`},
	} {
		r := run(t, step.cmd)
		envelope(t, r)
		if r.code != step.code || !strings.Contains(r.stdout, step.want) {
			t.Fatalf("%q: exit %d, want %d and %s: %s", step.cmd.Args[1:], r.code, step.code, step.want, r.stdout)
		}
	}
}

// block adds blockers that show then sees, and refuses a cycle; unblock
// removes them again.
func TestBlockUnblock(t *testing.T) {
	first := ftask(t, "init", "~/tasks")
	same := func(args ...string) *exec.Cmd {
		cmd := exec.Command(binary, args...)
		cmd.Env = first.Env
		return cmd
	}
	for _, step := range []struct {
		cmd  *exec.Cmd
		code int
		want string
	}{
		{first, 0, `"action":"created"`},
		{same("create", "a"), 0, `"id":1,`},
		{same("create", "b"), 0, `"id":2,`},
		{same("create", "c"), 0, `"id":3,`},
		{same("block", "1", "--blockers", "2"), 0, `"blocked_by":[2],`},
		{same("block", "2", "--blockers", "3"), 0, `"added":[3]`},
		{same("show", "1"), 0, `"readiness":"blocked","blocking":[2]`},
		{same("block", "3", "--blockers", "1,2"), 1, `"details":{"rule":"acyclic","ids":[1,2],"cycles":[[3,1,2],[3,2]]}`},
		{same("block", "1", "--blockers", "2"), 0, `"added":[]`},
		{same("block", "1", "--blockers", "1"), 1, `"kind":"invalid-input"`},
		{same("block", "1"), 2, `"kind":"usage"`},
		{same("unblock", "1", "--blockers", "2,9"), 0, `"blocked_by":[],`},
		{same("show", "1"), 0, `"readiness":"ready","blocking":[]`},
		{same("unblock", "1", "--blockers", "2"), 0, `"removed":[]`},
	} {
		r := run(t, step.cmd)
		envelope(t, r)
		if r.code != step.code || !strings.Contains(r.stdout, step.want) {
			t.Fatalf("%q: exit %d, want %d and %s: %s", step.cmd.Args[1:], r.code, step.code, step.want, r.stdout)
		}
	}
}

// list returns tasks across folders in tree order, with their readiness.
func TestList(t *testing.T) {
	first := ftask(t, "init", "~/tasks")
	same := func(args ...string) *exec.Cmd {
		cmd := exec.Command(binary, args...)
		cmd.Env = first.Env
		return cmd
	}
	for _, step := range []struct {
		cmd  *exec.Cmd
		code int
		want string
	}{
		{first, 0, `"action":"created"`},
		{same("list"), 0, `"result":{"tasks":[]}`},
		{same("create-folder", "-p", "/proj/travel"), 0, `"created"`},
		{same("create", "b", "--folder", "/proj/travel"), 0, `"id":1,`},
		{same("create", "a", "--blocked-by", "1"), 0, `"id":2,`},
		{same("complete", "1"), 0, `"changed":true`},
		{same("list"), 0, `"tasks":[{"schema":1,"id":2,`},
		{same("list", "--include-complete", "--include-folders"), 0, `"folders":["/","/proj","/proj/travel"],"tasks":[{"schema":1,"id":2,`},
		{same("list", "--folder", "/nope"), 1, `"folders":["/nope"]`},
	} {
		r := run(t, step.cmd)
		envelope(t, r)
		if r.code != step.code || !strings.Contains(r.stdout, step.want) {
			t.Fatalf("%q: exit %d, want %d and %s: %s", step.cmd.Args[1:], r.code, step.code, step.want, r.stdout)
		}
	}
}

// A relative root is resolved against the working directory as the shell
// reports it: through a symlink, not with it resolved.
func TestInitRelative(t *testing.T) {
	cmd := ftask(t, "init", "tasks")
	home := envHome(cmd)
	if err := os.Mkdir(filepath.Join(home, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "link")
	if err := os.Symlink(filepath.Join(home, "real"), link); err != nil {
		t.Fatal(err)
	}
	cmd.Dir = link
	cmd.Env = append(cmd.Env, "PWD="+link)
	r := run(t, cmd)
	envelope(t, r)
	if r.code != 0 || !strings.Contains(r.stdout, `"root":"`+link+`/tasks"`) {
		t.Fatalf("exit %d: %s", r.code, r.stdout)
	}
	if _, err := os.Stat(filepath.Join(home, "real", "tasks", "ftask.json")); err != nil {
		t.Error(err)
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
