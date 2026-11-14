package risk_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/risk"
	"github.com/stainless-code/verdict/internal/jq"
)

func entry(status, severity, kind string, anchored bool) any {
	return jq.NewObj(
		"status", status,
		"severity", severity,
		"kind", kind,
		"anchored", anchored,
	)
}

type floorCase struct {
	wantUnrev   *int
	name        string
	wantFloor   string
	entries     []any
	wantReasons []string
	cov         risk.CoverageInput
	stale       bool
}

func TestComputeFloor(t *testing.T) {
	t.Parallel()
	knownZero := risk.CoverageInput{Known: true, UnreviewedFiles: 0}
	knownGap := risk.CoverageInput{Known: true, UnreviewedFiles: 2}
	unknownCov := risk.CoverageInput{Known: false}

	tests := []floorCase{
		{
			name:      "clean-fresh-complete",
			entries:   []any{entry("addressed", "critical", "inline", true)},
			cov:       knownZero,
			wantFloor: "low",
			wantUnrev: new(0),
		},
		{
			name:        "missing-coverage-bumps-to-medium",
			cov:         unknownCov,
			wantFloor:   "medium",
			wantReasons: []string{"missing-coverage"},
		},
		{
			name:        "unreviewed-files-bumps-to-medium",
			cov:         knownGap,
			wantFloor:   "medium",
			wantReasons: []string{"unreviewed-files"},
			wantUnrev:   new(2),
		},
		{
			name:        "stale-head-bumps-to-medium",
			cov:         knownZero,
			stale:       true,
			wantFloor:   "medium",
			wantReasons: []string{"stale-head"},
			wantUnrev:   new(0),
		},
		{
			name:      "deferred-finding-bumps-to-medium",
			entries:   []any{entry("deferred", "informational", "inline", true)},
			cov:       knownZero,
			wantFloor: "medium",
			wantUnrev: new(0),
		},
		{
			name:      "unanchored-inline-bumps-to-medium",
			entries:   []any{entry("open", "informational", "inline", false)},
			cov:       knownZero,
			wantFloor: "medium",
			wantUnrev: new(0),
		},
		{
			name:      "open-important-bumps-to-high",
			entries:   []any{entry("open", "important", "inline", true)},
			cov:       knownZero,
			wantFloor: "high",
			wantUnrev: new(0),
		},
		{
			name:      "open-critical-bumps-to-critical",
			entries:   []any{entry("open", "critical", "inline", true), entry("open", "important", "section", false)},
			cov:       knownZero,
			wantFloor: "critical",
			wantUnrev: new(0),
		},
	}

	for i := range tests {
		tc := tests[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertFloorCase(t, &tc)
		})
	}
}

func assertFloorCase(t *testing.T, tc *floorCase) {
	t.Helper()
	floor, inputs, reasons := risk.ComputeFloor(tc.entries, tc.cov, tc.stale)
	if floor != tc.wantFloor {
		t.Errorf("floor = %q, want %q", floor, tc.wantFloor)
	}
	if !slices.Equal(reasons, tc.wantReasons) {
		t.Errorf("reasons = %v, want %v", reasons, tc.wantReasons)
	}
	if (inputs.UnreviewedFiles == nil) != (tc.wantUnrev == nil) {
		t.Errorf("UnreviewedFiles nil mismatch: got %v, want %v", inputs.UnreviewedFiles, tc.wantUnrev)
		return
	}
	if tc.wantUnrev != nil && *inputs.UnreviewedFiles != *tc.wantUnrev {
		t.Errorf("UnreviewedFiles = %d, want %d", *inputs.UnreviewedFiles, *tc.wantUnrev)
	}
}

type evalCase struct {
	claim        *core.RiskClaim
	name         string
	maxRisk      string
	wantClaimed  string
	wantFloor    string
	wantReported string
	wantReason   string
	entries      []any
	cov          risk.CoverageInput
	wantExceeded bool
}

