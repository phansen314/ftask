package errs

import (
	"bytes"
	"encoding/json"
	"testing"
)

func marshal(t *testing.T, v any) string {
	t.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return string(bytes.TrimSuffix(b.Bytes(), []byte("\n")))
}

func details(t *testing.T, e *Error) string {
	t.Helper()
	return marshal(t, e.Details)
}

func TestInvalidInputSorted(t *testing.T) {
	e := InvalidInput([]Problem{
		{"/tags/2", "b"},
		{"/extra", "z"},
		{"/tags/2", "a"},
		{"", "x"},
		{"/tags/10", "c"},
	})
	want := `{"problems":[{"field":"","reason":"x"},{"field":"/extra","reason":"z"},{"field":"/tags/10","reason":"c"},{"field":"/tags/2","reason":"a"},{"field":"/tags/2","reason":"b"}]}`
	if got := details(t, e); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestDetailsShapes(t *testing.T) {
	arg := ""
	for _, tc := range []struct {
		name string
		e    *Error
		want string
	}{
		{"environment", Environment("HOME"), `{"variable":"HOME"}`},
		{"not-initialized", NotInitialized(MissingMetadata), `{"missing":"metadata"}`},
		{"not-found empty lists", NotFound(nil, []int64{7}, nil), `{"folders":[],"ids":[7],"paths":[]}`},
		{"conflict", Conflict(RuleDuplicateID, []int64{42}), `{"rule":"duplicate-id","ids":[42]}`},
		{"conflict no ids", Conflict(RuleConfigExists, nil), `{"rule":"config-exists","ids":[]}`},
		{"acyclic", Acyclic([]int64{3}, [][]int64{{1, 3, 2}}), `{"rule":"acyclic","ids":[3],"cycles":[[1,3,2]]}`},
		{"busy", Busy(), `{}`},
		{"internal", Internal("x"), `{}`},
		{"corrupt", Corrupt("/r/ftask.json", CorruptUnexpectedFile), `{"path":"/r/ftask.json","reason":"unexpected-file"}`},
		{"io", IO("/r/a", "EACCES"), `{"path":"/r/a","code":"EACCES"}`},
		{"unsupported-format", UnsupportedFormat("/r/1.json", 2, []int64{1}), `{"path":"/r/1.json","found":2,"supported":[1]}`},
		{"usage", Usage([]UsageProblem{{Argument: &arg, Reason: "extra argument"}, {Reason: "missing <id>"}}),
			`{"problems":[{"argument":"","reason":"extra argument"},{"reason":"missing <id>"}]}`},
	} {
		if got := details(t, tc.e); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestErrorJSON(t *testing.T) {
	e := Busy()
	if got, want := marshal(t, e), `{"kind":"busy","message":"another write holds the write lock; retry","details":{}}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	type partial struct {
		ID int64 `json:"id"`
	}
	e = Corrupt("/r/p/5.json", CorruptUnexpectedFile).WithPartial(partial{ID: 5})
	if got, want := marshal(t, e), `{"kind":"corrupt","message":"/r/p/5.json: corrupt (unexpected-file)","details":{"path":"/r/p/5.json","reason":"unexpected-file"},"partial":{"id":5}}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestWarningJSON(t *testing.T) {
	for _, tc := range []struct {
		w    Warning
		want string
	}{
		{CorruptFile("/r/1.json", 1),
			`{"kind":"unusable-file","message":"/r/1.json: skipped, corrupt","paths":["/r/1.json"],"ids":[1],"reason":"corrupt"}`},
		{UnreadableFile("/r/2.json", 2, "EACCES"),
			`{"kind":"unusable-file","message":"/r/2.json: skipped, unreadable (EACCES)","paths":["/r/2.json"],"ids":[2],"reason":"unreadable","code":"EACCES"}`},
		{UnsupportedFile("/r/3.json", 3),
			`{"kind":"unusable-file","message":"/r/3.json: skipped, unsupported-format","paths":["/r/3.json"],"ids":[3],"reason":"unsupported-format"}`},
		{DanglingReference("/r/4.json", 4, 9),
			`{"kind":"dangling-reference","message":"task 4 is blocked by task 9, which does not exist","paths":["/r/4.json"],"ids":[4,9]}`},
		{NotesMissing("/r/5.md", 5, "ENOSPC"),
			`{"kind":"notes-missing","message":"/r/5.md: task 5 written, but its notes could not be (ENOSPC)","paths":["/r/5.md"],"ids":[5],"code":"ENOSPC"}`},
		{UnreadableFolder("/r/p", "EACCES"),
			`{"kind":"unreadable-folder","message":"/r/p: cannot list folder (EACCES)","paths":["/r/p"],"ids":[],"code":"EACCES"}`},
		{DuplicateID(4, nil),
			`{"kind":"duplicate-id","message":"task ID 4 has 0 task files","paths":[],"ids":[4]}`},
	} {
		if got := marshal(t, tc.w); got != tc.want {
			t.Errorf("got  %s\nwant %s", got, tc.want)
		}
	}
}

func TestCollectorDedup(t *testing.T) {
	var c Collector
	c.Add(CorruptFile("/r/1.json", 1))
	c.Add(CorruptFile("/r/1.json", 1))
	c.Add(DuplicateID(4, []string{"/r/4.json", "/r/p/4.json"}))
	c.Add(DuplicateID(4, []string{"/r/4.json"}))
	c.Add(DanglingReference("/r/2.json", 2, 9))
	c.Add(DanglingReference("/r/2.json", 2, 9))
	c.Add(DanglingReference("/r/2.json", 2, 8))
	c.Add(UnreadableFolder("/r/x", "EACCES"))
	c.Add(UnreadableFolder("/r/x", "EACCES"))
	c.Add(NotesMissing("/r/3.md", 3, "ENOSPC"))
	c.Add(NotesMissing("/r/3.md", 3, "ENOSPC"))
	ws := c.Warnings()
	if len(ws) != 6 {
		t.Fatalf("got %d warnings, want 6: %+v", len(ws), ws)
	}
	for _, w := range ws {
		if w.Kind == WarnDuplicateID && len(w.Paths) != 2 {
			t.Errorf("duplicate-id: first recorded should be kept, got %v", w.Paths)
		}
	}
}

func TestCollectorOrder(t *testing.T) {
	var c Collector
	c.Add(CorruptFile("/r/b.json", 2))
	c.Add(DanglingReference("/r/5.json", 5, 10))
	c.Add(DanglingReference("/r/5.json", 5, 9))
	c.Add(CorruptFile("/r/a.json", 1))
	c.Add(DuplicateID(3, []string{"/r/3.json"}))
	c.Add(Warning{Kind: "zz-unknown", Paths: []string{}, IDs: []int64{}})
	c.Add(UnreadableFolder("/r/q", "EACCES"))

	var got []string
	for _, w := range c.Warnings() {
		got = append(got, marshal(t, []any{w.Kind, w.Paths, w.IDs}))
	}
	want := []string{
		`["dangling-reference",["/r/5.json"],[5,9]]`,
		`["dangling-reference",["/r/5.json"],[5,10]]`,
		`["duplicate-id",["/r/3.json"],[3]]`,
		`["unreadable-folder",["/r/q"],[]]`,
		`["unusable-file",["/r/a.json"],[1]]`,
		`["unusable-file",["/r/b.json"],[2]]`,
		`["zz-unknown",[],[]]`,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %s, want %s", i, got[i], want[i])
		}
	}
}

func TestCollectorEmpty(t *testing.T) {
	var c Collector
	if got := marshal(t, c.Warnings()); got != "[]" {
		t.Errorf("empty collector: got %s, want []", got)
	}
}

func TestBrokenPreconditionsAreInternal(t *testing.T) {
	for _, tc := range []struct {
		name string
		e    *Error
	}{
		{"invalid-input no problems", InvalidInput(nil)},
		{"usage no problems", Usage(nil)},
		{"conflict acyclic", Conflict(RuleAcyclic, []int64{3})},
		{"acyclic no cycles", Acyclic([]int64{3}, nil)},
		{"acyclic no ids", Acyclic(nil, nil)},
		{"acyclic length mismatch", Acyclic([]int64{3, 4}, [][]int64{{1, 3}})},
		{"acyclic short cycle", Acyclic([]int64{3}, [][]int64{{3}})},
		{"acyclic repeated ID", Acyclic([]int64{3}, [][]int64{{3, 3}})},
		{"acyclic cycle not through id", Acyclic([]int64{3}, [][]int64{{1, 2}})},
	} {
		if tc.e.Kind != KindInternal {
			t.Errorf("%s: kind %s, want internal", tc.name, tc.e.Kind)
		}
	}
}
