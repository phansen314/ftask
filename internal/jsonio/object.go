package jsonio

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Member is one key and its value, in an Object.
type Member struct {
	Key   string
	Value any
}

// Object is a JSON object that keeps its members in order. A parsed object
// keeps a repeated key as two members; callers that accept the object reject
// repeats first (see ParseValue). Its JSON encoding emits members in stored order.
type Object struct {
	Members []Member
}

// Get returns the value of the first member named key.
func (o *Object) Get(key string) (any, bool) {
	for _, m := range o.Members {
		if m.Key == key {
			return m.Value, true
		}
	}
	return nil, false
}

// Set replaces the value of the member named key, keeping its position, or
// appends a new member.
func (o *Object) Set(key string, value any) {
	for i := range o.Members {
		if o.Members[i].Key == key {
			o.Members[i].Value = value
			return
		}
	}
	o.Members = append(o.Members, Member{Key: key, Value: value})
}

// Delete removes the member named key, if present.
func (o *Object) Delete(key string) {
	for i := range o.Members {
		if o.Members[i].Key == key {
			o.Members = append(o.Members[:i], o.Members[i+1:]...)
			return
		}
	}
}

// Len returns the number of members.
func (o *Object) Len() int { return len(o.Members) }

// MarshalJSON emits the members in stored order, strings escaped exactly as
// the rest of ftask's output is (see marshal). It has a value receiver, so an
// Object stored by value encodes the same as a *Object.
func (o Object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	if err := o.appendTo(&b); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (o Object) appendTo(b *bytes.Buffer) error {
	b.WriteByte('{')
	for i, m := range o.Members {
		if i > 0 {
			b.WriteByte(',')
		}
		if err := encodeTo(b, m.Key); err != nil {
			return err
		}
		b.WriteByte(':')
		if err := appendValue(b, m.Value); err != nil {
			return err
		}
	}
	b.WriteByte('}')
	return nil
}

// appendValue appends v's compact encoding. Objects and arrays of the tree are
// written directly rather than through the encoder, which would call each
// nested MarshalJSON and rescan its output: quadratic in depth.
func appendValue(b *bytes.Buffer, v any) error {
	switch v := v.(type) {
	case *Object:
		if v == nil {
			b.WriteString("null")
			return nil
		}
		return v.appendTo(b)
	case Object:
		return v.appendTo(b)
	case []any:
		if v == nil {
			b.WriteString("null")
			return nil
		}
		b.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := appendValue(b, item); err != nil {
				return err
			}
		}
		b.WriteByte(']')
		return nil
	default:
		return encodeTo(b, v)
	}
}

// encodeTo appends v's compact encoding, without HTML escaping and without
// the encoder's trailing newline.
func encodeTo(b *bytes.Buffer, v any) error {
	var tmp bytes.Buffer
	enc := json.NewEncoder(&tmp)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	b.Write(bytes.TrimSuffix(tmp.Bytes(), []byte("\n")))
	return nil
}

// Pointer appends one reference token to the JSON Pointer ptr, escaping it
// per RFC 6901 ("~" as "~0", "/" as "~1").
func Pointer(ptr, token string) string {
	token = strings.ReplaceAll(token, "~", "~0")
	token = strings.ReplaceAll(token, "/", "~1")
	return ptr + "/" + token
}
