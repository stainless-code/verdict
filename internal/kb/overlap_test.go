package kb

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

func TestFindingRuleIDsSkipsEmptyIDs(t *testing.T) {
	finding := jq.NewObj("kb", []any{"", "r1", "r1", 7, "r2"})
	got := FindingRuleIDs(finding)
	if strings.Join(got, ",") != "r1,r2" {
		t.Fatalf("FindingRuleIDs = %q, want [r1 r2]", got)
	}
}

func TestReadJoinsRefusesOversizeFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(Joins))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	row := `{"rule":"r1"}` + "\n"
	padded := row + strings.Repeat(" ", int(jq.MaxKBBytes)-len(row)+1)
	if err := os.WriteFile(path, []byte(padded), 0o600); err != nil {
		t.Fatal(err)
	}
	var warned []string
	got := ReadJoins(root, func(m string) { warned = append(warned, m) })
	if len(got) != 0 || len(warned) != 1 {
		t.Fatalf("ReadJoins over the cap = %d rows, warnings %q; want 0 rows and one warning", len(got), warned)
	}
}

func TestComputeOverlapWarnsOnOversizeRecallManifest(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".verdict", "state", "7")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "findings.json"), []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := `["r1"]`
	padded := manifest + strings.Repeat(" ", int(jq.MaxKBBytes)-len(manifest)+1)
	if err := os.WriteFile(filepath.Join(dir, "kb-recall.json"), []byte(padded), 0o600); err != nil {
		t.Fatal(err)
	}
	var warned []string
	warn := func(m string) { warned = append(warned, m) }
	ComputeOverlap(root, "skip ", ScanBundleFindings(root, "skip ", warn), warn)
	if !slices.ContainsFunc(warned, func(m string) bool { return strings.Contains(m, "kb-recall.json") }) {
		t.Fatalf("warnings = %q, want the oversize kb-recall.json named", warned)
	}
}

func TestScanBundleFindingsWarnsOnUnreadableState(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, ".verdict", "state")
	if err := os.MkdirAll(state, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(state, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(state, 0o750); err != nil {
			t.Error(err)
		}
	})
	if _, err := os.ReadDir(state); err == nil {
		t.Skip("state dir still readable (running as root?)")
	}
	var warned []string
	ScanBundleFindings(root, "skip ", func(m string) { warned = append(warned, m) })
	if len(warned) != 1 {
		t.Fatalf("warnings = %q, want one unreadable-state warning", warned)
	}
	if out := ScanBundleFindings(t.TempDir(), "skip ", func(m string) { t.Errorf("missing state dir warned: %s", m) }); out != nil {
		t.Fatalf("missing state dir = %v, want nil", out)
	}
}
