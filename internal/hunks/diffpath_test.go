package hunks_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/hunks"
)

// TestHeaderPaths pins the shared quote/space-safe header parser: C-quoted
// tokens decode (octal UTF-8, escaped quotes, backslashes), unquoted paths
// may contain spaces, and renames keep both paths.
func TestHeaderPaths(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		header   string
		old, new string
	}{
		{"plain", "diff --git a/x.txt b/x.txt", "x.txt", "x.txt"},
		{"rename", "diff --git a/old.go b/new.go", "old.go", "new.go"},
		{"unquoted spaces", "diff --git a/my file.txt b/my file.txt", "my file.txt", "my file.txt"},
		{"quoted spaces", `diff --git "a/my old.txt" "b/my new.txt"`, "my old.txt", "my new.txt"},
		{"quoted unicode octal", `diff --git "a/h\303\251llo.txt" "b/h\303\251llo.txt"`, "héllo.txt", "héllo.txt"},
		{"quoted escaped quote", `diff --git "a/say\"hi.txt" "b/say\"hi.txt"`, `say"hi.txt`, `say"hi.txt`},
		{"quoted backslash", `diff --git "a/c\\temp" "b/c\\temp"`, `c\temp`, `c\temp`},
		{"tab separator", "diff --git \"a/x y\"\t\"b/x y\"", "x y", "x y"},
		{"deleted to dev null", "diff --git a/gone.txt b/gone.txt", "gone.txt", "gone.txt"},
		{"greedy b-side", "diff --git a/aa b/bb b/b", "aa b/bb", "b"},
		{"single unquoted token", "diff --git a/x", "x", ""},
		{"empty quoted b-side", `diff --git "a/x" ""`, "x", ""},
		{"not a header", "--- a/x", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			oldPath, newPath := hunks.HeaderPaths(tt.header)
			if oldPath != tt.old || newPath != tt.new {
				t.Errorf("HeaderPaths(%q) = %q, %q; want %q, %q", tt.header, oldPath, newPath, tt.old, tt.new)
			}
		})
	}
}

// TestParseQuotedPaths proves hunk rows are keyed by the unquoted path, so
// anchors on a quoted path match the index instead of reading as unknown.
func TestParseQuotedPaths(t *testing.T) {
	t.Parallel()
	diff := "diff --git \"a/h\\303\\251llo.txt\" \"b/h\\303\\251llo.txt\"\n" +
		"--- \"a/h\\303\\251llo.txt\"\n" +
		"+++ \"b/h\\303\\251llo.txt\"\n" +
		"@@ -1,2 +1,2 @@\n" +
		"-old\n" +
		"+new\n"
	got := hunks.Parse(diff)
	if len(got) != 2 {
		t.Fatalf("hunks = %v, want two rows", got)
	}
	for _, h := range got {
		if h.File != "héllo.txt" {
			t.Errorf("row file = %q, want the unquoted path", h.File)
		}
	}
	if !hunks.Indexed(got, "héllo.txt") {
		t.Error("quoted path is not indexed by its unquoted name")
	}
}

// TestUnquotePath proves the standalone rename-to form decodes too.
func TestUnquotePath(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ in, want string }{
		{"plain.txt", "plain.txt"},
		{`"spaced name.txt"`, "spaced name.txt"},
		{`"h\303\251llo"`, "héllo"},
		// A token strconv cannot decode keeps its literal bytes: dropping it
		// would erase the path an anchor has to match.
		{`"bad\q"`, `bad\q`},
		{`""`, ""},
	} {
		if got := hunks.UnquotePath(tt.in); got != tt.want {
			t.Errorf("UnquotePath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
