package reportcore_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

const samePatchHead = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

const samePatchBase = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

const samePatchID = "9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d9f3c2a1b7d"

// samePatchDiff is the canonical reviewed diff bytes every coherent
// same-patch fixture binds.
const samePatchDiff = "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n" +
	"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"

// samePatchBundle writes a byte-coherent bundle whose fetched head moved by
// rebase but whose patch id matches the completed post receipt stamped at
// the posted head.
func samePatchBundle(t *testing.T, postedPatch string) {
	t.Helper()
	writeFile(t, ".review/5101/full.diff", samePatchDiff)
	sum := sha256.Sum256([]byte(samePatchDiff))
	sha := hex.EncodeToString(sum[:])
	writeFile(t, ".review/5101/identity.json",
		`{"schema_version":"1","patch_id":"`+samePatchID+`","patch_id_kind":"git-patch-id",`+
			`"diff_sha256":"`+sha+`","raw_diff_sha256":"`+sha+`","head_sha":"`+storeHeadSHA+`","base_sha":"`+samePatchBase+`"}`)
	post := `{"schema_version":"1","review":{"id":1,"state":"COMMENTED"},"head":"` + samePatchHead + `",` +
		`"posted_at":"2026-01-01T00:00:00Z","patch_id":"` + postedPatch + `","patch_id_kind":"git-patch-id"}`
	writeFile(t, ".review/5101/post.json", post)
}

// TestRunReportCompletenessSamePatch proves the rebase pass: posted and
// fetched heads differ but the patch ids agree, so the gate passes with
// the rebase line and the data carries the patch comparison.
func TestRunReportCompletenessSamePatch(t *testing.T) {
	req := fixture(t, "[]")
	samePatchBundle(t, samePatchID)
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
	samePatchBundle(t, "0123456789abcdef0123456789abcdef01234567")
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

// assertStaleCompleteness proves the gate fails stale with no same-patch
// relaxation: SamePatch is absent and the stale-head failure stands.
func assertStaleCompleteness(t *testing.T, resp *core.ReportResponse, warn string) {
	t.Helper()
	if resp == nil {
		t.Fatal("missing response")
	}
	if !resp.GateFailed {
		t.Fatalf("GateFailed = false, want the stale-head failure: %q", resp.CheckLines)
	}
	joined := strings.Join(resp.CheckLines, "\n")
	if !strings.Contains(joined, "stale-head") {
		t.Errorf("CheckLines = %q, want the stale-head failure", joined)
	}
	if comp := resp.Data.Completeness; comp == nil || comp.SamePatch != nil {
		t.Errorf("same_patch = %v, want absent", comp)
	}
	if warn != "" && !slices.Contains(resp.Warnings, warn) {
		t.Errorf("warnings = %q, want %q", resp.Warnings, warn)
	}
}

// TestRunReportCompletenessStaleBytes proves an identity that no longer
// binds the bundle bytes never relaxes the gate: the rebase line stays
// off and the SHA comparison fails stale with the legacy warning.
func TestRunReportCompletenessStaleBytes(t *testing.T) {
	req := fixture(t, "[]")
	samePatchBundle(t, samePatchID)
	writeFile(t, ".review/5101/full.diff", "replaced bytes\n")
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Completeness: true},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	assertStaleCompleteness(t, resp, "patch identity unavailable — completeness compares heads")
}

// TestRunReportCompletenessStaleHead proves an identity naming a moved head
// never relaxes the gate.
func TestRunReportCompletenessStaleHead(t *testing.T) {
	req := fixture(t, "[]")
	samePatchBundle(t, samePatchID)
	raw := readFile(t, ".review/5101/identity.json")
	writeFile(t, ".review/5101/identity.json",
		strings.Replace(string(raw), `"head_sha":"`+storeHeadSHA+`"`, `"head_sha":"cccccccccccccccccccccccccccccccccccccccc"`, 1))
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Completeness: true},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	assertStaleCompleteness(t, resp, "patch identity unavailable — completeness compares heads")
}

// TestRunReportCompletenessIncompleteReceipt proves a two-field post stub
// proves no publication: same_patch stays absent and the pre-existing
// incomplete-post gate stands (no head recorded), with the verified
// reviewed id still reported.
func TestRunReportCompletenessIncompleteReceipt(t *testing.T) {
	req := fixture(t, "[]")
	samePatchBundle(t, samePatchID)
	writeFile(t, ".review/5101/post.json",
		`{"patch_id":"`+samePatchID+`","patch_id_kind":"git-patch-id"}`)
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Completeness: true},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !resp.GateFailed {
		t.Fatalf("GateFailed = false, want the incomplete-post failure: %q", resp.CheckLines)
	}
	assertIncompletePostGate(t, resp)
}

// assertIncompletePostGate proves the pre-existing incomplete-post gate
// stands with no same-patch relaxation: same_patch and posted_patch_id
// absent, the verified reviewed id still reported.
func assertIncompletePostGate(t *testing.T, resp *core.ReportResponse) {
	t.Helper()
	joined := strings.Join(resp.CheckLines, "\n")
	if !strings.Contains(joined, "check: incomplete — post record carries no head SHA") {
		t.Errorf("CheckLines = %q, want the incomplete-post failure", joined)
	}
	comp := resp.Data.Completeness
	if comp == nil || comp.SamePatch != nil {
		t.Errorf("same_patch = %v, want absent", comp)
	}
	if comp == nil || comp.ReviewedPatchID == nil || *comp.ReviewedPatchID != samePatchID {
		t.Errorf("reviewed_patch_id = %v, want the verified reviewed id", comp)
	}
	if comp == nil || comp.PostedPatchID != nil {
		t.Errorf("posted_patch_id = %v, want absent", comp)
	}
}

// TestRunReportCompletenessPendingReceipt proves a pending or dry-run post
// receipt proves no publication.
func TestRunReportCompletenessPendingReceipt(t *testing.T) {
	for name, review := range map[string]string{
		"pending": `{"schema_version":"1","review":{"id":1,"state":"PENDING"},"head":"` + samePatchHead + `",` +
			`"posted_at":"2026-01-01T00:00:00Z","patch_id":"` + samePatchID + `","patch_id_kind":"git-patch-id"}`,
		"dry-run": `{"schema_version":"1","review":{"id":1,"state":"COMMENTED"},"dry_run":true,"head":"` + samePatchHead + `",` +
			`"posted_at":"2026-01-01T00:00:00Z","patch_id":"` + samePatchID + `","patch_id_kind":"git-patch-id"}`,
	} {
		t.Run(name, func(t *testing.T) {
			req := fixture(t, "[]")
			samePatchBundle(t, samePatchID)
			writeFile(t, ".review/5101/post.json", review)
			resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
				ReadRequest: req,
				Flags:       core.ReportFlags{Completeness: true},
			})
			if ref != nil {
				t.Fatalf("Run refusal: %v", ref)
			}
			assertStaleCompleteness(t, resp, "")
		})
	}
}

// TestRunReportCompletenessInvalidIdentity proves an algorithm-invalid
// patch id warns and falls back to the SHA comparison.
func TestRunReportCompletenessInvalidIdentity(t *testing.T) {
	req := fixture(t, "[]")
	samePatchBundle(t, samePatchID)
	raw := readFile(t, ".review/5101/identity.json")
	writeFile(t, ".review/5101/identity.json", strings.Replace(string(raw), samePatchID, "not-a-patch-id", 1))
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Completeness: true},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	assertStaleCompleteness(t, resp, "patch identity unavailable — completeness compares heads")
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
