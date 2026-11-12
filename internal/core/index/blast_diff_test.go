package indexcore

import (
	"slices"
	"testing"
)

// TestBlastChangesReadNewSideLines pins the diff scan: added lines, the two
// sides of a deletion, context that moves the pointer without marking, a rename
// with no hunk, and a C-quoted path.
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
			want: []blastChange{{path: "c.go", lines: []int{2, 3, 4}}},
		},
		{
			name: "a rename with no hunk keeps the new path",
			diff: `diff --git a/old.go b/new.go
similarity index 100%
rename from old.go
rename to new.go
`,
			want: []blastChange{{path: "new.go"}},
		},
		{
			name: "a C-quoted path is decoded",
			diff: "diff --git \"a/h\\303\\251llo.go\" \"b/h\\303\\251llo.go\"\n" +
				"--- \"a/h\\303\\251llo.go\"\n+++ \"b/h\\303\\251llo.go\"\n@@ -1 +1,2 @@\n package a\n+var x = 1\n",
			want: []blastChange{{path: "héllo.go", lines: []int{2}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := blastChanges(tc.diff)
			if len(got) != len(tc.want) {
				t.Fatalf("changes = %+v; want %+v", got, tc.want)
			}
			for i := range tc.want {
				if got[i].path != tc.want[i].path || !slices.Equal(got[i].lines, tc.want[i].lines) {
					t.Errorf("change %d = %+v; want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestBlastHunkStart pins the new-side start reader, including a header the
// parser cannot read.
func TestBlastHunkStart(t *testing.T) {
	cases := []struct {
		header string
		want   int
	}{
		{"@@ -1,3 +1,4 @@", 1},
		{"@@ -5 +9 @@", 9},
		{"@@ -1 +1,0 @@", 1},
		{"@@ nonsense", 0},
	}
	for _, tc := range cases {
		if got := blastHunkStart(tc.header); got != tc.want {
			t.Errorf("blastHunkStart(%q) = %d; want %d", tc.header, got, tc.want)
		}
	}
}
