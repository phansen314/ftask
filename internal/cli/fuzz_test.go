package cli

import (
	"bytes"
	"io/fs"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/phansen314/ftask/internal/fsys"
)

// noFiles is an empty, read-only filesystem: fuzzed command lines never
// touch the real disk — an --input path like /dev/zero would never end, and
// init would create directories.
type noFiles struct{}

var _ fsys.FS = noFiles{}

func (noFiles) OpenRoot(string) (fsys.Root, error) { return nil, syscall.ENOENT }
func (noFiles) Mkdir(string, fs.FileMode) error    { return syscall.EROFS }
func (noFiles) MkdirAll(string, fs.FileMode) error { return syscall.EROFS }
func (noFiles) ReadFile(string) ([]byte, error)    { return nil, syscall.ENOENT }
func (noFiles) Stat(string) (fs.FileInfo, error)   { return nil, syscall.ENOENT }
func (noFiles) Lstat(string) (fs.FileInfo, error)  { return nil, syscall.ENOENT }

// Any command line yields help or exactly one envelope line with exit 0-2,
// never a panic (implementation-spec.md, Generated and cross-cutting).
func FuzzRun(f *testing.F) {
	for _, s := range []string{"", "version", "version\x00-i\x00-", "version\x00-i\x00f.json", "init\x00~/t", "init\x00rel", "create\x00t\x00--notes-file\x00-", "create\x00t\x00--notes-file\x00f\x00--blocked-by\x001,x", "create\x00-i\x00-", "create-folder\x00-p\x00/a/b", "complete\x001", "reopen\x00-i\x00-", "t\x001\x00x\x00--mode\x00m", "--help", "t\x00--extra\x00{"} {
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
