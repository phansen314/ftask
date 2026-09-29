package e2e

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/ftask/internal/fsys"
)

// clock fixes the time every crash-test command sees, so the files of two
// runs compare byte for byte.
const clock = "FTASK_E2E_CLOCK=2026-09-28T12:00:00Z"

// snapshot is a home directory's contents: each path relative to the home
// maps to the file's bytes, with the home itself written "~", or to "/" for
// a directory. Temp files are left out and listed in temps.
type snapshot struct {
	files map[string]string
	temps []string
}

func snap(t *testing.T, home string) snapshot {
	t.Helper()
	s := snapshot{files: map[string]string{}}
	err := filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == home {
			return err
		}
		rel, _ := filepath.Rel(home, p)
		switch {
		case strings.HasPrefix(d.Name(), fsys.TempPrefix):
			s.temps = append(s.temps, rel)
			if d.IsDir() {
				return filepath.SkipDir // delete-folder's folder, renamed aside
			}
		case d.IsDir():
			s.files[rel] = "/"
		default:
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			s.files[rel] = strings.ReplaceAll(string(b), home, "~")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// crashCase is one write, and what its Crash behavior and Retry safety
// promise (operations.md).
type crashCase struct {
	name string
	// init: run from a fresh home with no tree; else from a new tree after
	// setup.
	init  bool
	setup [][]string
	args  []string
	// order is what the write changes, relative to the home, in the order
	// its Crash behavior says the changes land. Paths in one group land in
	// one call.
	order [][]string
	// rerun is what running the write again gives after a crash that left
	// the tree at stage (0: nothing changed; len(order): everything): the
	// exit code, a fragment of the envelope, and whether the tree must then
	// be as after an uninterrupted run.
	rerun func(stage int) (code int, want string, same bool)
}

func safe(want string) func(int) (int, string, bool) {
	return func(int) (int, string, bool) { return 0, want, true }
}

var crashCases = []crashCase{
	{
		name: "init", init: true,
		args:  []string{"init", "~/tasks"},
		order: [][]string{{"tasks"}, {"tasks/ftask.json"}, {".config", ".config/ftask"}, {".config/ftask/config.toml"}},
		// Once the config is written, init has succeeded, and a rerun is a
		// rerun after success.
		rerun: func(stage int) (int, string, bool) {
			if stage == 4 {
				return 1, `"rule":"config-exists"`, true
			}
			return 0, `"ok":true`, true
		},
	},
	{
		name:  "create",
		args:  []string{"create", "Fix", "--notes", "call first"},
		order: [][]string{{"tasks/ftask.json"}, {"tasks/1.json"}, {"tasks/1.md"}},
		// Not safe after a crash: once the ID is consumed, a rerun creates
		// a second task.
		rerun: func(stage int) (int, string, bool) {
			if stage == 0 {
				return 0, `"id":1,`, true
			}
			return 0, `"id":2,`, false
		},
	},
	{
		name:  "create-folder",
		args:  []string{"create-folder", "-p", "/proj/travel"},
		order: [][]string{{"tasks/proj"}, {"tasks/proj/travel"}},
		rerun: safe(`"folder":"/proj/travel"`),
	},
	{
		name:  "complete",
		setup: [][]string{{"create", "a"}},
		args:  []string{"complete", "1"},
		order: [][]string{{"tasks/1.json"}},
		rerun: safe(`"completed_at":"2026-09-28T12:00:00Z"`),
	},
	{
		name:  "reopen",
		setup: [][]string{{"create", "a"}, {"complete", "1"}},
		args:  []string{"reopen", "1"},
		order: [][]string{{"tasks/1.json"}},
		rerun: safe(`"completed_at":null`),
	},
	{
		name:  "update",
		setup: [][]string{{"create", "a"}},
		args:  []string{"update", "1", "--title", "b", "--tags-add", "x"},
		order: [][]string{{"tasks/1.json"}},
		rerun: safe(`"title":"b"`),
	},
	{
		name:  "block",
		setup: [][]string{{"create", "a"}, {"create", "b"}},
		args:  []string{"block", "1", "--blockers", "2"},
		order: [][]string{{"tasks/1.json"}},
		rerun: safe(`"blocked_by":[2]`),
	},
	{
		name:  "unblock",
		setup: [][]string{{"create", "a"}, {"create", "b"}, {"block", "1", "--blockers", "2"}},
		args:  []string{"unblock", "1", "--blockers", "2"},
		order: [][]string{{"tasks/1.json"}},
		rerun: safe(`"blocked_by":[]`),
	},
	{
		name:  "delete",
		setup: [][]string{{"create", "a", "--notes", "n"}, {"create", "b", "--blocked-by", "1"}},
		args:  []string{"delete", "1"},
		order: [][]string{{"tasks/2.json"}, {"tasks/1.json"}, {"tasks/1.md"}},
		rerun: func(stage int) (int, string, bool) {
			if stage >= 2 {
				// The task is gone; an orphaned .md may be left for doctor.
				return 1, `"kind":"not-found"`, false
			}
			return 0, `"id":1,"folder":"/"`, true
		},
	},
	{
		name:  "delete-folder",
		setup: [][]string{{"create-folder", "-p", "/p/a"}, {"create", "a", "--folder", "/p/a"}, {"create", "b", "--blocked-by", "1"}},
		args:  []string{"delete-folder", "-r", "/p"},
		order: [][]string{{"tasks/2.json"}, {"tasks/p", "tasks/p/a", "tasks/p/a/1.json", "tasks/p/a/1.md"}},
		rerun: func(stage int) (int, string, bool) {
			if stage == 2 {
				// Renamed aside: gone from the tree, a hidden leftover for doctor.
				return 1, `"kind":"not-found"`, true
			}
			return 0, `"ids":[1]`, true
		},
	},
	{
		name:  "move",
		setup: [][]string{{"create", "a", "--notes", "n"}},
		args:  []string{"move", "1", "--to", "/p", "-p"},
		order: [][]string{{"tasks/p"}, {"tasks/p/1.md"}, {"tasks/1.json", "tasks/p/1.json"}, {"tasks/1.md"}},
		rerun: func(stage int) (int, string, bool) {
			if stage == 3 {
				// Moved; the old .md is left for doctor.
				return 0, `"changed":false`, false
			}
			return 0, `"changed":true`, true
		},
	},
	{
		name:  "move-folder",
		setup: [][]string{{"create-folder", "/a"}, {"create", "a", "--folder", "/a"}},
		args:  []string{"move-folder", "/a", "--to", "/x/y", "-p"},
		order: [][]string{{"tasks/x"}, {"tasks/a", "tasks/a/1.json", "tasks/a/1.md", "tasks/x/y", "tasks/x/y/1.json", "tasks/x/y/1.md"}},
		rerun: safe(`"folder":"/x/y"`),
	},
}

// fixture is the tree c runs against, in a home of its own.
func (c crashCase) fixture(t *testing.T) *tree {
	t.Helper()
	var tr *tree
	if c.init {
		cmd := ftask(t)
		tr = &tree{t: t, env: cmd.Env, home: envHome(cmd)}
	} else {
		tr = newTree(t)
	}
	tr.env = append(tr.env, clock)
	for _, args := range c.setup {
		r := run(t, tr.cmd(args...))
		if r.code != 0 {
			t.Fatalf("setup %q: exit %d: %s", args, r.code, r.stdout)
		}
	}
	return tr
}

// stages are the trees a crash may leave: before, then each group of order
// in turn changed as in after.
func (c crashCase) stages(before, after snapshot) []map[string]string {
	cur := maps.Clone(before.files)
	out := []map[string]string{maps.Clone(cur)}
	for _, group := range c.order {
		for _, p := range group {
			if v, ok := after.files[p]; ok {
				cur[p] = v
			} else {
				delete(cur, p)
			}
		}
		out = append(out, maps.Clone(cur))
	}
	return out
}

// Every write, killed with SIGKILL before each call that changes the disk in
// turn, leaves what its Crash behavior says: a tree at one of its stages,
// never going backwards, with only temp files besides; the tree still reads
// cleanly, nothing is wedged, and a rerun gives what its Retry safety
// promises (implementation-spec.md, Crash injection).
func TestCrashInjection(t *testing.T) {
	for _, c := range crashCases {
		t.Run(c.name, func(t *testing.T) {
			ref := c.fixture(t)
			before := snap(t, ref.home)
			steps(t, []step{{ref.cmd(c.args...), 0, `"ok":true`}})
			after := snap(t, ref.home)
			stages := c.stages(before, after)
			if !maps.Equal(stages[len(stages)-1], after.files) {
				t.Fatalf("order does not cover every change:\nbefore %v\nafter  %v", before.files, after.files)
			}

			seen := make([]bool, len(stages))
			last := 0
			for k := 1; ; k++ {
				if k > 50 {
					t.Fatal("still crashing at k=50")
				}
				tr := c.fixture(t)
				cmd := tr.cmd(c.args...)
				cmd.Env = append(cmd.Env, "FTASK_E2E_CRASH_BEFORE="+strconv.Itoa(k))
				r := run(t, cmd)
				ws, _ := cmd.ProcessState.Sys().(syscall.WaitStatus)
				if !ws.Signaled() {
					// Fewer than k calls change the disk: the uninterrupted run.
					envelope(t, r)
					if s := snap(t, tr.home); r.code != 0 || !maps.Equal(s.files, after.files) || len(s.temps) != 0 {
						t.Fatalf("k=%d, no crash: exit %d, tree %v temps %v: %s", k, r.code, s.files, s.temps, r.stdout)
					}
					// A write whose last call publishes by rename reaches its
					// last stage only here: no call follows to crash before.
					seen[len(stages)-1] = true
					break
				}
				if ws.Signal() != syscall.SIGKILL || r.stdout != "" {
					t.Fatalf("k=%d: %v, stdout %q; want SIGKILL and nothing", k, cmd.ProcessState, r.stdout)
				}
				crashed := snap(t, tr.home)
				stage := slices.IndexFunc(stages, func(m map[string]string) bool { return maps.Equal(m, crashed.files) })
				if stage < 0 {
					t.Fatalf("k=%d: tree at no stage of %q: %v", k, c.order, crashed.files)
				}
				if stage < last {
					t.Fatalf("k=%d: stage %d after stage %d", k, stage, last)
				}
				last, seen[stage] = stage, true
				t.Logf("k=%d: stage %d, temps %v", k, stage, crashed.temps)

				if _, err := os.Stat(filepath.Join(tr.home, ".config/ftask/config.toml")); err == nil {
					steps(t, []step{{tr.cmd("list", "--include-complete", "--include-folders"), 0, `"warnings":[]`}})
				}
				code, want, same := c.rerun(stage)
				steps(t, []step{{tr.cmd(c.args...), code, want}})
				s := snap(t, tr.home)
				if same && !maps.Equal(s.files, after.files) {
					t.Errorf("k=%d: rerun left %v, want %v", k, s.files, after.files)
				}
				// doctor never sees the config directory: init clears it.
				for _, tmp := range s.temps {
					if strings.HasPrefix(tmp, ".config/") {
						t.Errorf("k=%d: rerun left %s", k, tmp)
					}
				}
			}
			for i, ok := range seen {
				if !ok {
					t.Errorf("no crash left stage %d (%s)", i, fmt.Sprint(stages[i]))
				}
			}
		})
	}
}
