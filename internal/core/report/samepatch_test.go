package reportcore_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

const samePatchHead = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

const samePatchID = "9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d"

// samePatchBundle writes a bundle whose fetched head moved by rebase but
// whose patch id matches the completed post receipt.
func samePatchBundle(t *testing.T, postedHead, reviewedPatch, postedPatch string) {
	t.Helper()
	writeFile(t, ".review/5101/identity.json",
		`{"schema_version":"1","patch_id":"`+reviewedPatch+`","patch_id_kind":"git-patch-id",`+
			`"diff_sha256":"d","raw_diff_sha256":"r","head_sha":"`+storeHeadSHA+`","base_sha":"b"}`)
	post := `{"schema_version":"1","review":{"id":1},"head":"` + postedHead + `",` +
		`"posted_at":"2026-01-01T00:00:00Z","patch_id":"` + postedPatch + `","patch_id_kind":"git-patch-id"}`
	writeFile(t, ".review/5101/post.json", post)
}

// TestRunReportCompletenessSamePatch proves the rebase pass: posted and
// fetched heads differ but the patch ids agree, so the gate passes with
// the rebase line and the data carries the patch comparison.
func TestRunReportCompletenessSamePatch(t *testing.T) {
	req := fixture(t, "[]")
	samePatchBundle(t, samePatchHead, samePatchID, samePatchID)
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Completeness: true},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.GateFailed {
		t.Fatalf("GateFailed = true; same patch should pass: %q", resp.CheckLines)
	}
	joined := strings.Join(resp.CheckLines, "\n")
	if !strings.Contains(joined, "check: same patch reviewed at aaaaaaa (posted bbbbbbb) — completeness passes (rebase/amend)") {
		t.Errorf("CheckLines = %q, want the rebase pass line", joined)
	}
	assertSamePatchData(t, resp.Data.Completeness)
}

// assertSamePatchData proves the data carries the patch comparison: a
// differing-head reviewed=false with the matching reviewed/posted ids.
func assertSamePatchData(t *testing.T, comp *core.ReportCompleteness) {
	t.Helper()
	if comp == nil {
		t.Fatal("data.completeness is nil under --completeness")
	}
	if comp.Reviewed {
		t.Errorf("reviewed = true; heads differ")
	}
	if comp.SamePatch == nil || !*comp.SamePatch {
		t.Errorf("same_patch = %v, want true", comp.SamePatch)
	}
	assertSamePatchIDs(t, comp)
}

// assertSamePatchIDs proves the reviewed and posted patch ids agree.
func assertSamePatchIDs(t *testing.T, comp *core.ReportCompleteness) {
	t.Helper()
	if comp.ReviewedPatchID == nil || *comp.ReviewedPatchID != samePatchID {
		t.Errorf("reviewed_patch_id = %v", comp.ReviewedPatchID)
	}
	if comp.PostedPatchID == nil || *comp.PostedPatchID != samePatchID {
		t.Errorf("posted_patch_id = %v", comp.PostedPatchID)
	}
}

// TestRunReportCompletenessDifferentPatch proves a changed patch still
// fails stale even when both sides stamp patch ids.
func TestRunReportCompletenessDifferentPatch(t *testing.T) {
	req := fixture(t, "[]")
	samePatchBundle(t, samePatchHead, samePatchID, "0123456789abcdef0123456789abcdef0123456789")
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Completeness: true},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !resp.GateFailed {
		t.Fatal("GateFailed = false; a different patch must fail")
	}
	joined := strings.Join(resp.CheckLines, "\n")
	if !strings.Contains(joined, "stale-head") {
		t.Errorf("CheckLines = %q, want the stale-head failure", joined)
	}
	if comp := resp.Data.Completeness; comp == nil || comp.SamePatch == nil || *comp.SamePatch {
		t.Errorf("same_patch = %v, want false", comp)
	}
}

// TestRunReportCompletenessLegacyWarns proves a bundle without identity
// falls back to the SHA comparison and warns.
func TestRunReportCompletenessLegacyWarns(t *testing.T) {
	req := fixture(t, "[]")
	writeFile(t, ".review/5101/post.json",
		`{"schema_version":"1","review":{"id":1},"head":"`+storeHeadSHA+`","posted_at":"2026-01-01T00:00:00Z"}`)
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Completeness: true},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.GateFailed {
		t.Errorf("GateFailed = true; matching heads should pass: %q", resp.CheckLines)
	}
	if !slices.Contains(resp.Warnings, "patch identity unavailable — completeness compares heads") {
		t.Errorf("warnings = %q, want the legacy fallback warning", resp.Warnings)
	}
	if comp := resp.Data.Completeness; comp == nil || comp.SamePatch != nil {
		t.Errorf("same_patch = %v, want absent on legacy bundles", comp)
	}
}
