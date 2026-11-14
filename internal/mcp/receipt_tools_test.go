package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// truncationReceipt is the exact bytes ScanCut emits on a capped source scan.
var truncationReceipt = jq.PrettyOrdered(core.Wire(core.ScanCut(true, 100)))

const mcpOpenFinding = `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
	`"anchored":true,"body":"Usable body.","suggestion":null,` +
	`"inBounds":true,"rigor":"walked","severity":"critical",` +
	`"sig":"c111111111111111111111111111111111111111","sig_version":"1",` +
	`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open",` +
	`"rationale":"","history":[]}`

// writeTruncatedBundle lands a PR bundle whose store carries a fetch scan
// receipt, the fixture the read tools inherit their receipts from.
func writeTruncatedBundle(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	dir := ".verdict/state/5101"
	writeFile(t, dir+"/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	writeFile(t, dir+"/findings.json", "["+mcpOpenFinding+"]")
	writeFile(t, dir+"/truncation.json", truncationReceipt)
}

// receiptJSON extracts the five receipt keys from a tool result's data and
// renders them compactly for a literal comparison.
func receiptJSON(t *testing.T, data *jq.Obj) string {
	t.Helper()
	receipt := jq.NewObj()
	for _, key := range [...]string{"truncated", "truncation_reason", "returned", "total", "next_offset"} {
		v, present := data.Get(key)
		if !present {
			t.Fatalf("data lost %q", key)
		}
		receipt.Set(key, v)
	}
	return receipt.MarshalCompact()
}

// TestNextToolCarriesReceipt proves the next tool restates a truncated
// bundle's source receipt over its result: reason kept, returned counting the
// served finding, the scan's total and continuation dropped.
func TestNextToolCarriesReceipt(t *testing.T) {
	writeTruncatedBundle(t)
	registered := findTool(t, "next")
	req := &core.NextRequest{}
	req.ReadRequest = core.ReadRequest{Repo: "acme/app", Pr: "5101"}
	data, ref := registered.invoke(context.Background(), req)
	if ref != nil {
		t.Fatalf("next invoke refusal: %v", ref)
	}
	if item, ok := data.Get("item"); !ok || item == nil {
		t.Fatal("next lost the finding")
	}
	const want = `{"truncated":true,"truncation_reason":"scan_cap","returned":1,"total":null,"next_offset":null}`
	if got := receiptJSON(t, data); got != want {
		t.Fatalf("next receipt = %s, want %s", got, want)
	}
}

// TestShowToolCarriesReceipt proves the show tool keeps fix beside item and
// restates the receipt: a found sig counts itself, an unknown sig is a
// truncated empty result instead of a false unknown-sig refusal.
func showReq(sig string) *core.ShowRequest {
	req := &core.ShowRequest{Sig: sig}
	req.ReadRequest = core.ReadRequest{Repo: "acme/app", Pr: "5101"}
	return req
}

func TestShowToolCarriesReceipt(t *testing.T) {
	writeTruncatedBundle(t)
	registered := findTool(t, "show")
	hit, ref := registered.invoke(context.Background(), showReq("c1111111"))
	if ref != nil {
		t.Fatalf("show invoke refusal: %v", ref)
	}
	if _, ok := hit.Get("fix"); !ok {
		t.Fatal("show lost fix")
	}
	const wantHit = `{"truncated":true,"truncation_reason":"scan_cap","returned":1,"total":null,"next_offset":null}`
	if got := receiptJSON(t, hit); got != wantHit {
		t.Fatalf("show hit receipt = %s, want %s", got, wantHit)
	}

	miss, ref := registered.invoke(context.Background(), showReq("deadbeef"))
	if ref != nil {
		t.Fatalf("unknown sig under truncation refused: %v", ref)
	}
	if v, ok := miss.Get("item"); !ok || v != nil {
		t.Fatalf("miss item = %v", v)
	}
	const wantMiss = `{"truncated":true,"truncation_reason":"scan_cap","returned":0,"total":null,"next_offset":null}`
	if got := receiptJSON(t, miss); got != wantMiss {
		t.Fatalf("show miss receipt = %s, want %s", got, wantMiss)
	}
}

// TestNextToolWithoutReceiptStaysBare proves a complete bundle's next result
// carries no receipt keys: legacy callers see the same object as before.
func TestNextToolWithoutReceiptStaysBare(t *testing.T) {
	t.Chdir(t.TempDir())
	dir := ".verdict/state/5101"
	writeFile(t, dir+"/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	writeFile(t, dir+"/findings.json", "["+mcpOpenFinding+"]")
	registered := findTool(t, "next")
	req := &core.NextRequest{}
	req.ReadRequest = core.ReadRequest{Repo: "acme/app", Pr: "5101"}
	data, ref := registered.invoke(context.Background(), req)
	if ref != nil {
		t.Fatalf("next invoke refusal: %v", ref)
	}
	if _, ok := data.Get("truncated"); ok {
		t.Fatalf("untruncated next grew a receipt: %s", strings.Join(data.Keys, ","))
	}
}
