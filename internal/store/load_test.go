package store

import (
	"os"
	"syscall"
	"testing"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/fsys"
	"github.com/phansen314/ftask/internal/model"
)

func TestLoad(t *testing.T) {
	f := newFixture(t)
	f.task("proj", 1, false, 2)
	f.write("tasks/proj/2.json", "{")
	f.write("tasks/proj/3.json", "")
	f.task("proj", 4, false)
	f.write("tasks/proj/5.json", `{"schema": 2}`)
	f.task("proj", 7, false)
	f.write("tasks/proj/4.json", f.read("tasks/proj/7.json")) // id 7 in 4.json

	env := f.withFault(fsys.ErrnoAt(fsys.OpReadFile, "proj/6.json", 1, syscall.EIO))
	f.task("proj", 6, false)
	var w errs.Collector
	readTx(t, env, &w, func(tx *Tx) {
		ld := tx.Load(Location{"/proj", 1})
		if ld.State != Usable || ld.Task.ID != 1 || ld.Task.BlockedBy[0] != 2 {
			t.Errorf("1: %+v", ld)
		}
		wantNoErr(t, tx.Needed(ld))
		wantNoErr(t, tx.Relevant(ld))
		task := tx.Task(ld)
		if task.Folder != "/proj" || task.NotesPath != f.root+"/proj/1.md" || task.Title != "task 1" {
			t.Errorf("Task: %+v", task)
		}

		for _, tc := range []struct {
			id     model.ID
			reason errs.CorruptReason
		}{{2, errs.CorruptNotJSON}, {3, errs.CorruptNotJSON}, {4, errs.CorruptInvalid}} {
			ld := tx.Load(Location{"/proj", tc.id})
			if ld.State != Corrupt || ld.Reason != tc.reason {
				t.Errorf("%d: %+v", tc.id, ld)
			}
			wantErr(t, tx.Needed(ld), errs.KindCorrupt, errs.CorruptDetails{Path: tx.Path(ld.Loc.Rel()), Reason: tc.reason})
			wantNoErr(t, tx.Relevant(ld))
		}

		ld = tx.Load(Location{"/proj", 5})
		if ld.State != Unsupported || ld.Found != 2 {
			t.Errorf("5: %+v", ld)
		}
		wantErr(t, tx.Needed(ld), errs.KindUnsupportedFormat,
			errs.UnsupportedFormatDetails{Path: tx.Path("proj/5.json"), Found: 2, Supported: []int64{1}})
		wantNoErr(t, tx.Relevant(ld))

		ld = tx.Load(Location{"/proj", 6})
		if ld.State != Unreadable {
			t.Errorf("6: %+v", ld)
		}
		wantErr(t, tx.Needed(ld), errs.KindIO, errs.IODetails{Path: tx.Path("proj/6.json"), Code: "EIO"})
		wantNoErr(t, tx.Relevant(ld))

		ld = tx.Load(Location{"/proj", 8})
		if ld.State != Vanished {
			t.Errorf("8: %+v", ld)
		}
		wantNoErr(t, tx.Needed(ld))
		wantNoErr(t, tx.Relevant(ld))
	})
	p := func(rel string) string { return f.root + "/" + rel }
	want := []errs.Warning{
		errs.CorruptFile(p("proj/2.json"), 2),
		errs.CorruptFile(p("proj/3.json"), 3),
		errs.CorruptFile(p("proj/4.json"), 4),
		errs.UnsupportedFile(p("proj/5.json"), 5),
		errs.UnreadableFile(p("proj/6.json"), 6, "EIO"),
	}
	got := w.Warnings()
	if len(got) != len(want) {
		t.Fatalf("warnings %+v", got)
	}
	for i := range want {
		if got[i].Paths[0] != want[i].Paths[0] || got[i].Reason != want[i].Reason || got[i].Code != want[i].Code {
			t.Errorf("warning %d: %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A task file is read once per transaction, however often it is loaded.
func TestLoadCached(t *testing.T) {
	f := newFixture(t)
	f.task("", 1, false)
	var ops []fsys.Op
	readTx(t, f.withFault(fsys.Record(&ops)), nil, func(tx *Tx) {
		a := tx.Load(Location{"/", 1})
		b := tx.Load(Location{"/", 1})
		if a != b {
			t.Error("loaded twice")
		}
	})
	n := 0
	for _, op := range ops {
		if op.Name == fsys.OpReadFile && op.Path == "1.json" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("read %d times", n)
	}
}

// A symlink swapped in for a task file is unreadable (ELOOP), not followed.
func TestLoadSymlink(t *testing.T) {
	f := newFixture(t)
	f.task("", 1, false)
	must(t, os.Symlink("1.json", f.root+"/2.json"))
	readTx(t, f.env, nil, func(tx *Tx) {
		ld := tx.Load(Location{"/", 2})
		if ld.State != Unreadable {
			t.Fatalf("%+v", ld)
		}
		wantErr(t, tx.Needed(ld), errs.KindIO, errs.IODetails{Path: tx.Path("2.json"), Code: "ELOOP"})
	})
}
