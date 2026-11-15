package corpuscore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core/corpus/corpustest"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// TestCohortFiltersDiffSpelledPhrase proves the cohort path grades each sibling
// through its own bundle diff: the filtered sibling has no hit, the control
// sibling still matches.
func TestCohortFiltersDiffSpelledPhrase(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(readcommon.StateDir(root), "corpus-race-selected")
	filtered := filepath.Join(readcommon.StateDir(root), "corpus-race-filtered")
	corpustest.Bundle(t, selected, corpustest.CleanDiff, corpustest.CleanBody)
	corpustest.Bundle(t, filtered, corpustest.SpelledDiff, corpustest.Finding)

	sample := loadCohortSample(t, selected)
	samples, warnings := Cohort(selected, &sample)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %q, want none for complete siblings", warnings)
	}
	if len(samples) != 2 {
		t.Fatalf("samples = %d, want the selected bundle and one sibling", len(samples))
	}
	if samples[0].Report.Matched != 1 || samples[1].Report.Matched != 0 {
		t.Fatalf("cohort matches = [%d %d], want selected 1 and filtered 0",
			samples[0].Report.Matched, samples[1].Report.Matched)
	}
}

// loadCohortSample grades the selected bundle the way calibrate does.
func loadCohortSample(t *testing.T, dir string) Sample {
	t.Helper()
	c, ref := Load(dir)
	if ref != nil {
		t.Fatalf("Load: %v", ref)
	}
	entries, err := readcommon.LoadFindings(dir)
	if err != nil {
		t.Fatalf("findings: %v", err)
	}
	report, ref := EvaluateDir(dir, c, entries)
	if ref != nil {
		t.Fatalf("EvaluateDir: %v", ref)
	}
	sample, ref := LoadSample(dir, report)
	if ref != nil {
		t.Fatalf("LoadSample: %v", ref)
	}
	return sample
}

// TestCohortWarnsOnUnreadableSibling proves a sibling dropped for an unusable
// diff is named, not silently removed from the published denominator.
func TestCohortWarnsOnUnreadableSibling(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(readcommon.StateDir(root), "corpus-race-selected")
	broken := filepath.Join(readcommon.StateDir(root), "corpus-race-broken")
	corpustest.Bundle(t, selected, corpustest.CleanDiff, corpustest.CleanBody)
	corpustest.Bundle(t, broken, corpustest.SpelledDiff, corpustest.Finding)
	if err := os.Remove(filepath.Join(broken, "full.diff")); err != nil {
		t.Fatal(err)
	}
	sample := loadCohortSample(t, selected)
	samples, warnings := Cohort(selected, &sample)
	if len(samples) != 1 {
		t.Fatalf("samples = %d, want only the selected bundle", len(samples))
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], broken) || !strings.Contains(warnings[0], "full.diff") {
		t.Fatalf("warnings = %q, want one naming %s and full.diff", warnings, broken)
	}
}
