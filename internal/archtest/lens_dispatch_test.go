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

// findingsCap matches prose or manifest data that bounds how many findings a
// lens may return. The dispatch bounds scope by files; capping the return count
// throws away evidence a lens already paid for, and aggregation is the place
// that decides what survives.
var findingsCap = regexp.MustCompile(`(?i)at most \d+ findings|no more than \d+ findings|"max_findings"\s*:\s*\d`)

// builtinLensIDs is the shipped lens roster in manifest order.
var builtinLensIDs = []string{"api-contract", "perf", "test"}

// lensRow is one manifest lens row as the note header renders it: the id and
// the dispatch caps it declares.
type lensRow struct {
	ID          string `json:"id"`
	MaxFiles    int    `json:"max_files"`
	MaxFindings int    `json:"max_findings"`
}

// TestBuiltinLensManifestCapsFilesNotFindings holds the shipped pack manifest
// to the dispatch contract: every built-in lens keeps the ~12-file scope cap
// and declares no findings cap, so the note header a lens reads carries only
// the file cap.
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

// TestBuiltinLensBodiesCapFilesNotFindings holds each shipped lens body to the
// same contract.
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

// TestReviewerDispatchLeadsWithDomain pins the shared dispatch template: the
// lens is named by the domain it owns, before its hypothesis label and
// question, and the dispatch caps files only.
func TestReviewerDispatchLeadsWithDomain(t *testing.T) {
	t.Parallel()
	prompt := string(readSkillFile(t, "prompts/reviewer-subagent.md"))
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

// builtinPackDir names the shipped built-in pack.
func builtinPackDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(moduleRoot(t), "skills", "verdict", "packs", "builtin")
}
