package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// pick finds fzf on PATH and checks its version before anything else
// (pick-spec.md, Requirements). Fake fzfs stand in for real ones; the real
// one, if installed, is run with a bad FZF_DEFAULT_OPTS.
func TestPickFzfCheck(t *testing.T) {
	fake := func(script string) string {
		dir := t.TempDir()
		if script != "" {
			if err := os.WriteFile(filepath.Join(dir, "fzf"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	type want struct {
		kind, reason, found string
	}
	check := func(t *testing.T, path string, env []string, w want) {
		t.Helper()
		cmd := ftask(t, "pick")
		cmd.Env = slices.DeleteFunc(cmd.Env, func(kv string) bool { return strings.HasPrefix(kv, "PATH=") })
		cmd.Env = append(cmd.Env, append([]string{"PATH=" + path}, env...)...)
		r := run(t, cmd)
		envelope(t, r)
		var got struct {
			Error struct {
				Kind    string `json:"kind"`
				Details struct {
					Reason string  `json:"reason"`
					Found  *string `json:"found"`
				} `json:"details"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
			t.Fatal(err)
		}
		d := got.Error.Details
		if r.code != 1 || got.Error.Kind != w.kind || d.Reason != w.reason || (w.reason == "fzf-too-old" && (d.Found == nil || *d.Found != w.found)) {
			t.Errorf("exit %d: %s", r.code, r.stdout)
		}
	}
	// Past the check, pick stops for now: the picker isn't written yet.
	passed := want{kind: "internal"}

	t.Run("missing", func(t *testing.T) { check(t, fake(""), nil, want{"unavailable", "fzf-missing", ""}) })
	t.Run("too old", func(t *testing.T) {
		check(t, fake("echo '0.62.0 (abc)'"), nil, want{"unavailable", "fzf-too-old", "0.62.0"})
	})
	t.Run("not a version", func(t *testing.T) { check(t, fake("echo 'huh'"), nil, want{"unavailable", "fzf-too-old", "huh"}) })
	t.Run("fails", func(t *testing.T) { check(t, fake("exit 3"), nil, want{"unavailable", "fzf-failed", ""}) })
	t.Run("minimum", func(t *testing.T) { check(t, fake("echo '0.63.0 (397fe8e3)'"), nil, passed) })
	t.Run("not executable", func(t *testing.T) {
		dir := fake("echo 0.63.0")
		if err := os.Chmod(filepath.Join(dir, "fzf"), 0o644); err != nil {
			t.Fatal(err)
		}
		check(t, dir, nil, want{"unavailable", "fzf-missing", ""})
	})
	// The person's options never reach fzf --version.
	t.Run("options withheld", func(t *testing.T) {
		dir := fake(`[ -n "$FZF_DEFAULT_OPTS$FZF_DEFAULT_OPTS_FILE" ] && exit 2; echo 0.63.0`)
		check(t, dir, []string{"FZF_DEFAULT_OPTS=--bogus", "FZF_DEFAULT_OPTS_FILE=/nonexistent"}, passed)
	})
	t.Run("real fzf, bad options", func(t *testing.T) {
		real, err := exec.LookPath("fzf")
		if err != nil {
			t.Skip("no fzf installed")
		}
		check(t, filepath.Dir(real), []string{"FZF_DEFAULT_OPTS=--bogus"}, passed)
	})
}
