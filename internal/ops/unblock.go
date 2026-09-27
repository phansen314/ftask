package ops

import "github.com/phansen314/ftask/internal/model"

// unblock accepts id among the blockers: it is simply not there to remove.
func decodeUnblock(f *model.Fields, p *model.Problems) any {
	in := BlockersInput{ID: requiredID(f, p, "id")}
	in.Blockers, _ = blockerList(f, p, "blockers")
	return in
}
