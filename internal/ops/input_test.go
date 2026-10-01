package ops

import (
	"reflect"
	"slices"
	"testing"

	"github.com/phansen314/ftask/internal/errs"
	"github.com/phansen314/ftask/internal/jsonio"
	"github.com/phansen314/ftask/internal/model"
	"github.com/phansen314/ftask/internal/schematest"
)

// bases are valid inputs per operation, mutated one place at a time.
var bases = map[string][]string{
	"version":       {`{}`},
	"info":          {`{}`},
	"doctor":        {`{"kinds": ["cycle", "temp-leftover"]}`, `{}`},
	"repair":        {`{"kinds": ["temp-leftover", "metadata-missing"]}`, `{}`},
	"init":          {`{"root": "/home/u/tasks", "replace_config": true}`},
	"create-folder": {`{"folder": "/proj/travel", "parents": true}`},
	"create": {
		`{"title": "Book flights", "folder": "/proj", "priority": 2, "tags": ["travel", "urgent"], "blocked_by": [41, 43], "extra": {"status": "x"}, "notes": "n"}`,
		`{"title": "x"}`,
	},
	"show":          {`{"id": 42}`},
	"complete":      {`{"id": 42}`},
	"reopen":        {`{"id": 42}`},
	"block":         {`{"id": 42, "blockers": [41, 43]}`},
	"unblock":       {`{"id": 42, "blockers": [41, 43]}`},
	"delete":        {`{"id": 42}`},
	"move":          {`{"id": 42, "to": "/proj/travel", "parents": true}`},
	"delete-folder": {`{"folder": "/proj/travel", "recursive": true}`},
	"move-folder":   {`{"folder": "/proj/travel", "to": "/archive", "parents": true}`},
	"update": {
		`{"id": 42, "title": "x", "priority": 3, "tags": {"add": ["a"], "remove": ["b"]}, "extra": {"merge": {"k": 1}, "remove": ["z"]}}`,
		`{"id": 42, "tags": {"replace_all": ["a"]}, "extra": {"replace_all": {"k": 1}}}`,
		`{"id": 42, "priority": null}`,
	},
	"frontier": {`{"folder": "/proj", "recursive": false, "tags_any": ["a", "b"], "tags_all": ["c"], "limit": 10, "fields": ["title", "id"]}`},
	"list":     {`{"folder": "/proj", "recursive": false, "readiness": ["ready", "complete"], "include_folders": true, "tags_any": ["a"], "tags_all": ["b", "c"], "limit": 0, "fields": ["readiness", "blocking"]}`},
}

// scopeCandidates exercise frontier's and list's narrowing: field names and
// readiness values, good and bad, and limits at their bounds.
var scopeCandidates = []string{
	`"ready"`, `"complete"`, `"id"`, `"notes_path"`, `"Ready"`, `"folders"`,
	`["id"]`, `["blocking", "schema"]`, `["id", "id"]`, `["title", "Title"]`, `["nope"]`,
	`["blocked"]`, `["ready", "ready"]`, `["done"]`, `["ready", 1]`,
	`9007199254740991`, `9007199254740992`,
}

// updateCandidates exercise the forms of update's tags and extra.
var updateCandidates = []string{
	`{"replace_all": []}`, `{"replace_all": {}}`, `{"add": []}`, `{"remove": []}`, `{"merge": {}}`,
	`{"add": ["a"], "replace_all": ["b"]}`, `{"merge": {"a": 1}, "replace_all": {}}`,
	`{"add": ["Urgent"]}`, `{"remove": ["a", "a"]}`, `{"merge": {"a": 1}}`, `{"remove": [1]}`,
	`{"add": ["a"], "x": 1}`, `{"replace_all": ["a"], "x": 1}`,
}

func TestInputsAgreeWithSchemas(t *testing.T) {
	for _, op := range Operations() {
		if _, ok := bases[op]; !ok {
			t.Errorf("%s: no base input", op)
		}
	}
	for op, docs := range bases {
		n := 0
		for _, base := range docs {
			// A base both sides reject would let every mutation agree
			// vacuously, losing the accept side unnoticed.
			if ok, f := schematest.Check(t, op+"-input", []byte(base)); !ok {
				t.Errorf("%s: base rejected by the schema at %s\n  %s", op, f, base)
			}
			if ps := problems(t, op, base); ps != nil {
				t.Errorf("%s: base rejected by the adapter: %v\n  %s", op, ps, base)
			}
			for _, doc := range schematest.Mutations(t, base, slices.Concat(updateCandidates, scopeCandidates)...) {
				agreeInput(t, op, doc)
				n++
			}
		}
		t.Logf("%s: %d documents", op, n)
	}
}

