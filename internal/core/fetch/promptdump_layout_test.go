package fetchcore

import (
	"errors"
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
	got, err := reviewProcedure()
	if err != nil {
		t.Fatalf("reviewProcedure: %v", err)
	}
	if string(got) != want.String() {
		t.Fatalf("reviewProcedure = %q, want the embedded assembly %q", got, want.String())
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

// TestPromptDumpRefusesMissingEmbeddedPreamble pins the typed refusal for a
// binary whose embedded preamble is missing, whatever the working directory.
func TestPromptDumpRefusesMissingEmbeddedPreamble(t *testing.T) {
	var warnings []string
	ctx := &fetchRunCtx{runner: &session{warnings: &warnings}}
	var sb strings.Builder
	ref := dumpPrompts(ctx, &sb, func(name string) ([]byte, error) {
		if name == "prompts/system.md" {
			return nil, errors.New("read embedded prompts/system.md: not found")
		}
		return []byte("trusted\n"), nil
	})
	if ref == nil || ref.Code != "prompt-dump-failed" {
		t.Fatalf("refusal = %+v, want prompt-dump-failed", ref)
	}
	if sb.Len() != 0 {
		t.Errorf("dump wrote %q before refusing", sb.String())
	}
}

// TestPromptDumpWarnsForMissingEmbeddedSibling pins the degrade path: a
// missing non-preamble file is named on stderr and the rest is still trusted.
func TestPromptDumpWarnsForMissingEmbeddedSibling(t *testing.T) {
	var warnings []string
	ctx := &fetchRunCtx{runner: &session{warnings: &warnings}}
	var sb strings.Builder
	ref := dumpPrompts(ctx, &sb, func(name string) ([]byte, error) {
		if name == "prompts/diagrams.md" {
			return nil, errors.New("read embedded prompts/diagrams.md: not found")
		}
		return []byte(name + "\n"), nil
	})
	if ref != nil {
		t.Fatalf("refusal = %+v, want none", ref)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "prompts/diagrams.md") {
		t.Errorf("warnings = %q, want the missing payload file named", warnings)
	}
	if strings.Contains(sb.String(), "prompts/diagrams.md\n") || !strings.Contains(sb.String(), "prompts/system.md\n") {
		t.Errorf("dump = %q, want the siblings kept and the missing file omitted", sb.String())
	}
}
