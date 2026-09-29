package ops

import (
	"slices"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/model"
	"github.com/phansen314/ftask/internal/store"
)

func decodeUnblock(f *model.Fields, p *model.Problems) any {
	in := BlockersInput{ID: requiredID(f, p, "id")}
	in.Blockers, _ = blockerList(f, p, "blockers")
	return in
}

// UnblockOutput is unblock's result (unblock-output): the task after the
// operation, plus the blockers it removed, ascending.
type UnblockOutput struct {
	model.Task
	Removed []model.ID `json:"removed"`
}

// runUnblock removes the blockers from the one task with id, under the write
// lock. It reads no other task file: removing an edge can't create a cycle,
// and a blocker need not exist, which is how a dangling reference is
// cleared. A blocker not in blocked_by is not there to remove; if none is,
// nothing is written.
func runUnblock(env Env, in BlockersInput, w *errs.Collector) (any, *errs.Error) {
	var out UnblockOutput
	e := store.Write(env.Env, w, func(tx *store.Tx) *errs.Error {
		ld, e := findOne(tx, in.ID)
		if e != nil {
			return e
		}
		removed := []model.ID{}
		var kept []model.ID
		for _, b := range ld.Task.BlockedBy {
			if slices.Contains(in.Blockers, b) {
				removed = append(removed, b)
			} else {
				kept = append(kept, b)
			}
		}
		if len(removed) > 0 {
			ld.Task.BlockedBy = kept
			if e := replaceTask(tx, ld.Loc.Rel(), &ld.Task, model.TimestampOf(env.Clock())); e != nil {
				return e
			}
		}
		slices.Sort(removed)
		out.Task, out.Removed = tx.Task(ld), removed
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}
