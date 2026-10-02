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
		runtime := t.TempDir()
		cmd.Env = append(cmd.Env, append([]string{"PATH=" + path, "XDG_RUNTIME_DIR=" + runtime}, env...)...)
		r := run(t, cmd)
		envelope(t, r)
		// The session, if pick made one, went when it exited.
		if des, err := os.ReadDir(runtime); err != nil || len(des) != 0 {
			t.Errorf("runtime dir after pick: %v, %v", des, err)
		}
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

// pick makes its session under $XDG_RUNTIME_DIR, else the temp directory,
// and removes it as it exits (pick-spec.md, Session).
func TestPickSession(t *testing.T) {
	fzf := t.TempDir()
	if err := os.WriteFile(filepath.Join(fzf, "fzf"), []byte("#!/bin/sh\necho 0.63.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	pick := func(env ...string) result {
		cmd := ftask(t, "pick")
		cmd.Env = slices.DeleteFunc(cmd.Env, func(kv string) bool { return strings.HasPrefix(kv, "PATH=") })
		cmd.Env = append(cmd.Env, append([]string{"PATH=" + fzf}, env...)...)
		r := run(t, cmd)
		envelope(t, r)
		return r
	}
	kind := func(r result) string {
		var got struct {
			Error struct {
				Kind string `json:"kind"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
			t.Fatal(err)
		}
		return got.Error.Kind
	}
	// A runtime directory that doesn't exist is where pick tried.
	missing := filepath.Join(t.TempDir(), "missing")
	if r := pick("XDG_RUNTIME_DIR="+missing, "TMPDIR="+t.TempDir()); kind(r) != "io" || !strings.Contains(r.stdout, missing) {
		t.Errorf("missing runtime dir: %s", r.stdout)
	}
	if r := pick("XDG_RUNTIME_DIR=", "TMPDIR="+missing); kind(r) != "io" || !strings.Contains(r.stdout, missing) {
		t.Errorf("empty runtime dir, missing temp dir: %s", r.stdout)
	}
	tmp := t.TempDir()
	if r := pick("TMPDIR=" + tmp); kind(r) != "internal" {
		t.Errorf("temp dir: %s", r.stdout)
	}
	if des, _ := os.ReadDir(tmp); len(des) != 0 {
		t.Errorf("temp dir after pick: %v", des)
	}
}

// ftask __pick is pick's hidden helper: outside a session it is a usage
// error, and help never shows it.
func TestPickHelper(t *testing.T) {
	helper := func(env []string, args ...string) result {
		cmd := ftask(t, append([]string{"__pick"}, args...)...)
		cmd.Env = append(cmd.Env, env...)
		r := run(t, cmd)
		envelope(t, r)
		if r.code != 2 || !strings.Contains(r.stdout, `"kind":"usage"`) {
			t.Errorf("%q %q: exit %d: %s", env, args, r.code, r.stdout)
		}
		return r
	}
	helper(nil, "text", "footer")
	helper([]string{"FTASK_PICK_SESSION=" + t.TempDir()}, "text", "footer")
	// A session pick made, as pick makes it: the verb is what's wrong.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "session"), []byte("ftask pick session 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := helper([]string{"FTASK_PICK_SESSION=" + dir}, "nope"); !strings.Contains(r.stdout, `"argument":"nope"`) {
		t.Errorf("unknown verb: %s", r.stdout)
	}

	for _, args := range [][]string{{"--help"}, {"help"}, {"__pik"}} {
		r := run(t, ftask(t, args...))
		if strings.Contains(r.stdout, "__pick") {
			t.Errorf("%q shows the helper: %s", args, r.stdout)
		}
	}
}
