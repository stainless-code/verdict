package fetchcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	skillfs "github.com/stainless-code/verdict"
)

// reviewPayloadPaths is the literal trust order the bundle file promises.
// It is typed here, not read from the function under test, so a dropped or
// reordered payload file fails the assembly test.
var reviewPayloadPaths = []string{
	skillfs.ProcedureMD, "prompts/system.md", "prompts/review.md", "prompts/diagrams.md",
}

// TestReviewProcedureFromEmbeddedPayload pins the review assembly against the
// embedded files and proves a bundle-shaped directory on disk cannot change
// it: an installed user runs from a repo with no procedure/ checkout.
func TestReviewProcedureFromEmbeddedPayload(t *testing.T) {
	dir := t.TempDir()
	plantProcedureDecoys(t, dir)
	t.Chdir(dir)

	var want strings.Builder
	for _, name := range reviewPayloadPaths {
		raw, err := skillfs.Read(name)
		if err != nil {
			t.Fatalf("read embedded %s: %v", name, err)
		}
		want.Write(raw)
	}
	got, err := skillfs.ReviewProcedure()
	if err != nil {
		t.Fatalf("ReviewProcedure: %v", err)
	}
	if string(got) != want.String() {
		t.Fatalf("ReviewProcedure = %q, want the embedded assembly %q", got, want.String())
	}
}

// plantProcedureDecoys writes different bytes at every path the old disk
// reader consulted, so a fallback to the working directory is observable.
func plantProcedureDecoys(t *testing.T, dir string) {
	t.Helper()
	paths := []string{"PROCEDURE.md", "prompts/system.md", "prompts/review.md", "prompts/diagrams.md"}
	for i, name := range paths {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		body := "disk decoy " + strings.Repeat(string(rune('a'+i)), 3) + "\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
