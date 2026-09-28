package cli

import (
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/jsonio"
)

// integer is a JSON integer literal: what Int converts to a number.
var integer = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)

// buildInput places each given argument and option at its field. Values are
// judged by the adapters; the problems returned are the CLI's own —
// non-UTF-8 values and JSON option values that cannot be read — and a field
// with one is left out of the input (implementation-spec.md, Conversion).
func buildInput(c *Command, cmd *cobra.Command, args []string) (*jsonio.Object, []errs.Problem) {
	in := &jsonio.Object{}
	var problems []errs.Problem
	bad := func(field, reason string) { problems = append(problems, errs.Problem{Field: field, Reason: reason}) }

	for i, a := range c.Args {
		if !utf8.ValidString(args[i]) {
			bad(a.Field, "must be UTF-8")
			continue
		}
		setAt(in, a.Field, scalar(a.Type, args[i]))
	}
	for _, o := range c.Options {
		f := cmd.Flags().Lookup(o.Name)
		if !f.Changed || o.Field == "" {
			continue
		}
		var raw []string
		switch o.Type {
		case Bool:
			b, _ := cmd.Flags().GetBool(o.Name)
			setAt(in, o.Field, b)
			continue
		case IDList, TagList, Repeated:
			raw, _ = cmd.Flags().GetStringArray(o.Name)
		default:
			s, _ := cmd.Flags().GetString(o.Name)
			raw = []string{s}
		}
		if !allUTF8(raw) {
			bad(o.Field, "must be UTF-8")
			continue
		}
		switch o.Type {
		case IDList, TagList:
			items := []any{}
			for _, s := range raw {
				if s == "" {
					continue
				}
				for _, item := range strings.Split(s, ",") {
					items = append(items, scalar(itemType(o.Type), item))
				}
			}
			setAt(in, o.Field, items)
		case Repeated:
			items := make([]any, len(raw))
			for i, s := range raw {
				items[i] = s
			}
			setAt(in, o.Field, items)
		case JSON:
			v, repeated, err := jsonio.ParseValueAt([]byte(raw[0]), depth(o.Field))
			switch {
			case err != nil:
				bad(o.Field, "not valid JSON: "+err.Error())
			case len(repeated) > 0:
				for _, r := range repeated {
					bad(o.Field+r, "repeated key")
				}
			default:
				setAt(in, o.Field, v)
			}
		default:
			setAt(in, o.Field, scalar(o.Type, raw[0]))
		}
	}
	return in, problems
}

func itemType(t Type) Type {
	if t == IDList {
		return Int
	}
	return String
}

// scalar converts one token of type t.
func scalar(t Type, s string) any {
	switch {
	case t == NullableInt && s == "null":
		return nil
	case (t == Int || t == NullableInt) && integer.MatchString(s):
		return json.Number(s)
	}
	return s
}

func allUTF8(ss []string) bool {
	for _, s := range ss {
		if !utf8.ValidString(s) {
			return false
		}
	}
	return true
}

// depth is how many levels deep ptr's field sits in the input: 1 for /extra.
func depth(ptr string) int { return strings.Count(ptr, "/") }

// setAt sets the field at ptr, creating the objects above it. Pointers come
// from the command tables: plain names, no escapes.
func setAt(obj *jsonio.Object, ptr string, v any) {
	segs := strings.Split(strings.TrimPrefix(ptr, "/"), "/")
	for _, s := range segs[:len(segs)-1] {
		next, ok := obj.Get(s)
		child, isObj := next.(*jsonio.Object)
		if !ok || !isObj {
			child = &jsonio.Object{}
			obj.Set(s, child)
		}
		obj = child
	}
	obj.Set(segs[len(segs)-1], v)
}

// readInput reads --input: the file at path, or stdin for "-". It returns
// the input object, or the error that stops the command — io if it cannot
// be read, invalid-input if it cannot be read as one JSON object.
func readInput(path string, env Env) (*jsonio.Object, *errs.Error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(env.Stdin)
	} else {
		data, err = env.Ops.FS.ReadFile(path)
	}
	if err != nil {
		return nil, errs.FromOS(path, err)
	}
	obj, repeated, err := jsonio.ParseObject(data)
	if err != nil {
		return nil, errs.InvalidInput([]errs.Problem{{Field: "", Reason: err.Error()}})
	}
	if len(repeated) > 0 {
		ps := make([]errs.Problem, len(repeated))
		for i, r := range repeated {
			ps[i] = errs.Problem{Field: r, Reason: "repeated key"}
		}
		return nil, errs.InvalidInput(ps)
	}
	return obj, nil
}
