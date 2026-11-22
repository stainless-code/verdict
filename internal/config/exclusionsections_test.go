package config_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/config"
)

const (
	sectionsNotes  = "diff --git a/notes.txt b/notes.txt\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1 +1 @@\n-a\n+b\n"
	sectionsSecret = "diff --git a/secret/plan.md b/secret/plan.md\n--- a/secret/plan.md\n+++ b/secret/plan.md\n@@ -1 +1 @@\n-PRIVACY-EXCLUDED-SENTINEL\n+c\n"
	sectionsRename = "diff --git a/old/moved.go b/new/moved.go\nsimilarity index 90%\nrename from old/moved.go\nrename to new/moved.go\n"
)

// TestWithoutExcludedSections pins the shared privacy filter: an excluded
// section leaves byte-exactly, an unrelated section stays byte-exactly, and a
// rename is withheld when either endpoint matches.
func TestWithoutExcludedSections(t *testing.T) {
	t.Parallel()
	diff := sectionsNotes + sectionsSecret + sectionsRename
	t.Run("no exclusions", func(t *testing.T) {
		assertFiltered(t, diff, nil, diff)
	})
	t.Run("b-side glob", func(t *testing.T) {
		assertFiltered(t, diff, []string{"secret/**"}, sectionsNotes+sectionsRename)
	})
	t.Run("rename a-side glob", func(t *testing.T) {
		assertFiltered(t, diff, []string{"old/**"}, sectionsNotes+sectionsSecret)
	})
	t.Run("rename b-side glob", func(t *testing.T) {
		assertFiltered(t, diff, []string{"new/**"}, sectionsNotes+sectionsSecret)
	})
	t.Run("everything", func(t *testing.T) {
		assertFiltered(t, diff, []string{"**"}, "")
	})
}

// assertFiltered compares one filter call with its literal expectation.
func assertFiltered(t *testing.T, diff string, exclusions []string, want string) {
	t.Helper()
	if got := config.WithoutExcludedSections(diff, exclusions); got != want {
		t.Fatalf("filtered = %q, want %q", got, want)
	}
}

// TestWithoutExcludedSectionsKeepsPreamble pins the bytes before the first
// header: a hand-made diff may carry them, and the filter may not drop them.
func TestWithoutExcludedSectionsKeepsPreamble(t *testing.T) {
	t.Parallel()
	diff := "warning: 1 file withheld upstream\n" + sectionsSecret + sectionsNotes
	got := config.WithoutExcludedSections(diff, []string{"secret/**"})
	if got != "warning: 1 file withheld upstream\n"+sectionsNotes {
		t.Fatalf("filtered = %q", got)
	}
}

// TestExcludedEndpoint pins the endpoint rule the fetch and record filters
// share: the b-side path, or the a-side path of a rename, never a bare
// substring of either.
func TestExcludedEndpoint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path, oldPath string
		exclusions    []string
		want          bool
	}{
		{path: "notes.txt", want: false},
		{path: "secret/plan.md", exclusions: []string{"secret/**"}, want: true},
		{path: "new/moved.go", oldPath: "old/moved.go", exclusions: []string{"old/**"}, want: true},
		{path: "new/moved.go", oldPath: "old/moved.go", exclusions: []string{"new/**"}, want: true},
		{path: "new/notes.txt", oldPath: "new/notes.txt", exclusions: []string{"old/**"}, want: false},
		{path: "secretive/plan.md", exclusions: []string{"secret/**"}, want: false},
	}
	for _, tc := range tests {
		if got := config.ExcludedEndpoint(tc.path, tc.oldPath, tc.exclusions); got != tc.want {
			t.Errorf("ExcludedEndpoint(%q, %q, %v) = %t, want %t", tc.path, tc.oldPath, tc.exclusions, got, tc.want)
		}
	}
	if got := config.ExcludedEndpoint("secret/plan.md", "", []string{"secret/*"}); !got {
		t.Error("single-star glob did not match inside one directory")
	}
}
