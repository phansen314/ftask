package cli

import (
	"bytes"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/ftask/internal/fsys"
)

// noFiles is a filesystem with no files: fuzzed --input paths read nothing
// from the real disk (a path like /dev/zero would never end).
type noFiles struct{ fsys.FS }

func (noFiles) ReadFile(string) ([]byte, error) { return nil, syscall.ENOENT }

// Any command line yields help or exactly one envelope line with exit 0-2,
// never a panic (implementation-spec.md, Generated and cross-cutting).
func FuzzRun(f *testing.F) {
	for _, s := range []string{"", "version", "version\x00-i\x00-", "version\x00-i\x00f.json", "t\x001\x00x\x00--mode\x00m", "--help", "t\x00--extra\x00{"} {
		f.Add(s, "{}")
	}
	cmds := append(slices.Clone(commands), synthetic...)
	f.Fuzz(func(t *testing.T, line, stdin string) {
		var args []string
		if line != "" {
			args = strings.Split(line, "\x00")
		}
		env, _, _ := testEnv(stdin)
		env.Ops.FS = noFiles{}
		out, code := execute(cmds, args, env)
		if code == ExitOK && !bytes.HasPrefix(out, []byte("{")) {
			return // help or completion
		}
		if code < ExitOK || code > ExitUsage || !bytes.HasSuffix(out, []byte("\n")) || bytes.Count(out, []byte("\n")) != 1 {
			t.Fatalf("%q: exit %d, output %q", args, code, out)
		}
	})
}
