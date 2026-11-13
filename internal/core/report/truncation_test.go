package reportcore_test

import (
	"context"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

func TestCheckedOutputReceipt(t *testing.T) {
	req := fixture(t, "[]")
	writeFile(t, ".verdict/state/5101/full.diff", "diff --git a/a b/a\ndiff --git a/b b/b\ndiff --git a/c b/c\ndiff --git a/d b/d\ndiff --git a/e b/e\ndiff --git a/f b/f\ndiff --git a/g b/g\ndiff --git a/h b/h\n")
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{ReadRequest: req, Flags: core.ReportFlags{Output: "report.md"}})
	if ref != nil {
		t.Fatal(ref)
	}
	receipt := resp.Data.Receipt
	if receipt == nil || receipt.TruncationReason != "response_cap" || receipt.Returned != 7 || receipt.Total == nil || *receipt.Total != 8 {
		t.Fatalf("checked output receipt = %+v", receipt)
	}
}

// truncationReceipt is the exact bytes fetch writes when a source scan stops
// at its page cap: 100 files listed, 12 expected, no continuation.
const truncationReceipt = "{\n  \"truncated\": true,\n  \"truncation_reason\": \"scan_cap\",\n" +
	"  \"returned\": 100,\n  \"total\": 12,\n  \"next_offset\": null\n}\n"

func writeTruncation(t *testing.T) {
	t.Helper()
	writeFile(t, ".verdict/state/5101/truncation.json", truncationReceipt)
}

// TestNextCarriesBundleReceipt proves next restates a truncated bundle's
// source receipt over its result: the reason survives, returned counts the
// one finding next serves, and the scan's total and continuation do not.
func TestNextCarriesBundleReceipt(t *testing.T) {
	req := fixture(t, "["+openFinding+"]")
	writeTruncation(t)
	resp, ref := reportcore.RunNext(context.Background(), &core.NextRequest{ReadRequest: req})
	if ref != nil {
		t.Fatalf("RunNext refusal: %v", ref)
	}
	if resp.Item == nil {
		t.Fatal("next lost the top finding")
	}
	r := resp.Receipt
	if r == nil || !r.Truncated || r.TruncationReason != "scan_cap" || r.Returned != 1 || r.Total != nil || r.NextOffset != nil {
		t.Fatalf("next receipt = %+v", r)
	}
}

// TestNextEmptyUnderTruncationSaysSo proves an empty next over a truncated
// store is a success envelope with truncated:true returned:0 and a next line
// that stops the caller reading the miss as proof of absence.
func TestNextEmptyUnderTruncationSaysSo(t *testing.T) {
	req := fixture(t, "[]")
	writeTruncation(t)
	resp, ref := reportcore.RunNext(context.Background(), &core.NextRequest{ReadRequest: req})
	if ref != nil {
		t.Fatalf("RunNext refusal: %v", ref)
	}
	if resp.Item != nil {
		t.Fatal("an empty store produced an item")
	}
	if resp.Human != "next: no matching finding in partial results — a miss is not proof of absence" {
		t.Errorf("Human = %q", resp.Human)
	}
	r := resp.Receipt
	if r == nil || !r.Truncated || r.TruncationReason != "scan_cap" || r.Returned != 0 {
		t.Fatalf("empty next receipt = %+v", r)
	}
}

// TestNextWithoutReceiptStaysLegacy proves a complete store still renders
// the legacy empty line with no receipt: nothing truncated, nothing added.
func TestNextWithoutReceiptStaysLegacy(t *testing.T) {
	req := fixture(t, "[]")
	resp, ref := reportcore.RunNext(context.Background(), &core.NextRequest{ReadRequest: req})
	if ref != nil {
		t.Fatalf("RunNext refusal: %v", ref)
	}
	if resp.Receipt != nil {
		t.Fatalf("untruncated next grew a receipt: %+v", resp.Receipt)
	}
	if resp.Human != "next: none open — nothing to act on" {
		t.Errorf("Human = %q", resp.Human)
	}
}

// TestShowReceiptUnderTruncation proves show under a truncated store says so:
// an unknown sig is a success envelope with truncated:true returned:0 (the
// sig may exist past the scan cap), and a found sig counts itself.
func TestShowReceiptUnderTruncation(t *testing.T) {
	req := fixture(t, "["+openFinding+"]")
	writeTruncation(t)
	miss, ref := reportcore.RunShow(context.Background(), &core.ShowRequest{ReadRequest: req, Sig: "deadbeef"})
	if ref != nil {
		t.Fatalf("unknown sig under truncation refused: %v", ref)
	}
	if miss.Item != nil || miss.Fix != nil {
		t.Fatalf("miss carried data: %+v", miss)
	}
	r := miss.Receipt
	if r == nil || !r.Truncated || r.TruncationReason != "scan_cap" || r.Returned != 0 {
		t.Fatalf("show miss receipt = %+v", r)
	}
	hit, ref := reportcore.RunShow(context.Background(), &core.ShowRequest{ReadRequest: req, Sig: "c1111111"})
	if ref != nil {
		t.Fatalf("RunShow refusal: %v", ref)
	}
	if hit.Item == nil || hit.Fix == nil {
		t.Fatal("hit lost its record")
	}
	if r := hit.Receipt; r == nil || r.Returned != 1 || r.TruncationReason != "scan_cap" {
		t.Fatalf("show hit receipt = %+v", r)
	}
}

// TestShowUnknownSigWithoutReceiptRefuses proves the legacy refusal survives
// on a complete store: a miss there is real, not a capped scan.
func TestShowUnknownSigWithoutReceiptRefuses(t *testing.T) {
	req := fixture(t, "["+openFinding+"]")
	_, ref := reportcore.RunShow(context.Background(), &core.ShowRequest{ReadRequest: req, Sig: "deadbeef"})
	if ref == nil || ref.Code != "unknown-sig" {
		t.Fatalf("refusal = %+v, want unknown-sig", ref)
	}
}
