package archtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var findingsCap = regexp.MustCompile(`(?i)at most \d+ findings|no more than \d+ findings|"max_findings"\s*:\s*\d`)

var builtinLensIDs = []string{"api-contract", "perf", "test"}

type lensRow struct {
	ID          string `json:"id"`
	MaxFiles    int    `json:"max_files"`
	MaxFindings int    `json:"max_findings"`
}

func TestBuiltinLensManifestCapsFilesNotFindings(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join(builtinPackDir(t), "pack.json"))
	if err != nil {
		t.Fatalf("read pack.json: %v", err)
	}
	var manifest struct {
		Lenses []lensRow `json:"lenses"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode pack.json: %v", err)
	}
	want := []lensRow{{ID: "api-contract", MaxFiles: 12}, {ID: "perf", MaxFiles: 12}, {ID: "test", MaxFiles: 12}}
	if !slices.Equal(manifest.Lenses, want) {
		t.Fatalf("pack.json lenses = %v, want %v", manifest.Lenses, want)
	}
}

func TestBuiltinLensBodiesCapFilesNotFindings(t *testing.T) {
	t.Parallel()
	for _, id := range builtinLensIDs {
		body, err := os.ReadFile(filepath.Join(builtinPackDir(t), "lenses", id+".md"))
		if err != nil {
			t.Fatalf("read %s.md: %v", id, err)
		}
		if !strings.Contains(string(body), "at most ~12 files") {
			t.Errorf("%s.md dropped the ~12-file scope cap", id)
		}
		if hit := findingsCap.FindString(string(body)); hit != "" {
			t.Errorf("%s.md still caps findings: %q", id, hit)
		}
	}
}

func TestReviewerDispatchLeadsWithDomain(t *testing.T) {
	t.Parallel()
	prompt := string(readProcedureFile(t, "prompts/reviewer-subagent.md"))
	domain := strings.Index(prompt, "\nDomain: ")
	if domain < 0 {
		t.Fatalf("prompts/reviewer-subagent.md dispatch template carries no domain line")
	}
	if question := strings.Index(prompt, "Question (exactly one"); domain > question {
		t.Errorf("the domain line sits after the question in the dispatch template")
	}
	if !strings.Contains(prompt, "name at most ~12 files") {
		t.Errorf("prompts/reviewer-subagent.md dropped the ~12-file scope cap")
	}
	if hit := findingsCap.FindString(prompt); hit != "" {
		t.Errorf("prompts/reviewer-subagent.md still caps findings: %q", hit)
	}
}

func builtinPackDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(moduleRoot(t), "procedure", "packs", "builtin")
}