// agreeInput checks doc against op's input schema and its adapter: both
// accept, or both reject at the same fields (schematest.Failure.Matches).
// Problems from Additional validation — rules no schema expresses — are left
// out.
func agreeInput(t *testing.T, op, doc string) {
	t.Helper()
	libOK, lib := schematest.Check(t, op+"-input", []byte(doc))
	obj, _, err := jsonio.ParseObject([]byte(doc))
	if err != nil {
		t.Fatalf("parse %s: %v", doc, err)
	}
	var p model.Problems
	decode(decoders[op], obj, &p)
	var adAt []string
	for _, pr := range p.SchemaList() {
		adAt = append(adAt, pr.Field)
	}
	adOK := len(adAt) == 0
	switch {
	case libOK != adOK:
		t.Errorf("%s: schema accepts %v (%s), adapter accepts %v (%q)\n  %s", op, libOK, lib, adOK, adAt, doc)
	case !libOK && !lib.Matches(adAt):
		t.Errorf("%s: schema rejects at %s, adapter at %q\n  %s", op, lib, adAt, doc)
	}
}

func problems(t *testing.T, op, doc string) []errs.Problem {
	t.Helper()
	obj, _, err := jsonio.ParseObject([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	_, p, e := Decode(op, obj)
	if e != nil {
		t.Fatalf("%s %s: %v", op, doc, e)
	}
	if p.OK() {
		return nil
	}
	return errs.InvalidInput(p.List()).Details.(errs.InvalidInputDetails).Problems
}

func fields(ps []errs.Problem) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Field)
	}
	return out
}

// Exact fields, including Additional validation and the forms within
// update's oneOf fields, which the agreement test compares loosely.
func TestInputProblems(t *testing.T) {
	for _, tc := range []struct {
		op, doc string
		want    []string
	}{
		{"version", `{"x": 1}`, []string{"/x"}},
		{"init", `{"root": "tasks"}`, []string{"/root"}},
		{"init", `{"root": "~/tasks"}`, []string{"/root"}},
		{"init", `{"root": "/a/../b"}`, []string{"/root"}},
		{"init", `{"root": "/.."}`, []string{"/root"}},
		{"init", `{"root": ""}`, []string{"/root"}},
		{"init", `{}`, []string{"/root"}},
		{"create", `{"title": "   "}`, []string{"/title"}},
		{"create", `{"title": "a\nb", "folder": "proj", "tags": ["a", "a"]}`, []string{"/folder", "/tags/1", "/title"}},
		{"block", `{"id": 42, "blockers": [41, 42]}`, []string{"/blockers/1"}},
		{"block", `{"id": 42, "blockers": []}`, []string{"/blockers"}},
		{"block", `{"id": 0, "blockers": [0]}`, []string{"/blockers/0", "/id"}},
		{"block", `{"id": 42, "blockers": [42, 42]}`, []string{"/blockers/1"}},
		{"block", `{"id": 42, "blockers": [42, "x"]}`, []string{"/blockers/1"}},
		{"unblock", `{"id": 42, "blockers": [42]}`, nil},
		{"update", `{"id": 42}`, []string{""}},
		{"update", `{"id": 42, "x": 1}`, []string{"", "/x"}},
		{"update", `{"id": 42, "tags": {"add": ["Urgent"]}}`, []string{"/tags/add/0"}},
		{"update", `{"id": 42, "tags": {"add": ["a"], "replace_all": []}}`, []string{"/tags/add"}},
		{"update", `{"id": 42, "tags": {"replace_all": [], "add": [], "x": 1}}`, []string{"/tags/add", "/tags/x"}},
		{"update", `{"id": 42, "tags": {"replace_all": ["A"], "remove": ["a"]}}`, []string{"/tags/remove", "/tags/replace_all/0"}},
		{"update", `{"id": 42, "tags": {"remove": ["a", "a"]}}`, []string{"/tags/remove/1"}},
		{"update", `{"id": 42, "tags": {"replace_all": ["a", "b", "a"]}}`, []string{"/tags/replace_all/2"}},
		{"update", `{"id": 42, "extra": {"merge": [], "remove": ["a"]}}`, []string{"/extra/merge"}},
		{"update", `{"id": 42, "extra": {"replace_all": {}, "merge": {"a": 1}}}`, []string{"/extra/merge"}},
		{"update", `{"id": 42, "tags": {}}`, []string{"/tags"}},
		{"update", `{"id": 42, "tags": {"add": [], "remove": ["a"]}}`, []string{"/tags/add"}},
		{"update", `{"id": 42, "tags": {"add": ["a", "b"], "remove": ["b"]}}`, []string{"/tags/remove/0"}},
		{"update", `{"id": 42, "tags": {"add": ["A"], "remove": ["A"]}}`, []string{"/tags/add/0", "/tags/remove/0"}},
		{"update", `{"id": 42, "tags": {"replace_all": ["a"], "x": 1}}`, []string{"/tags/x"}},
		{"update", `{"id": 42, "extra": {"merge": {"k": 1}, "remove": ["k"]}}`, []string{"/extra/remove/0"}},
		{"update", `{"id": 42, "extra": {"merge": {}}}`, []string{"/extra/merge"}},
		{"update", `{"id": 42, "extra": {"remove": ["a", "a", 1]}}`, []string{"/extra/remove/1", "/extra/remove/2"}},
		{"update", `{"id": 42, "extra": {"replace_all": []}}`, []string{"/extra/replace_all"}},
		{"update", `{"id": 42, "title": ""}`, []string{"/title"}},
		// Integer literals, which the agreement corpus leaves out.
		{"show", `{"id": 2.0}`, []string{"/id"}},
		{"complete", `{"id": 42e0}`, []string{"/id"}},
		{"create", `{"title": "x", "priority": 1.0, "blocked_by": [4, 5.0]}`, []string{"/blocked_by/1", "/priority"}},
		{"block", `{"id": 42, "blockers": [2e0]}`, []string{"/blockers/0"}},
		{"update", `{"id": 42, "priority": 3.0}`, []string{"/priority"}},
	} {
		if got := fields(problems(t, tc.op, tc.doc)); !slices.Equal(got, tc.want) {
			t.Errorf("%s %s: fields %q, want %q", tc.op, tc.doc, got, tc.want)
		}
	}
}

