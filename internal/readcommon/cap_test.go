package readcommon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// writePaddedStore writes content padded with JSON whitespace to exactly
// size bytes, so an at-cap store still decodes.
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

// growStore extends a store past its cap without writing the body: the cap
// must refuse before decoding, so the contents never matter.
func growStore(t *testing.T, path string, size int64) {
	t.Helper()
	if err := os.Truncate(path, size); err != nil {
		t.Fatal(err)
	}
}

// TestLoadFindingsStoreCap proves findings.json decodes at the cap and
// refuses typed corrupt-store one byte over it.
func TestLoadFindingsStoreCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "findings.json")
	writePaddedStore(t, path, "[]", jq.MaxFindingsBytes)
	entries, err := LoadFindings(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("LoadFindings at cap = %v, %v; want empty, nil", entries, err)
	}
	growStore(t, path, jq.MaxFindingsBytes+1)
	_, err = LoadFindings(dir)
	var ref *core.RefusalError
	if !errors.As(err, &ref) || ref.Code != "corrupt-store" {
		t.Fatalf("err = %v, want corrupt-store", err)
	}
}

// TestPrFieldStoreCap proves PR.json decodes at the cap and reads as absent
// (never allocated) when oversized.
func TestPrFieldStoreCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "PR.json")
	writePaddedStore(t, path, `{"headRefOid":"abc1234"}`, jq.MaxPRBytes)
	if got := PrField(dir, "head_sha"); got != "abc1234" {
		t.Errorf("PrField at cap = %q, want abc1234", got)
	}
	growStore(t, path, jq.MaxPRBytes+1)
	if got := PrField(dir, "head_sha"); got != "" {
		t.Errorf("PrField over cap = %q, want empty", got)
	}
	if got := ProviderOf(dir); got != "" {
		t.Errorf("ProviderOf over cap = %q, want empty", got)
	}
}

// TestReadCostFileStoreCap proves cost.json decodes at the cap and reads
// present-but-unparseable when oversized.
func TestReadCostFileStoreCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cost.json")
	writePaddedStore(t, path, "[]", jq.MaxCostBytes)
	_, present, parsed := readCostFile(path)
	if !present || !parsed {
		t.Errorf("readCostFile at cap = present %v parsed %v, want present and parsed", present, parsed)
	}
	growStore(t, path, jq.MaxCostBytes+1)
	_, present, parsed = readCostFile(path)
	if !present || parsed {
		t.Errorf("readCostFile over cap = present %v parsed %v, want true false", present, parsed)
	}
}