func TestEvaluateClaimFloorAndMaxRisk(t *testing.T) {
	t.Parallel()
	const headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const headB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	cleanCov := risk.CoverageInput{Known: true, UnreviewedFiles: 0}

	tests := []evalCase{
		{
			name:         "unclaimed-clean-stays-unknown-never-low",
			cov:          cleanCov,
			wantClaimed:  "unknown",
			wantFloor:    "low",
			wantReported: "unknown",
		},
		{
			name:         "unclaimed-with-missing-cov-reports-medium",
			cov:          risk.CoverageInput{Known: false},
			wantClaimed:  "unknown",
			wantFloor:    "medium",
			wantReported: "medium",
		},
		{
			name:         "unclaimed-with-open-critical-reports-critical",
			entries:      []any{entry("open", "critical", "inline", true)},
			cov:          cleanCov,
			wantClaimed:  "unknown",
			wantFloor:    "critical",
			wantReported: "critical",
		},
		{
			name:         "low-claim-overridden-by-open-critical",
			claim:        &core.RiskClaim{Level: "low", Rationale: "Small change.", AssessedHead: headA},
			entries:      []any{entry("open", "critical", "inline", true)},
			cov:          cleanCov,
			maxRisk:      "medium",
			wantClaimed:  "low",
			wantFloor:    "critical",
			wantReported: "critical",
			wantExceeded: true,
			wantReason:   "risk-exceeded",
		},
		{
			name:         "low-claim-with-stale-head-raised-to-medium",
			claim:        &core.RiskClaim{Level: "low", Rationale: "Assessed earlier.", AssessedHead: headB},
			cov:          cleanCov,
			maxRisk:      "low",
			wantClaimed:  "low",
			wantFloor:    "medium",
			wantReported: "medium",
			wantExceeded: true,
			wantReason:   "risk-exceeded",
		},
		{
			name:         "high-claim-preserved-above-low-floor",
			claim:        &core.RiskClaim{Level: "high", Rationale: "Touches auth invariants.", AssessedHead: headA},
			cov:          cleanCov,
			maxRisk:      "high",
			wantClaimed:  "high",
			wantFloor:    "low",
			wantReported: "high",
			wantExceeded: false,
		},
		{
			name:         "unknown-reported-fails-closed-under-max-risk",
			cov:          cleanCov,
			maxRisk:      "critical",
			wantClaimed:  "unknown",
			wantFloor:    "low",
			wantReported: "unknown",
			wantExceeded: true,
			wantReason:   "risk-unknown",
		},
	}

	for i := range tests {
		tc := tests[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertEvalCase(t, headA, &tc)
		})
	}
}

func assertEvalCase(t *testing.T, headA string, tc *evalCase) {
	t.Helper()
	got := risk.Evaluate(tc.claim, headA, tc.entries, tc.cov, tc.maxRisk)
	if got.Claimed != tc.wantClaimed || got.Floor != tc.wantFloor || got.Reported != tc.wantReported {
		t.Errorf("got (%s, %s, %s), want (%s, %s, %s)",
			got.Claimed, got.Floor, got.Reported, tc.wantClaimed, tc.wantFloor, tc.wantReported)
	}
	if got.Exceeded != tc.wantExceeded {
		t.Errorf("Exceeded = %v, want %v", got.Exceeded, tc.wantExceeded)
	}
	gotReason := ""
	if got.GateReason != nil {
		gotReason = *got.GateReason
	}
	if gotReason != tc.wantReason {
		t.Errorf("GateReason = %q, want %q", gotReason, tc.wantReason)
	}
}

