package cli

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
	// TagList is a comma list of string items; occurrences are joined.
	TagList
	// Repeated is one string item per occurrence.
	Repeated
	// JSON is exactly one JSON value, read like operation input.
	JSON
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
