package trendcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// truncationReceipt is the exact bytes fetch writes when a source scan stops
// at its page cap: 100 files listed, 12 expected, no continuation.
const truncationReceipt = "{\n  \"truncated\": true,\n  \"truncation_reason\": \"scan_cap\",\n" +
	"  \"returned\": 100,\n  \"total\": 12,\n  \"next_offset\": null\n}\n"

// TestTrendCarriesBundleReceipt proves trend restates a truncated bundle's
// source receipt over the rollup: the reason survives, returned counts the
// rows trend read, and the scan's total and continuation do not.
func TestTrendCarriesBundleReceipt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	state := readcommon.StateDir(root)
	dir := filepath.Join(state, "42")
	writeListedStore(t, dir, "["+trendOpenFinding+","+trendOpenFinding+"]")
	if err := os.WriteFile(filepath.Join(dir, "truncation.json"), []byte(truncationReceipt), 0o600); err != nil {
		t.Fatal(err)
	}
	read := core.ReadRequest{Runtime: &core.Runtime{Progress: &progressLog{}}, RepoRoot: root}
	resp, ref := Run(t.Context(), &Request{ReadRequest: read})
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	r := resp.Data.Receipt
	if r == nil || !r.Truncated || r.TruncationReason != "scan_cap" || r.Returned != 1 || r.Total != nil || r.NextOffset != nil {
		t.Fatalf("trend receipt = %+v", r)
	}
}

// TestTrendOversizedStoreIsAScanCut proves a findings.json over the read cap
// marks the rollup partial: the bundle is skipped, and the receipt says
// scan_cap with returned counting only the rows that were read.
func TestTrendOversizedStoreIsAScanCut(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	state := readcommon.StateDir(root)
	dir := filepath.Join(state, "42")
	writeListedStore(t, dir, "["+trendOpenFinding+"]")
	oversized := filepath.Join(state, "local-big")
	if err := os.MkdirAll(oversized, 0o755); err != nil {
		t.Fatal(err)
	}
	big := "[" + strings.Repeat(`{"sig":"aaaaaaaaaa"},`, 2000) + strings.Repeat("x", int(jq.MaxFindingsBytes)) + "]"
	if err := os.WriteFile(filepath.Join(oversized, "findings.json"), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	read := core.ReadRequest{Runtime: &core.Runtime{Progress: &progressLog{}}, RepoRoot: root}
	resp, ref := Run(t.Context(), &Request{ReadRequest: read})
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	if len(resp.Data.Rows) != 1 {
		t.Fatalf("rows = %d, want only the readable bundle", len(resp.Data.Rows))
	}
	r := resp.Data.Receipt
	if r == nil || !r.Truncated || r.TruncationReason != "scan_cap" || r.Returned != 1 {
		t.Fatalf("oversized-store receipt = %+v", r)
	}
	wire := resp.Data.Wire()
	if got, _ := wire.Get("truncated"); got != true {
		t.Fatalf("wire truncated = %v, want true", got)
	}
}

// TestTrendWithoutReceiptStaysLegacy proves a complete portfolio publishes no
// receipt: nothing truncated, nothing added.
func TestTrendWithoutReceiptStaysLegacy(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	state := readcommon.StateDir(root)
	writeListedStore(t, filepath.Join(state, "42"), "["+trendOpenFinding+"]")
	read := core.ReadRequest{Runtime: &core.Runtime{Progress: &progressLog{}}, RepoRoot: root}
	resp, ref := Run(t.Context(), &Request{ReadRequest: read})
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	if resp.Data.Receipt != nil {
		t.Fatalf("untruncated trend grew a receipt: %+v", resp.Data.Receipt)
	}
}
