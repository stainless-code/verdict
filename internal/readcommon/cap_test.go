package readcommon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// writePaddedStore writes valid JSON padded with trailing whitespace to
// exactly size bytes. The padding parses, so the same bytes decode unless a
// size cap refuses them: an over-cap leg can only fail through the cap.
func writePaddedStore(t *testing.T, path, content string, size int64) {
	t.Helper()
	if int64(len(content)) > size {
		t.Fatalf("fixture is %d bytes, larger than the %d-byte store", len(content), size)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, size)
	copy(data, content)
	for i := len(content); i < len(data); i++ {
		data[i] = ' '
	}
	if !json.Valid(data) {
		t.Fatalf("padded store %q is not valid JSON at %d bytes", content, size)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// requireOverCap fails unless the store's own read path (jq.CappedFile)
// refuses path at limit with ErrTooLarge, so the over-cap leg cannot pass on
// any other failure.
func requireOverCap(t *testing.T, path string, limit int64) {
	t.Helper()
	if _, err := jq.CappedFile(path, limit); !errors.Is(err, jq.ErrTooLarge) {
		t.Fatalf("CappedFile(%s, %d) = %v, want ErrTooLarge", path, limit, err)
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
	writePaddedStore(t, path, "[]", jq.MaxFindingsBytes+1)
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
	const pr = `{"provider":"gitlab","head_sha":"abc1234"}`
	writePaddedStore(t, path, pr, jq.MaxPRBytes)
	if got := PrField(dir, "head_sha"); got != "abc1234" {
		t.Errorf("PrField at cap = %q, want abc1234", got)
	}
	if got := ProviderOf(dir); got != "gitlab" {
		t.Errorf("ProviderOf at cap = %q, want gitlab", got)
	}
	writePaddedStore(t, path, pr, jq.MaxPRBytes+1)
	requireOverCap(t, path, jq.MaxPRBytes)
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
	writePaddedStore(t, path, "[]", jq.MaxCostBytes+1)
	requireOverCap(t, path, jq.MaxCostBytes)
	_, present, parsed = readCostFile(path)
	if !present || parsed {
		t.Errorf("readCostFile over cap = present %v parsed %v, want true false", present, parsed)
	}
}
