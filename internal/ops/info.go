package ops

import "github.com/phansen314/ftask/internal/model"

// InfoInput is info's input: none.
type InfoInput struct{}

func decodeInfo(f *model.Fields, p *model.Problems) any { return InfoInput{} }