func writeTestReceipt(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEvaluateBundleReceiptsAndBands(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	// Bundle with no receipt has missing-coverage floor medium -> reported medium.
	if band := risk.BundleBand(dir, headA, nil); band != "medium" {
		t.Fatalf("empty bundle without coverage band = %q, want medium", band)
	}
	// Unclaimed bundle with complete coverage and low floor stays unknown (never low).
	cleanDir := t.TempDir()
	writeTestReceipt(t, cleanDir, "record.json", `{"head":"`+headA+`","coverage":{"gaps":0}}`)
	if band := risk.BundleBand(cleanDir, headA, nil); band != "unknown" {
		t.Fatalf("unclaimed clean bundle with complete coverage = %q, want unknown", band)
	}
	if band := risk.BundleBand(cleanDir, headA, []any{entry("open", "critical", "inline", true)}); band != "critical" {
		t.Fatalf("unclaimed bundle with open critical = %q, want critical", band)
	}

	writeTestReceipt(t, dir, "record.json", `{"head":"`+headA+`","coverage":{"gaps":0},"risk":{"level":"low","rationale":"Initial record pass.","assessed_head":"`+headA+`"}}`)
	writeTestReceipt(t, dir, "post.json", `{"head":"`+headA+`","coverage":{"gaps":1},"risk":{"level":"high","rationale":"Post pass found gap.","assessed_head":"`+headA+`"}}`)
	res := risk.EvaluateBundle(dir, headA, headA, nil, "")
	if res.Claimed != "high" || res.Reported != "high" || !slices.Equal(res.ReceiptReasons, []string{"unreviewed-files"}) {
		t.Fatalf("EvaluateBundle = %+v", res)
	}
	if band := risk.BundleBand(dir, headA, []any{entry("open", "critical", "inline", true)}); band != "critical" {
		t.Fatalf("BundleBand with open critical = %q, want critical", band)
	}
}

func TestBundleBandCountsWithheldFindings(t *testing.T) {
	t.Parallel()
	const headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	withheld := jq.NewObj(
		"status", "open",
		"severity", "critical",
		"kind", "inline",
		"anchored", true,
		"withheld", jq.NewObj("source", "agent", "reason", "suppressed", "at", "2026-11-13T13:24:03Z"),
	)
	dir := t.TempDir()
	writeTestReceipt(t, dir, "record.json", `{"head":"`+headA+`","coverage":{"gaps":0},"risk":{"level":"low","rationale":"Low claim.","assessed_head":"`+headA+`"}}`)
	if band := risk.BundleBand(dir, headA, []any{withheld}); band != "critical" {
		t.Fatalf("BundleBand with withheld open critical = %q, want critical", band)
	}
}

func TestReadReceiptPrefersMatchingCoverageKeyClaim(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const headOld = "1111111111111111111111111111111111111111"
	const headNew = "2222222222222222222222222222222222222222"

	oldPost := `{"head":"` + headOld + `","coverage":{"gaps":0},"risk":{"level":"low","rationale":"Stale post claim.","assessed_head":"` + headOld + `"}}`
	newRecord := `{"head":"` + headNew + `","coverage":{"gaps":0},"risk":{"level":"high","rationale":"Fresh record claim.","assessed_head":"` + headNew + `"}}`
	if err := os.WriteFile(filepath.Join(dir, "post.json"), []byte(oldPost), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "record.json"), []byte(newRecord), 0o644); err != nil {
		t.Fatal(err)
	}

	res := risk.EvaluateBundle(dir, headNew, headNew, nil, "")
	if res.Claimed != "high" || res.Reported != "high" || res.Inputs.StaleHead {
		t.Fatalf("EvaluateBundle with stale post.json and fresh record.json = %+v (stale_head=%v), want claimed=high reported=high stale_head=false",
			res, res.Inputs.StaleHead)
	}

	// Fallback still surfaces a stale claim when no receipt matches the current head.
	const headUnrecorded = "3333333333333333333333333333333333333333"
	fallback := risk.EvaluateBundle(dir, headUnrecorded, headUnrecorded, nil, "")
	if fallback.Claimed != "low" || !fallback.Inputs.StaleHead {
		t.Fatalf("EvaluateBundle fallback = %+v, want claimed=low stale_head=true", fallback)
	}
}

func TestDirtyWorktreeDigestMismatchMarksCoverageMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const oldSHA = "99fc09c4a541b225b9ec6a8ca48a31f39d6de4d0875304474f7eaa9a4e97acac"
	writeTestReceipt(t, dir, "source.json", `{"kind":"worktree","dirty":true,"head_sha":null,"base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","diff_sha256":"`+oldSHA+`"}`)
	writeTestReceipt(t, dir, "full.diff", "diff --git a/a.go b/a.go\n+modified\n")
	writeTestReceipt(t, dir, "record.json", `{"head":"dirty","diff_sha256":"`+oldSHA+`","coverage":{"gaps":0},"risk":{"level":"low","rationale":"Clean worktree.","assessed_head":"diff:`+oldSHA+`"}}`)

	covKey := risk.CoverageIdentity(dir, "")
	if covKey != "" {
		t.Fatalf("CoverageIdentity = %q, want empty on digest mismatch", covKey)
	}
	res := risk.EvaluateBundle(dir, "", covKey, nil, "low")
	if !res.Inputs.MissingCoverage || res.Floor != "medium" || res.Reported != "medium" || !res.Exceeded {
		t.Fatalf("EvaluateBundle on mismatched worktree digest = %+v (missing_coverage=%v), want floor=medium reported=medium exceeded=true missing_coverage=true",
			res, res.Inputs.MissingCoverage)
	}
	if !slices.Contains(res.ReceiptReasons, "missing-coverage") {
		t.Fatalf("ReceiptReasons = %v, want missing-coverage", res.ReceiptReasons)
	}
	if band := risk.BundleBand(dir, "", nil); band != "medium" {
		t.Fatalf("BundleBand on mismatched worktree digest = %q, want medium", band)
	}
}
