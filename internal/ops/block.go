package ops

import (
	"strconv"

	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/model"
)

// BlockersInput is the input of block and unblock: a task and the blockers
// to add or remove.
type BlockersInput struct {
	ID       model.ID
	Blockers []model.ID
}

func decodeBlock(f *model.Fields, p *model.Problems) any {
	in := BlockersInput{ID: requiredID(f, p, "id")}
	var ok bool
	in.Blockers, ok = blockerList(f, p, "blockers")
	if ok && !p.Failed("/id") { // Additional validation runs on valid fields only
		for i, b := range in.Blockers {
			if b == in.ID {
				p.AddAdditional(jsonio.Pointer("/blockers", strconv.Itoa(i)), "a task cannot block itself")
			}
		}
	}
	return in
}
