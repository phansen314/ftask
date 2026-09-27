package store

import (
	"testing"
)

func TestLocate(t *testing.T) {
	for _, tc := range []struct {
		goos, home, xdg   string
		wantHome, wantDir string
	}{
		{"linux", "/h", "/x", "/h", "/x/ftask"},
		{"linux", "/h", "", "/h", "/h/.config/ftask"},
		{"linux", "/h", "rel", "/h", "/h/.config/ftask"}, // a relative XDG_CONFIG_HOME is ignored
		{"linux", "", "/x", "", "/x/ftask"},
		{"linux", "rel", "", "", ""}, // a relative HOME is unusable
		{"linux", "", "", "", ""},
		{"darwin", "/h", "/x", "/h", "/h/Library/Application Support/ftask"},
		{"darwin", "", "/x", "", ""},
	} {
		env := map[string]string{"HOME": tc.home, "XDG_CONFIG_HOME": tc.xdg}
		home, dir := Locate(func(k string) string { return env[k] }, tc.goos)
		if home != tc.wantHome || dir != tc.wantDir {
			t.Errorf("%s HOME=%q XDG_CONFIG_HOME=%q: got %q, %q; want %q, %q", tc.goos, tc.home, tc.xdg, home, dir, tc.wantHome, tc.wantDir)
		}
	}
}

func TestParseConfig(t *testing.T) {
	valid := map[string]string{
		`root = "/a"`:                          "/a",
		"root = \"/a\"\n":                      "/a",
		`root="/a"`:                            "/a",
		"\t root \t=\t \"/a\" \t":              "/a",
		"# c\n\n  # indented\nroot = \"/a\"\n": "/a",
		"root = \"/a\"\r\n# c\r\n":             "/a",
		`root = "/a" # trailing`:               "/a",
		`root = "/a"#x`:                        "/a",
		`root = "/q\"b\\s\tt\nn"`:              "/q\"b\\s\tt\nn",
		`root = "/é\U0001F600"`:                "/é\U0001F600",
		"root = \"/raw\ttab é\"":               "/raw\ttab é",
		`root = ""`:                            "",
		"# comment with\ttab\nroot = \"/a\"":   "/a",
	}
	for in, want := range valid {
		got, ok := parseConfig([]byte(in))
		if !ok || got != want {
			t.Errorf("parseConfig(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	invalid := []string{
		"",
		"# only a comment\n",
		"\xEF\xBB\xBFroot = \"/a\"",
		"root = \"/\xff\"",
		"root = \"/a\"\nroot = \"/b\"",
		"other = \"/a\"",
		"root = \"/a\"\nother = 1",
		"[table]\nroot = \"/a\"",
		`"root" = "/a"`,
		`roots = "/a"`,
		`root = '/a'`,
		`root = """/a"""`,
		`root = /a`,
		`root "/a"`,
		`root = "/a`,
		`root = "/a" x`,
		`root = "/a" "b"`,
		`root = "/a\b"`,
		`root = "/a\r"`,
		`root = "/a\x"`,
		`root = "/a\uD800"`,
		`root = "/a\U00110000"`,
		`root = "/a\u00e"`,
		`root = "/a\u+0e9"`,
		`root = "/a\`,
		"root = \"/a\x01\"",
		"root = \"/a\x7f\"",
		"root = \"/a\rb\"",
		"root = \"/a\" # c\x01",
		"# c\x01\nroot = \"/a\"",
		"root = \"/a\"\r",
		"\rroot = \"/a\"",
	}
	for _, in := range invalid {
		if got, ok := parseConfig([]byte(in)); ok {
			t.Errorf("parseConfig(%q) = %q, want corrupt", in, got)
		}
	}
}

func TestParseRootPath(t *testing.T) {
	for raw, want := range map[string]RootPath{
		"/":          {Path: "/"},
		"/a/b/":      {Path: "/a/b"},
		"//a/./b":    {Path: "/a/b"},
		"~/":         {UnderHome: true, Path: "."},
		"~/tasks/":   {UnderHome: true, Path: "tasks"},
		"~//tasks/x": {UnderHome: true, Path: "tasks/x"},
	} {
		got, ok := ParseRootPath(raw)
		if !ok || got != want {
			t.Errorf("ParseRootPath(%q) = %+v, %v; want %+v", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"", "~", "~user/x", "rel", "./a", "/a/../b", "/..", "~/..", "~/a/../b"} {
		if got, ok := ParseRootPath(raw); ok {
			t.Errorf("ParseRootPath(%q) = %+v, want an illegal form", raw, got)
		}
	}
}

func TestRootPathExpand(t *testing.T) {
	for _, tc := range []struct {
		raw, home, want string
		ok              bool
	}{
		{"/a", "", "/a", true},
		{"~/", "/h", "/h", true},
		{"~/tasks", "/h/", "/h/tasks", true},
		{"~/tasks", "/", "/tasks", true},
		{"~/tasks", "", "", false},
	} {
		r, _ := ParseRootPath(tc.raw)
		got, ok := r.Expand(tc.home)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%q with home %q: got %q, %v; want %q, %v", tc.raw, tc.home, got, ok, tc.want, tc.ok)
		}
	}
}

func TestEncodeConfig(t *testing.T) {
	if got := string(EncodeConfig("/a/b")); got != "root = \"/a/b\"\n" {
		t.Errorf("got %q", got)
	}
	if got := string(EncodeConfig("/q\"b\\s\x01\x7f\n\té")); got != "root = \"/q\\\"b\\\\s\\u0001\\u007F\\n\\té\"\n" {
		t.Errorf("got %q", got)
	}
	for _, root := range []string{"/", "/a b", "/\"\\", "/\x00\x1f\x7f", "/\t\n", "/é😀", "/ "} {
		got, ok := parseConfig(EncodeConfig(root))
		if !ok || got != root {
			t.Errorf("round trip of %q: got %q, %v", root, got, ok)
		}
	}
}
