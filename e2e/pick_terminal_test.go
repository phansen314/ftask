package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The picker end to end, in a terminal, against each fzf under test
// (pick-spec.md, Testing). Each test drives pick's fzf through the
// terminal's keys and reads pick's envelope.

// pickOut is pick's envelope, in the parts these tests check.
type pickOut struct {
	OK     bool `json:"ok"`
	Result struct {
		Tasks []struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"tasks"`
		Missing     []int64 `json:"missing"`
		Actions     []any   `json:"actions"`
		NotesEdited []int64 `json:"notes_edited"`
	} `json:"result"`
	Error *struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
		Details struct {
			Actions []any `json:"actions"`
			Error   *struct {
				Kind string `json:"kind"`
			} `json:"error"`
		} `json:"details"`
	} `json:"error"`
}

// decode checks r is one envelope and decodes it.
func decode(t *testing.T, r result) pickOut {
	t.Helper()
	envelope(t, r)
	var out pickOut
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// picked checks r is a successful pick with no actions, and returns the
// selected IDs, in order.
func picked(t *testing.T, r result) []int64 {
	t.Helper()
	out := decode(t, r)
	res := out.Result
	if r.code != 0 || !out.OK || res.Actions == nil || len(res.Actions) != 0 || res.Missing == nil || res.NotesEdited == nil {
		t.Fatalf("exit %d: %s", r.code, r.stdout)
	}
	ids := []int64{}
	for _, task := range res.Tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

// pickTree is a tree with tasks 1 to 3 in /trips, for the picker. In the
// picker's line order they are 1, 2, 3, so 1 is the first line, at the
// bottom in fzf's default layout, and up moves to the next.
func pickTree(t *testing.T) *tree {
	t.Helper()
	tr := newTree(t)
	for _, c := range [][]string{
		{"create-folder", "/trips"},
		{"create", "Book flights", "--folder", "/trips"},
		{"create", "Renew passport", "--folder", "/trips"},
		{"create", "Pack bags", "--folder", "/trips"},
	} {
		if r := run(t, tr.cmd(c...)); r.code != 0 {
			t.Fatal(r.stdout)
		}
	}
	return tr
}

// setQuery types q and waits for fzf to match it, to n lines, and put the
// cursor on the first of them.
func (p *picker) setQuery(q string, n int) fzfState {
	p.t.Helper()
	p.send(q)
	return p.waitState(fmt.Sprintf("query %q matching %d", q, n), func(st fzfState) bool {
		return st.Query == q && st.MatchCount == n && (n == 0) == (st.Current == nil) && (n == 0 || st.Current.Index == st.Matches[0].Index)
	})
}

// command enters command mode with Esc, and waits for its header.
func (p *picker) command() {
	p.t.Helper()
	p.send(keyEsc)
	p.waitScreen("[cmd]")
}

// quit quits from insert mode: Esc, then Esc again in command mode.
func (p *picker) quit() {
	p.t.Helper()
	p.command()
	p.send(keyEsc)
}

// Enter, quit and cancel, and the edges of each: Enter or quit with no line
// to pick gives an empty selection, not an error. FZF_DEFAULT_OPTS that
// would end fzf without a callback changes none of it.
func TestPickOutcomes(t *testing.T) {
	type outcome struct {
		name  string
		empty bool // on a tree with no tasks
		keys  func(p *picker)
		want  []int64 // nil: cancelled
	}
	outcomes := []outcome{
		{"enter", false, func(p *picker) { p.send(keyEnter) }, []int64{1}},
		{"enter after moving", false, func(p *picker) {
			p.send(keyUp)
			p.waitState("the cursor on 2", func(st fzfState) bool { return lineKey(st.Current) == "2@/trips" })
			p.send(keyEnter)
		}, []int64{2}},
		{"enter on the query's match", false, func(p *picker) {
			p.setQuery("pass", 1)
			p.send(keyEnter)
		}, []int64{2}},
		// Marked 3 then 1, emitted in line order.
		{"enter with marks", false, func(p *picker) {
			p.send(keyUp + keyUp)
			p.waitState("the cursor on 3", func(st fzfState) bool { return lineKey(st.Current) == "3@/trips" })
			p.send(keyTab)
			p.waitState("3 marked", func(st fzfState) bool { return len(st.Selected) == 1 })
			p.send(keyDown + keyDown)
			p.waitState("the cursor on 1", func(st fzfState) bool { return lineKey(st.Current) == "1@/trips" })
			p.send(keyTab)
			p.waitState("1 marked", func(st fzfState) bool { return len(st.Selected) == 2 })
			p.send(keyEnter)
		}, []int64{1, 3}},
		{"enter, query matching nothing", false, func(p *picker) {
			p.setQuery("zzz", 0)
			p.send(keyEnter)
		}, []int64{}},
		{"enter on an empty list", true, func(p *picker) { p.send(keyEnter) }, []int64{}},
		{"quit", false, func(p *picker) { p.quit() }, []int64{}},
		{"quit with q", false, func(p *picker) {
			p.command()
			p.send("q")
		}, []int64{}},
		{"enter in command mode", false, func(p *picker) {
			p.command()
			p.send(keyEnter)
		}, []int64{1}},
		{"quit with marks", false, func(p *picker) {
			p.send(keyTab)
			p.waitState("1 marked", func(st fzfState) bool { return len(st.Selected) == 1 })
			p.quit()
		}, []int64{}},
		{"quit, query matching nothing", false, func(p *picker) {
			p.setQuery("zzz", 0)
			p.quit()
		}, []int64{}},
		{"quit on an empty list", true, func(p *picker) { p.quit() }, []int64{}},
		{"cancel", false, func(p *picker) { p.send(keyCtrlC) }, nil},
		{"cancel on an empty list", true, func(p *picker) { p.send(keyCtrlC) }, nil},
	}
	eachFzf(t, func(t *testing.T, fzfDir string) {
		full, empty := pickTree(t), newTree(t)
		for _, opts := range []string{"", "--select-1 --exit-0 --expect=esc"} {
			for _, o := range outcomes {
				name := o.name
				if opts != "" {
					name += ", FZF_DEFAULT_OPTS"
				}
				t.Run(name, func(t *testing.T) {
					tr := full
					if o.empty {
						tr = empty
					}
					cmd := tr.cmd("pick", "--fields", "id")
					if opts != "" {
						cmd.Env = append(cmd.Env, "FZF_DEFAULT_OPTS="+opts)
					}
					p := startPick(t, fzfDir, cmd)
					if p.done() {
						t.Fatalf("pick exited before the picker opened: %s", p.stdout.String())
					}
					st := p.loaded()
					if o.empty != (st.TotalCount == 0) {
						t.Fatalf("state %+v", st)
					}
					o.keys(p)
					r := p.result()
					if o.want == nil {
						out := decode(t, r)
						if r.code != 1 || out.Error == nil || out.Error.Kind != "cancelled" || out.Error.Details.Actions == nil || len(out.Error.Details.Actions) != 0 {
							t.Errorf("exit %d: %s", r.code, r.stdout)
						}
						return
					}
					if got := picked(t, r); !slices.Equal(got, o.want) {
						t.Errorf("picked %v, want %v", got, o.want)
					}
				})
			}
		}
	})
}

// Text from data is shown literally and never run: a title, notes with a
// newline, and the initial query, in the input line and command mode's
// header, each holding fzf action syntax
// (pick-spec.md, fzf contract: No data in action text).
func TestPickHostileText(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		// X is relative: callbacks run where pick does. Not the spec's
		// )+execute-silent(touch X)+(, whose last ( leaves a chain fzf
		// would refuse whole, but one fzf would run if it were injected.
		dir := t.TempDir()
		hostile := ")+execute-silent(touch X)+change-footer("
		tr := newTree(t)
		if r := run(t, tr.cmd("create", hostile, "--notes", "first\n"+hostile+"\nlast")); r.code != 0 {
			t.Fatal(r.stdout)
		}
		if r := run(t, tr.cmd("create", "Plain")); r.code != 0 {
			t.Fatal(r.stdout)
		}
		cmd := tr.cmd("pick", "--fields", "id,title", "--query", hostile)
		cmd.Dir = dir
		p := startPick(t, fzfDir, cmd)
		st := p.loaded()
		if st.Query != hostile || lineKey(st.Current) != "1@/" {
			t.Fatalf("state %+v", st)
		}
		// The line, the preview's title, and the notes' middle line.
		p.waitFor("the title in the list and the preview, and the notes", func() bool {
			s := p.screen()
			return strings.Count(s, hostile) >= 3 && strings.Contains(s, "last")
		})
		// Command mode's header shows the hidden query.
		p.command()
		// As much of it as fits the list beside the preview.
		p.waitScreen("[cmd] query: " + hostile[:27])
		p.send("i")
		p.waitScreen("open> " + hostile)
		p.post("change-query()")
		p.waitState("both lines", func(st fzfState) bool { return st.MatchCount == 2 })
		p.send(keyEnter)
		r := p.result()
		if got := picked(t, r); !slices.Equal(got, []int64{1}) {
			t.Errorf("picked %v", got)
		}
		if _, err := os.Stat(filepath.Join(dir, "X")); !os.IsNotExist(err) {
			t.Errorf("hostile text ran: %v", err)
		}
	})
}

// The modes (pick-spec.md, Modes): in insert mode every key types; command
// mode hides the query, keeps it filtering, ignores keys it doesn't bind,
// even those that would edit the query, and has its own keys.
func TestPickModes(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		p := startPick(t, fzfDir, tr.cmd("pick", "--fields", "id"))
		p.loaded()
		cursorOn := func(i int) {
			t.Helper()
			p.waitState(fmt.Sprintf("the cursor on line %d", i), func(st fzfState) bool { return st.Current != nil && st.Current.Index == i })
		}

		// Insert mode: command mode's keys type.
		p.setQuery("jkgGq ?/i", 0)
		p.send(keyCtrlU)
		p.setQuery("s", 3)

		p.command()
		p.waitScreen("[cmd] query: s")
		if strings.Contains(p.screen(), "open> ") {
			t.Errorf("input line shown in command mode:\n%s", p.screen())
		}
		// Keys command mode doesn't bind, and those bound to edit the
		// query, do nothing while it is hidden; k after them shows they
		// were handled.
		p.send("xz\x7f" + keyCtrlU + keyCtrlD + "\x17" + "k")
		cursorOn(1)
		if st := p.state(); st.Query != "s" || st.MatchCount != 3 {
			t.Errorf("query %q matching %d", st.Query, st.MatchCount)
		}
		p.send("G")
		cursorOn(2)
		p.send("g")
		cursorOn(0)
		p.send("k")
		cursorOn(1)
		p.send("j")
		cursorOn(0)

		// Marks: space, and Tab.
		p.send(" ")
		p.waitState("a mark", func(st fzfState) bool { return len(st.Selected) == 1 })
		p.send("k")
		cursorOn(1)
		p.send(keyTab)
		p.waitState("two marks", func(st fzfState) bool { return len(st.Selected) == 2 })

		// ? shows the keys in the preview until the cursor moves. Tab
		// moved down after marking, to line 0, so k goes to 2's line.
		p.send("?")
		p.waitScreen("until the cursor moves")
		p.send("k")
		p.waitFor("the help gone", func() bool {
			s := p.screen()
			return !strings.Contains(s, "until the cursor moves") && strings.Contains(s, "#2 Renew passport")
		})

		// i back to insert mode: the query shows, and keys edit and type.
		p.send("i")
		p.waitFor("insert mode", func() bool {
			s := p.screen()
			return strings.Contains(s, "open> s") && !strings.Contains(s, "[cmd]")
		})
		p.send(keyCtrlU)
		p.setQuery("j", 0)
		// ctrl-space to command mode, / back.
		p.send("\x00")
		p.waitScreen("[cmd] query: j")
		p.send("/")
		p.waitScreen("open> j")
		p.send(keyCtrlU)
		p.waitState("the query cleared", func(st fzfState) bool { return st.Query == "" && st.MatchCount == 3 })

		p.send(keyEnter)
		if got := picked(t, p.result()); !slices.Equal(got, []int64{1, 2}) {
			t.Errorf("picked %v", got)
		}
	})
}

// c, the first action, end to end: marked lines completed in line order,
// the list reloaded without them and with the marks cleared, the status
// line, command mode kept, and every call in the output's actions; then,
// with every target shown complete, c reopens.
func TestPickComplete(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		p := startPick(t, fzfDir, tr.cmd("pick", "--fields", "id"))
		p.loaded()
		p.command()
		// Mark 3, then 1: space marks and moves down.
		p.send("G ")
		p.waitState("3 marked", func(st fzfState) bool { return len(st.Selected) == 1 })
		p.send("j ")
		p.waitState("1 marked", func(st fzfState) bool { return len(st.Selected) == 2 })
		p.send("c")
		p.waitScreen("✓ completed 2: 1, 3")
		st := p.waitState("the reload", func(st fzfState) bool { return st.TotalCount == 1 })
		if len(st.Selected) != 0 || lineKey(st.Current) != "2@/trips" {
			t.Errorf("after c: %+v", st)
		}
		if !strings.Contains(p.screen(), "[cmd]") {
			t.Errorf("left command mode:\n%s", p.screen())
		}
		p.send(keyEnter)
		r := p.result()
		out := decode(t, r)
		var ops []string
		for _, a := range out.Result.Actions {
			b, _ := json.Marshal(a)
			ops = append(ops, string(b))
		}
		if r.code != 0 || len(ops) != 2 || !strings.HasPrefix(ops[0], `{"input":{"id":1},"operation":"complete","output":{"ok":true,`) ||
			!strings.HasPrefix(ops[1], `{"input":{"id":3},"operation":"complete","output":{"ok":true,`) {
			t.Errorf("exit %d: %s", r.code, r.stdout)
		}

		// Every target shown complete: c reopens.
		p = startPick(t, fzfDir, tr.cmd("pick", "--fields", "id", "--scope", "all"))
		p.loaded()
		p.command()
		p.send("G")
		// Ready first, then complete: 1 and 3, completed together, by ID.
		p.waitState("the cursor on the last line", func(st fzfState) bool { return lineKey(st.Current) == "3@/trips" })
		p.send("c")
		p.waitScreen("✓ reopened 3")
		p.send(keyEsc) // quits, in command mode
		if r := p.result(); r.code != 0 || !strings.Contains(r.stdout, `"operation":"reopen"`) {
			t.Errorf("exit %d: %s", r.code, r.stdout)
		}
	})
}

// e end to end: the editor gets the terminal, its keys and its screen,
// while fzf is suspended; fzf comes back with the status line, and the
// output names the notes edited. ctrl-c in the editor ends only the editor:
// Enter after it gives the envelope, with the session's actions
// (pick-spec.md, Testing: Interrupts).
func TestPickEdit(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		ed := filepath.Join(t.TempDir(), "ed")
		// It asks on the terminal, and appends the answer to each note.
		script := "#!/bin/sh\nprintf 'EDITOR> '\nread line\nfor f; do echo \"$line\" >> \"$f\"; done\n"
		if err := os.WriteFile(ed, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		pick := func(t *testing.T, env ...string) *picker {
			cmd := tr.cmd("pick", "--fields", "id")
			cmd.Env = append(append(cmd.Env, "VISUAL="+ed), env...)
			p := startPick(t, fzfDir, cmd)
			p.loaded()
			p.command()
			return p
		}
		notesOf := func(id string) string {
			b, _ := os.ReadFile(filepath.Join(tr.root(), "trips", id+".md"))
			return string(b)
		}

		t.Run("edit", func(t *testing.T) {
			p := pick(t)
			p.send("G ") // mark 3
			p.waitState("3 marked", func(st fzfState) bool { return len(st.Selected) == 1 })
			p.send("j ") // and 1
			p.waitState("1 marked", func(st fzfState) bool { return len(st.Selected) == 2 })
			p.send("e")
			p.waitScreen("EDITOR>")
			p.send("hello\r")
			p.waitScreen("✓ edited notes 2: 1, 3")
			if notesOf("1") != "hello\n" || notesOf("3") != "hello\n" {
				t.Errorf("notes %q %q", notesOf("1"), notesOf("3"))
			}
			p.waitScreen("hello") // the preview, reloaded
			p.send(keyEnter)
			r := p.result()
			if out := decode(t, r); r.code != 0 || !slices.Equal(out.Result.NotesEdited, []int64{1, 3}) {
				t.Errorf("exit %d: %s", r.code, r.stdout)
			}
		})

		t.Run("ctrl-c in the editor", func(t *testing.T) {
			// No preview, so the status line has the width.
			p := pick(t, "FTASK_PICK_OPTS=--preview-window=hidden")
			p.send("c") // complete 1, an action to report
			p.waitScreen("✓ completed 1")
			p.waitState("the reload", func(st fzfState) bool { return st.TotalCount == 2 && !st.Reading })
			p.send("e")
			p.waitScreen("EDITOR>")
			p.send(keyCtrlC)
			p.waitScreen("✗ e: editor exited with status 130")
			if p.done() {
				t.Fatal("ctrl-c in the editor ended pick")
			}
			p.send(keyEnter)
			r := p.result()
			out := decode(t, r)
			if r.code != 0 || len(out.Result.Actions) != 1 || len(out.Result.NotesEdited) != 0 {
				t.Errorf("exit %d: %s", r.code, r.stdout)
			}
		})

		t.Run("vi", func(t *testing.T) {
			if _, err := exec.LookPath("vi"); err != nil {
				t.Skip("no vi")
			}
			cmd := tr.cmd("pick", "--fields", "id")
			cmd.Env = append(cmd.Env, "EDITOR=vi")
			p := startPick(t, fzfDir, cmd)
			p.loaded()
			p.command()
			p.send("e")
			p.waitFor("vi", func() bool { return !strings.Contains(p.screen(), "[cmd]") })
			p.send("ofrom vi\x1b")
			p.send(":wq\r")
			p.waitScreen("✓ edited notes 2")
			p.send(keyEsc)
			if r := p.result(); r.code != 0 || !strings.Contains(r.stdout, `"notes_edited":[2]`) {
				t.Errorf("exit %d: %s", r.code, r.stdout)
			}
		})
	})
}

// s and r end to end: s cycles the scope and reloads, and the prompt names
// it; r finds a task another process created.
func TestPickScopeAndReload(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		if r := run(t, tr.cmd("complete", "3")); r.code != 0 {
			t.Fatal(r.stdout)
		}
		p := startPick(t, fzfDir, tr.cmd("pick", "--fields", "id"))
		if st := p.loaded(); st.TotalCount != 2 {
			t.Fatalf("open: %+v", st)
		}
		p.command()
		p.send("s")
		p.waitScreen("✓ scope: all")
		p.waitState("all three", func(st fzfState) bool { return st.TotalCount == 3 })
		p.send("i")
		p.waitScreen("all> ")
		p.command()

		if r := run(t, tr.cmd("create", "Buy adapter", "--folder", "/trips")); r.code != 0 {
			t.Fatal(r.stdout)
		}
		p.send("r")
		p.waitScreen("✓ reloaded")
		p.waitState("the new task", func(st fzfState) bool { return st.TotalCount == 4 })
		p.waitScreen("Buy adapter")
		p.send(keyEsc)
		if r := p.result(); r.code != 0 || !strings.Contains(r.stdout, `"actions":[]`) {
			t.Errorf("exit %d: %s", r.code, r.stdout)
		}
	})
}

// n end to end: the prompt borrows the query line, starting with the
// query, and the list stays put while the title is typed; Enter creates
// the task, clears the query, and puts the cursor on the new task. A
// refused title keeps the prompt open; Esc cancels it.
func TestPickNew(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		p := startPick(t, fzfDir, tr.cmd("pick", "--fields", "id"))
		p.loaded()
		p.setQuery("pa", 2)
		p.command()
		p.send("n")
		p.waitScreen("new> pa")
		p.waitScreen("[new]")
		p.send("ck snacks")
		st := p.waitState("the title typed", func(st fzfState) bool { return st.Query == "pack snacks" })
		if st.MatchCount != 2 {
			t.Errorf("the list moved while typing the title: %+v", st)
		}
		p.send(keyEnter)
		p.waitScreen("✓ created 4")
		p.waitState("the cursor on the new task", func(st fzfState) bool {
			return st.Query == "" && st.TotalCount == 4 && lineKey(st.Current) == "4@/"
		})
		p.waitScreen("[cmd]")
		inCommandMode := func() {
			t.Helper()
			// The input hides on the reload's load event, just after the
			// prompt closes; then unbound keys type nothing, and ? after
			// them, showing the keys, shows they were handled.
			p.waitFor("the input hidden", func() bool { return !strings.Contains(p.screen(), "> ") })
			p.send("xz?")
			p.waitScreen("until the cursor moves")
			if q := p.state().Query; q != "" {
				t.Errorf("typed into the query in command mode: %q", q)
			}
		}
		inCommandMode()

		// A refused title: the prompt stays open; Esc cancels it. ctrl-d
		// on the empty prompt first deletes nothing and keeps it open:
		// fzf's default would end the session.
		p.send("n")
		p.waitScreen("[new]")
		p.send(keyCtrlD + "?")
		p.waitState("ctrl-d handled", func(st fzfState) bool { return st.Query == "?" })
		if p.done() || !strings.Contains(p.screen(), "new> ?") {
			t.Fatalf("ctrl-d ended the prompt:\n%s", p.screen())
		}
		p.send(keyCtrlU)
		p.waitState("the prompt cleared", func(st fzfState) bool { return st.Query == "" })
		p.send(keyEnter)
		p.waitScreen("✗ create: invalid-input")
		if !strings.Contains(p.screen(), "new> ") {
			t.Errorf("prompt closed:\n%s", p.screen())
		}
		p.send(keyEsc)
		p.waitScreen("[cmd]")
		inCommandMode()
		p.send(keyEnter)
		r := p.result()
		out := decode(t, r)
		if r.code != 0 || len(out.Result.Actions) != 2 || len(out.Result.Tasks) != 1 || out.Result.Tasks[0].ID != 4 {
			t.Errorf("exit %d: %s", r.code, r.stdout)
		}
	})
}

// Text from data in the status line is shown literally and never run: here
// an error whose message holds the root path, which holds fzf action syntax
// and a newline (pick-spec.md, Testing: Hostile text).
func TestPickHostileStatus(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads unreadable files")
	}
	eachFzf(t, func(t *testing.T, fzfDir string) {
		// A short home, so the message fits the status line.
		home, err := os.MkdirTemp("", "h")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(filepath.Join(home, "r"), 0o755); os.RemoveAll(home) })
		// The spec's )+execute-silent(touch X)+( leaves a ( that the rest
		// of the message can't close into an action, so fzf would refuse
		// the whole chain; this one makes a chain fzf would run.
		hostile := ")+execute-silent(touch X)+change-footer("
		init := ftask(t, "init", filepath.Join(home, hostile+"\nr"))
		init.Env = []string{"HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "PATH=" + os.Getenv("PATH")}
		if r := run(t, init); r.code != 0 {
			t.Fatal(r.stdout)
		}
		tr := &tree{t: t, env: init.Env, home: home}
		if r := run(t, tr.cmd("create", "one", "--notes", "secret")); r.code != 0 {
			t.Fatal(r.stdout)
		}
		notes := filepath.Join(home, hostile+"\nr", "1.md")
		if err := os.Chmod(notes, 0); err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		cmd := tr.cmd("pick")
		cmd.Dir = dir
		cmd.Env = append(cmd.Env, "FTASK_PICK_OPTS=--preview-window=hidden")
		// Checked however the test ends.
		t.Cleanup(func() {
			for _, d := range []string{dir, home} {
				if _, err := os.Stat(filepath.Join(d, "X")); !os.IsNotExist(err) {
					t.Errorf("hostile text ran, in %s: %v", d, err)
				}
			}
		})
		p := startPick(t, fzfDir, cmd)
		p.loaded()
		p.command()
		p.send("e")
		p.waitScreen("✗ e: open " + home + "/" + hostile + `\nr/1.md: permission denied`)
		p.send(keyEsc)
		if r := p.result(); r.code != 0 {
			t.Errorf("exit %d: %s", r.code, r.stdout)
		}
	})
}

// p end to end, the first prompt on targets: it starts with the target's
// priority, a refused value keeps it open, and the line shows the new one;
// on several targets it starts empty, and null clears them all.
func TestPickPriority(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		if r := run(t, tr.cmd("update", "1", "--priority", "2")); r.code != 0 {
			t.Fatal(r.stdout)
		}
		p := startPick(t, fzfDir, tr.cmd("pick", "--fields", "id,priority"))
		p.loaded()
		p.command()
		p.send("p")
		p.waitScreen("priority 1> 2")
		p.send(keyCtrlU + "high" + keyEnter)
		p.waitScreen("✗ priority 1: invalid-input")
		p.waitScreen("priority 1> high")
		p.send(keyCtrlU + "5" + keyEnter)
		p.waitScreen("✓ set priority 1")
		p.waitScreen("p5")

		// Every line marked: several, empty to start; null clears. Marks
		// wait for the reload: fzf drops those made before its list is in.
		p.waitFor("the input hidden", func() bool { return !strings.Contains(p.screen(), "> ") })
		p.post("select-all")
		p.waitState("three marked", func(st fzfState) bool { return len(st.Selected) == 3 })
		p.send("p")
		p.waitScreen("priority 3 tasks> ")
		p.send("null" + keyEnter)
		p.waitScreen("✓ set priority 3: 1, 2, 3")
		p.waitFor("p5 gone", func() bool { return !strings.Contains(p.screen(), "p5") })
		p.send(keyEnter)
		r := p.result()
		out := decode(t, r)
		if r.code != 0 || len(out.Result.Actions) != 5 || !strings.Contains(r.stdout, `"tasks":[{"id":1,"priority":null}]`) {
			t.Errorf("exit %d: %s", r.code, r.stdout)
		}
	})
}

// t end to end: it starts with the target's tags; a mix of bare and
// prefixed tags is refused with the prompt kept, as is a tag update
// refuses; bare tags then replace them, shown on the line.
func TestPickTags(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		if r := run(t, tr.cmd("update", "1", "--tags-add", "travel")); r.code != 0 {
			t.Fatal(r.stdout)
		}
		p := startPick(t, fzfDir, tr.cmd("pick", "--fields", "id,tags", "--query", "book"))
		p.loaded()
		p.command()
		p.send("t")
		p.waitScreen("tags 1> travel")
		p.send(" +urgent" + keyEnter)
		p.waitScreen("✗ tags: a mix of bare and +/- tags")
		p.waitScreen("tags 1> travel +urgent")
		p.send(keyCtrlU + "Urgent" + keyEnter)
		p.waitScreen("✗ tags 1: invalid-input")
		p.send(keyCtrlU + "travel,urgent" + keyEnter)
		p.waitScreen("✓ set tags 1")
		p.waitScreen("#travel #urgent")
		// The search query came back, and still filters.
		if st := p.state(); st.Query != "book" || st.MatchCount != 1 {
			t.Errorf("query after the prompt: %+v", st)
		}
		p.send(keyEnter)
		if r := p.result(); r.code != 0 || !strings.Contains(r.stdout, `"tasks":[{"id":1,"tags":["travel","urgent"]}]`) {
			t.Errorf("exit %d: %s", r.code, r.stdout)
		}
	})
}

// pick in a pipeline: --from reads an upstream envelope, of each accepted
// shape, while the picker draws on the terminal; stdout goes downstream.
// The rejections end pick before the picker opens (pick-spec.md, Accepted
// envelopes).
func TestPickPipelines(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		if r := run(t, tr.cmd("complete", "3")); r.code != 0 {
			t.Fatal(r.stdout)
		}
		envOf := func(args ...string) string {
			r := run(t, tr.cmd(args...))
			if r.code != 0 {
				t.Fatal(r.stdout)
			}
			return r.stdout
		}
		listed := envOf("list", "--readiness", "ready,complete", "--fields", "id")
		accepted := []struct {
			name, upstream string
			lines          int // candidates
		}{
			{"list", listed, 3},
			{"frontier", envOf("frontier", "--fields", "id,title"), 2},
			{"show", envOf("show", "2"), 1},
			{"single task", envOf("update", "1", "--priority", "1"), 1},
			{"empty tasks", envOf("list", "--tags-any", "none", "--fields", "id"), 0},
		}
		for _, a := range accepted {
			t.Run(a.name, func(t *testing.T) {
				p := startPick(t, fzfDir, stdin(tr.cmd("pick", "--from", "-", "--fields", "id"), a.upstream))
				st := p.loaded()
				if st.TotalCount != a.lines {
					t.Fatalf("%d lines, want %d; screen:\n%s", st.TotalCount, a.lines, p.screen())
				}
				p.quit()
				if got := picked(t, p.result()); len(got) != 0 {
					t.Errorf("picked %v", got)
				}
			})
		}
		// A shell pipeline: list into pick into a second pick, which
		// narrows the first's selection, with both pickers on the one
		// terminal in turn; the result goes to a file.
		t.Run("two passes", func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out.json")
			ft := exec.Command("sh", "-c", fmt.Sprintf(
				"%[1]q list --fields id | %[1]q pick --from - | %[1]q pick --from - --fields id,title > %[2]q", binary, out))
			ft.Env = tr.env
			p := startPick(t, fzfDir, ft)
			if st := p.loaded(); st.TotalCount != 2 {
				t.Fatalf("first pass: %+v", st)
			}
			p.send(keyTab + keyUp + keyTab)
			p.waitState("both marked", func(st fzfState) bool { return len(st.Selected) == 2 })
			p.send(keyEnter)
			// The second picker has the first's selection, on the same port.
			p.waitFor("the second pass", func() bool {
				st, err := p.get()
				return err == nil && st.TotalCount == 2 && len(st.Selected) == 0 && lineKey(st.Current) == "1@/trips"
			})
			p.send(keyUp + keyEnter)
			if code := p.wait(); code != 0 {
				t.Fatalf("exit %d: %s", code, p.stderr.String())
			}
			b, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), `"tasks":[{"id":2,"title":"Renew passport"}]`) {
				t.Errorf("out: %s", b)
			}
		})
		rejected := []struct {
			name, upstream, want string
		}{
			{"not JSON", "nope", `"field":"/ids"`},
			{"two values", envOf("show", "1") + envOf("show", "2"), `"field":"/ids"`},
			{"ok false", run(t, tr.cmd("show", "99")).stdout, `not-found`},
			{"no tasks", `{"ok":true,"result":{"folders":["/"]},"warnings":[]}`, `"field":"/ids"`},
		}
		for _, rj := range rejected {
			t.Run("rejects "+rj.name, func(t *testing.T) {
				p := startPick(t, fzfDir, stdin(tr.cmd("pick", "--from", "-"), rj.upstream))
				r := p.result()
				out := decode(t, r)
				if r.code != 1 || out.Error == nil || out.Error.Kind != "invalid-input" || !strings.Contains(r.stdout, rj.want) {
					t.Errorf("exit %d: %s", r.code, r.stdout)
				}
			})
		}
	})
}

// A final read that fails after the picker closes gives incomplete, with
// the read's error and the actions (pick-spec.md, Output).
func TestPickFinalReadFails(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		p := startPick(t, fzfDir, tr.cmd("pick"))
		p.loaded()
		if err := os.Rename(tr.root(), tr.root()+".gone"); err != nil {
			t.Fatal(err)
		}
		p.send(keyEnter)
		r := p.result()
		out := decode(t, r)
		if r.code != 1 || out.Error == nil || out.Error.Kind != "incomplete" || out.Error.Details.Error == nil ||
			out.Error.Details.Actions == nil || len(out.Error.Details.Actions) != 0 {
			t.Errorf("exit %d: %s", r.code, r.stdout)
		}
	})
}

// ctrl-d on an empty prompt deletes nothing and keeps the session: it is
// delete-char, never fzf's default abort or a delete.
func TestPickCtrlD(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		before := run(t, tr.cmd("list", "--readiness", "ready,blocked,complete")).stdout
		p := startPick(t, fzfDir, tr.cmd("pick", "--fields", "id"))
		p.loaded()
		p.send(keyCtrlD + keyCtrlD)
		// A key after them shows they were handled, and fzf is still up.
		p.setQuery("pack", 1)
		if p.done() {
			t.Fatal("ctrl-d ended pick")
		}
		p.send(keyEnter)
		if got := picked(t, p.result()); !slices.Equal(got, []int64{3}) {
			t.Errorf("picked %v", got)
		}
		if after := run(t, tr.cmd("list", "--readiness", "ready,blocked,complete")).stdout; after != before {
			t.Errorf("tree changed:\n%s\n%s", before, after)
		}
	})
}

// The first load: fzf may or may not run on-load for the first list, but
// the picker always opens with the cursor on the first line.
func TestPickFirstLoadCursor(t *testing.T) {
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		for i := range 20 {
			p := startPick(t, fzfDir, tr.cmd("pick"))
			st := p.loaded()
			if st.Current.Index != 0 || lineKey(st.Current) != "1@/trips" {
				t.Fatalf("run %d: cursor on %+v", i, st.Current)
			}
			p.send(keyCtrlC)
			p.wait()
		}
	})
}

// FZF_DEFAULT_OPTS='--tmux' inside tmux still runs fzf in the terminal,
// tmux's pane, rather than in a popup, which has a terminal of its own
// (pick-spec.md, fzf options). A callback bound through the options says
// which terminal fzf runs on; plain fzf --tmux is the control.
func TestPickTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux installed")
	}
	eachFzf(t, func(t *testing.T, fzfDir string) {
		tr := pickTree(t)
		dir := t.TempDir()
		sock := filepath.Join(dir, "sock")
		ttyBind := func(file string) string {
			return fmt.Sprintf("--bind 'ctrl-t:execute-silent(ps -o tty= -p $$ > %s)'", file)
		}
		env, port := pickEnv(t, fzfDir, append(tr.env, "FZF_DEFAULT_OPTS=--tmux", "PS1=$ ", "FTASK_PICK_OPTS="+ttyBind("pick.tty")))
		cmd := exec.Command("tmux", "-S", sock, "-f", "/dev/null", "new-session", "-x", "100", "-y", "24", "sh")
		cmd.Env, cmd.Dir = env, dir
		p := &picker{term: startTerm(t, cmd, 24, 100), port: port}
		t.Cleanup(func() { exec.Command("tmux", "-S", sock, "kill-server").Run() })
		p.waitScreen("$")
		out, err := exec.Command("tmux", "-S", sock, "display", "-p", "#{pane_tty}").Output()
		if err != nil {
			t.Fatal(err)
		}
		pane := strings.TrimSpace(string(out))
		// ttyOf presses ctrl-t and returns the terminal the callback ran on.
		ttyOf := func(file string) string {
			t.Helper()
			p.send("\x14")
			var b []byte
			p.waitFor(file, func() bool { b, _ = os.ReadFile(filepath.Join(dir, file)); return len(b) > 0 })
			return strings.TrimSpace(string(b))
		}

		p.send("echo Popup | fzf " + ttyBind("control.tty") + " > /dev/null\r")
		p.waitScreen("Popup")
		if got := ttyOf("control.tty"); strings.HasSuffix(pane, "/"+got) {
			t.Fatalf("control: fzf --tmux ran on the pane's terminal, %s", pane)
		}
		p.send(keyCtrlC)
		// Keys typed before the popup closes go to it.
		p.waitFor("the prompt back", func() bool {
			n := 0
			for _, l := range strings.Split(p.screen(), "\n") {
				if strings.HasSuffix(l, "$") || strings.Contains(l, "$ ") {
					n++
				}
			}
			return n == 2
		})

		p.send(fmt.Sprintf("%q pick --fields id > out.json; echo $? > rc\r", binary))
		p.listening()
		p.loaded()
		if got := ttyOf("pick.tty"); !strings.HasSuffix(pane, "/"+got) {
			t.Errorf("pick's fzf ran on %s, not the pane's %s", got, pane)
		}
		p.send(keyEnter)
		rc := filepath.Join(dir, "rc")
		p.waitFor("pick to exit", func() bool { b, _ := os.ReadFile(rc); return len(b) > 0 })
		b, _ := os.ReadFile(rc)
		res, _ := os.ReadFile(filepath.Join(dir, "out.json"))
		if strings.TrimSpace(string(b)) != "0" || !strings.Contains(string(res), `"tasks":[{"id":1}]`) {
			t.Errorf("exit %s: %s", b, res)
		}
	})
}
