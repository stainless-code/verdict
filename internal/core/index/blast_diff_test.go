package indexcore

import (
	"slices"
	"testing"
)

// TestBlastChangesReadNewSideLines pins the diff scan: added lines, the two
// sides of a deletion, context that moves the pointer without marking, a rename
// with no hunk, a binary section, a C-quoted path, a no-newline marker, CRLF
// lines, a deleted file, and a new file.
//
//nolint:revive // the diff-fixture table reads as one block
func TestBlastChangesReadNewSideLines(t *testing.T) {
	cases := []struct {
		name string
		diff string
		want []blastChange
	}{
		{
			name: "additions mark the new lines",
			diff: `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -3,0 +4,2 @@
+one
+two
`,
			want: []blastChange{{path: "a.go", lines: []int{4, 5}}},
		},
		{
			name: "a pure deletion marks both sides",
			diff: `diff --git a/b.go b/b.go
--- a/b.go
+++ b/b.go
@@ -5,2 +4,0 @@
-old
-gone
`,
			want: []blastChange{{path: "b.go", lines: []int{4, 5}}},
		},
		{
			name: "context moves the pointer without marking",
			diff: `diff --git a/c.go b/c.go
--- a/c.go
+++ b/c.go
@@ -1,3 +1,4 @@
 one
-two
+2
 three
+four
`,
			want: []blastChange{{path: "c.go", lines: []int{1, 2, 4}}},
		},
		{
			name: "an added content line that starts with ++ still counts its line",
			diff: "diff --git a/p.go b/p.go\n--- a/p.go\n+++ b/p.go\n" +
				"@@ -1 +1,3 @@\n package a\n+++x\n+var y = 2\n",
			want: []blastChange{{path: "p.go", lines: []int{2, 3}}},
		},
		{
			name: "a deleted content line that starts with -- keeps its anchor",
			diff: "diff --git a/s.sql b/s.sql\n--- a/s.sql\n+++ b/s.sql\n" +
				"@@ -1,3 +1,2 @@\n select 1\n--- old comment\n select 2\n",
			want: []blastChange{{path: "s.sql", lines: []int{1, 2}}},
		},
		{
			name: "a rename with no hunk keeps the new path and every declaration",
			diff: `diff --git a/old.go b/new.go
similarity index 100%
rename from old.go
rename to new.go
`,
			want: []blastChange{{path: "new.go", all: true, hunkless: true}},
		},
		{
			name: "a binary section keeps the new path and names no line",
			diff: `diff --git a/img.png b/img.png
new file mode 100644
index 0000000..e69de29
Binary files /dev/null and b/img.png differ
`,
			want: []blastChange{{path: "img.png"}},
		},
		{
			name: "a C-quoted path is decoded",
			diff: "diff --git \"a/h\\303\\251llo.go\" \"b/h\\303\\251llo.go\"\n" +
				"--- \"a/h\\303\\251llo.go\"\n+++ \"b/h\\303\\251llo.go\"\n@@ -1 +1,2 @@\n package a\n+var x = 1\n",
			want: []blastChange{{path: "héllo.go", lines: []int{2}}},
		},
		{
			name: "a no-newline marker moves nothing",
			diff: `diff --git a/n.go b/n.go
--- a/n.go
+++ b/n.go
@@ -1,2 +1,2 @@
 package a
-var x = 1
\ No newline at end of file
+var x = 2
`,
			want: []blastChange{{path: "n.go", lines: []int{1, 2}}},
		},
		{
			name: "CRLF content lines parse as line numbers",
			diff: "diff --git a/w.go b/w.go\n--- a/w.go\n+++ b/w.go\n" +
				"@@ -1,2 +1,3 @@\n package a\r\n+var x = 1\r\n+var y = 2\r\n",
			want: []blastChange{{path: "w.go", lines: []int{2, 3}}},
		},
		{
			name: "a deleted file keeps the new path",
			diff: `diff --git a/gone.go b/gone.go
deleted file mode 100644
index 1234567..0000000
--- a/gone.go
+++ /dev/null
@@ -1,3 +0,0 @@
-package a
-
-func A() {}
`,
			want: []blastChange{{path: "gone.go", lines: []int{1}}},
		},
		{
			name: "a new file marks its added lines",
			diff: `diff --git a/new.go b/new.go
new file mode 100644
index 0000000..1234567
--- /dev/null
+++ b/new.go
@@ -0,0 +1,3 @@
+package a
+
+func A() {}
`,
			want: []blastChange{{path: "new.go", lines: []int{1, 2, 3}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := blastChanges(tc.diff)
			if len(got) != len(tc.want) {
				t.Fatalf("changes = %+v; want %+v", got, tc.want)
			}
			for i := range tc.want {
				if got[i].path != tc.want[i].path || !slices.Equal(got[i].lines, tc.want[i].lines) ||
					got[i].all != tc.want[i].all || got[i].hunkless != tc.want[i].hunkless {
					t.Errorf("change %d = %+v; want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestBlastHunkSeed pins the new-side seed reader, including the zero-count
// header whose change sits after its start line, and a header the parser
// cannot read.
func TestBlastHunkSeed(t *testing.T) {
	cases := []struct {
		header string
		want   int
	}{
		{"@@ -1,3 +1,4 @@", 1},
		{"@@ -5 +9 @@", 9},
		{"@@ -1 +1,0 @@", 2},
		{"@@ -5,2 +4,0 @@", 5},
		{"@@ nonsense", 0},
	}
	for _, tc := range cases {
		if got := blastHunkSeed(tc.header); got != tc.want {
			t.Errorf("blastHunkSeed(%q) = %d; want %d", tc.header, got, tc.want)
		}
	}
}
