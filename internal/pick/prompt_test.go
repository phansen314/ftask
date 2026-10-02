package pick

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/phansen314/ftask/internal/jsonio"
)

// prioritizing is a test action with a prompt on its targets: priority,
// set through update, one call per target.
var prioritizing = action{key: "w", arity: anyTargets,
	run: func(r *actionRun, targets []shownLine) {
		start := promptStart(targets, func(t shownLine) string {
			if t.Priority == nil {
				return ""
			}
			return strconv.FormatInt(*t.Priority, 10)
		})
		r.openPrompt("w", targetLabel("priority", targets), start, targets)
	},
	apply: func(r *actionRun, value string, targets []shownLine) bool {
		return r.applyEach(value, targets, func(t shownLine) {
			in := &jsonio.Object{}
			in.Set("id", idNumber(t.ID))
			switch _, err := strconv.ParseInt(value, 10, 64); {
			case value == "":
				in.Set("priority", nil)
			case err == nil:
				in.Set("priority", json.Number(value))
			default:
				in.Set("priority", value) // for update to refuse
			}
			r.call(t.ID, "update", in, "set priority", "priority "+string(idNumber(t.ID)))
		})
	}}

// A prompt on targets: it starts with the one target's value, or empty
// for several; a refused value keeps it open; with several, an empty value
// does nothing; any other failure closes it.
func TestPromptOnTargets(t *testing.T) {
	withAction(t, prioritizing)
	tr := newTestTree(t)
	tr.run("create", map[string]any{"title": "one", "priority": 5})
	tr.run("create", map[string]any{"title": "two"})
	tr.run("create", map[string]any{"title": "three"})
	footerOnly := "transform-footer('/bin/ftask' __pick text 'footer')"
	_, line := tr.pick(map[string]any{}, fzfDoes{do: func(t *testing.T, helper func(...string) string) {
		text := func(name string) string { return helper("text", name) }
		helper("command", "")

		// One target: its value, and its ID in the label.
		helper("act", "w", "1@/")
		if p, q := text("prompt"), text("query"); p != "priority 1> " || q != "5" {
			t.Errorf("one: prompt %q, query %q", p, q)
		}
		if got := helper("enter", "x"); got != footerOnly {
			t.Errorf("refused printed %q", got)
		}
		if f := text("footer"); !strings.HasPrefix(f, "✗ priority 1: invalid-input: ") {
			t.Errorf("refused: footer %q", f)
		}
		if got := helper("enter", "7"); !strings.Contains(got, "reload-sync(") {
			t.Errorf("applied printed %q", got)
		}
		if f := text("footer"); f != "✓ set priority 1" {
			t.Errorf("applied: footer %q", f)
		}

		// Several: empty, counted in the label; empty does nothing.
		helper("act", "w", "2@/", "1@/")
		if p, q := text("prompt"), text("query"); p != "priority 2 tasks> " || q != "" {
			t.Errorf("several: prompt %q, query %q", p, q)
		}
		if got := helper("enter", ""); !strings.Contains(got, "reload-sync(") {
			t.Errorf("no change printed %q", got)
		}
		if f := text("footer"); f != "no change" {
			t.Errorf("no change: footer %q", f)
		}
		helper("act", "w", "3@/", "2@/")
		helper("enter", "3")
		if f := text("footer"); f != "✓ set priority 2: 2, 3" {
			t.Errorf("several: footer %q", f)
		}

		// A failure that isn't the value closes the prompt.
		helper("act", "w", "3@/")
		tr.run("delete", map[string]any{"id": 3})
		if got := helper("enter", "1"); !strings.Contains(got, "reload-sync(") {
			t.Errorf("not found printed %q", got)
		}
		if f := text("footer"); !strings.HasPrefix(f, "✗ priority 3: not-found: ") {
			t.Errorf("not found: footer %q", f)
		}
		helper("quit")
	}})
	// Every call, the refused one included; none for no change.
	if n := strings.Count(string(line), `"operation":"update"`); n != 5 {
		t.Errorf("%d updates: %s", n, line)
	}
}
