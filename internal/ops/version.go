package ops

import "github.com/phansen314/ftask/internal/model"

// VersionInput is version's input: none.
type VersionInput struct{}

func decodeVersion(f *model.Fields, p *model.Problems) any { return VersionInput{} }
