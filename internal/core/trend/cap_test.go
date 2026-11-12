package trendcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func writePaddedStore(t *testing.T, path, content string, size int64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, size)
	copy(data, content)
	for i := len(content); i < len(data); i++ {
		data[i] = ' '
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestFindingsStoreReadRefusesOversizedInput proves trend's per-bundle scan
// decodes findings.json at the cap and skips (never allocates) it one byte
// over.
func TestFindingsStoreReadRefusesOversizedInput(t *testing.T) {
	t.Chdir(t.TempDir())
	path := filepath.Join(".verdict", "state", "5101", "findings.json")
	writePaddedStore(t, path, "[]", jq.MaxFindingsBytes)
	entries, err := os.ReadDir(filepath.Join(".verdict", "state"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("ReadDir(state) = %d entries, %v; want 1", len(entries), err)
	}
	dir := filepath.Join(".verdict", "state", "5101")
	if _, ok := readBundleRow(&state{req: &Request{}}, dir, "5101"); !ok {
		t.Fatal("readBundleRow at cap = false, want true")
	}
	if err := os.Truncate(path, jq.MaxFindingsBytes+1); err != nil {
		t.Fatal(err)
	}
	st := &state{req: &Request{}}
	if _, ok := readBundleRow(st, dir, "5101"); ok {
		t.Fatal("readBundleRow over cap = true, want false")
	}
	if len(st.warnings) != 1 || !strings.Contains(st.warnings[0], "trend skips") {
		t.Errorf("warnings = %q, want one trend-skips line", st.warnings)
	}
}

// TestCostRecordsStoreCap proves the per-bundle cost store decodes at the cap
// and reads absent when oversized.
func TestCostRecordsStoreCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), readcommon.CostFile)
	writePaddedStore(t, path, `[{"est_usd":1}]`, jq.MaxCostBytes)
	if recs := readcommon.CostRecords(filepath.Dir(path)); len(recs) != 1 {
		t.Fatalf("costRecords at cap = %d records, want 1", len(recs))
	}
	if err := os.Truncate(path, jq.MaxCostBytes+1); err != nil {
		t.Fatal(err)
	}
	if recs := readcommon.CostRecords(filepath.Dir(path)); recs != nil {
		t.Errorf("costRecords over cap = %v, want nil", recs)
	}
}

// TestProvenanceReadRefusesOversizedPost proves the bundle's provenance triple
// comes from a capped read: post.json at the cap reads the recorded triple,
// and one byte over reads unmarked instead of being allocated and decoded.
func TestProvenanceReadRefusesOversizedPost(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "5101") + "/"
	post := `{"tool_version":"9.9.9","prompts_hash":"hash","model":"m"} `
	writePaddedStore(t, bundle+"post.json", post, jq.MaxPRBytes)
	if got, _ := bundleProvenance(bundle).Get("tool_version"); got != "9.9.9" {
		t.Fatalf("post.json at cap: tool_version = %v, want 9.9.9", got)
	}
	writePaddedStore(t, bundle+"post.json", post, jq.MaxPRBytes+1)
	for _, key := range []string{"tool_version", "prompts_hash", "model"} {
		if got, _ := bundleProvenance(bundle).Get(key); got != nil {
			t.Errorf("post.json over cap: %s = %v, want null", key, got)
		}
	}
}
