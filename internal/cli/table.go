package cli

import (
	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/jsonio"
)

// Type is how an argument's or option's value becomes its input field
// (implementation-spec.md, Conversion).
type Type int

const (
	// Bool is a flag: --x sets true, --x=false sets false.
	Bool Type = iota
	// String is taken as given; folder paths are strings too.
	String
	// Int becomes a number if shaped like a JSON integer, else stays a
	// string, which the adapter rejects as the wrong type.
	Int
	// NullableInt is Int, with the value null converting to null.
	NullableInt
	// IDList is a comma list of Int items; occurrences are joined.
	IDList
	// StringList is a comma list of string items (tags, field names, readiness
	// values); occurrences are joined.
	StringList
	// Repeated is one string item per occurrence.
	Repeated
	// JSON is exactly one JSON value, read like operation input.
	JSON
	// TextFile names a file, "-" for stdin, whose contents, exactly as they
	// are, become a string. A file that can't be read stops the command
	// with io; contents that are not UTF-8 are a problem at the field.
	TextFile
)

// Command is one CLI command: the operation it runs and how its arguments
// and options map onto that operation's input fields.
type Command struct {
	Name    string
	Op      string // operation name, for ops.Run
	Summary string // one line, for help
	Args    []Arg  // all required, in order
	Options []Option
	Example string
	// Resolve, if set, completes the input before the operation runs, doing
	// what the operation leaves to its caller (init's root). Its problems
	// are reported with the adapter's; its error stops the command.
	Resolve func(in *jsonio.Object, env Env) ([]errs.Problem, *errs.Error)
	// Exclusive lists groups of options, by name, of which at most one may
	// be given.
	Exclusive [][]string
}

// Arg is a positional argument. Every argument is required.
type Arg struct {
	Name  string // shown in help as <name>
	Field string // JSON Pointer of the input field it sets
	Type  Type   // String or Int
}

// Option is a command-specific option. Help may name the value in
// backquotes, as pflag shows it: "folder to list, as a `path`".
type Option struct {
	Name     string // long form, without "--"
	Short    string // one letter, or ""
	Field    string // JSON Pointer of the input field it sets
	Type     Type
	Required bool // unless --input is given
	Help     string
}
