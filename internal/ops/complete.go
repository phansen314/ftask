package ops

import "github.com/phansen314/ftask/internal/model"

func decodeComplete(f *model.Fields, p *model.Problems) any { return decodeID(f, p) }