func TestDecodedInputs(t *testing.T) {
	decodeOK := func(op, doc string) any {
		t.Helper()
		obj, _, err := jsonio.ParseObject([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		in, p, e := Decode(op, obj)
		if e != nil || !p.OK() {
			t.Fatalf("%s %s: %v %v", op, doc, e, p.List())
		}
		return in
	}
	if got := decodeOK("init", `{"root": "/a//b/./c/"}`).(InitInput); got != (InitInput{Root: "/a/b/c"}) {
		t.Errorf("init: %+v", got)
	}
	if got := decodeOK("init", `{"root": "/", "replace_config": true}`).(InitInput); got != (InitInput{Root: "/", ReplaceConfig: true}) {
		t.Errorf("init /: %+v", got)
	}
	c := decodeOK("create", `{"title": "  Book  flights　"}`).(CreateInput)
	if c.Title != "Book  flights" || c.Folder != "/" || c.Priority != nil || len(c.Tags) != 0 || c.Tags == nil ||
		c.BlockedBy == nil || c.Extra == nil || c.Extra.Len() != 0 || c.Notes != "" {
		t.Errorf("create defaults: %+v", c)
	}
	if got := decodeOK("frontier", `{}`).(ScopeInput); !reflect.DeepEqual(got, ScopeInput{Folder: "/", Recursive: true}) {
		t.Errorf("frontier defaults: %+v", got)
	}
	if got := decodeOK("list", `{"recursive": false}`).(ScopeInput); !reflect.DeepEqual(got, ScopeInput{Folder: "/", Readiness: []model.Readiness{model.Ready, model.Blocked}}) {
		t.Errorf("list: %+v", got)
	}
	limit := int64(0)
	want := ScopeInput{Folder: "/", Recursive: true, Readiness: []model.Readiness{model.Complete}, Narrowing: Narrowing{
		TagsAny: []model.Tag{"a", "b"}, TagsAll: []model.Tag{"c"}, Limit: &limit, Fields: []string{"title", "id"},
	}}
	if got := decodeOK("list", `{"readiness": ["complete"], "tags_any": ["a", "b"], "tags_all": ["c"], "limit": 0, "fields": ["title", "id"]}`).(ScopeInput); !reflect.DeepEqual(got, want) {
		t.Errorf("list narrowed: %+v", got)
	}
	u := decodeOK("update", `{"id": 7, "priority": null, "tags": {"replace_all": []}, "extra": {"remove": ["a"]}}`).(UpdateInput)
	if u.ID != 7 || u.Title != nil || u.Priority == nil || u.Priority.Value != nil ||
		!u.Tags.Replace || u.Tags.ReplaceAll == nil || len(u.Tags.ReplaceAll) != 0 ||
		u.Extra.ReplaceAll != nil || u.Extra.Merge.Len() != 0 || !reflect.DeepEqual(u.Extra.Remove, []string{"a"}) {
		t.Errorf("update: %+v %+v %+v", u, u.Tags, u.Extra)
	}
	if _, _, e := Decode("nosuch", &jsonio.Object{}); e == nil || e.Kind != errs.KindInternal {
		t.Errorf("unknown operation: %v", e)
	}
}
