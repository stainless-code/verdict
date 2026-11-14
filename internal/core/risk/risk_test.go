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

func TestEvaluateBundleReceiptsAndBands(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	// Bundle with no receipt classifies as unknown in trend bands.
	if band := risk.BundleBand(dir, headA, nil); band != "unknown" {
		t.Fatalf("empty bundle band = %q, want unknown", band)
	}

	// Write record.json with low claim and 0 coverage gaps; post.json overrides claim to medium.
	recJSON := `{"head":"` + headA + `","coverage":{"gaps":0},"risk":{"level":"low","rationale":"Initial record pass.","assessed_head":"` + headA + `"}}`
	postJSON := `{"head":"` + headA + `","coverage":{"gaps":1},"risk":{"level":"high","rationale":"Post pass found gap.","assessed_head":"` + headA + `"}}`
	if err := os.WriteFile(filepath.Join(dir, "record.json"), []byte(recJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "post.json"), []byte(postJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	res := risk.EvaluateBundle(dir, headA, headA, nil, "")
	if res.Claimed != "high" || res.Reported != "high" || !slices.Equal(res.ReceiptReasons, []string{"unreviewed-files"}) {
		t.Fatalf("EvaluateBundle = %+v", res)
	}
	if band := risk.BundleBand(dir, headA, []any{entry("open", "critical", "inline", true)}); band != "critical" {
		t.Fatalf("BundleBand with open critical = %q, want critical", band)
	}
	withheld := jq.NewObj(
		"status", "open",
		"severity", "critical",
		"kind", "inline",
		"anchored", true,
		"withheld", jq.NewObj("source", "agent", "reason", "suppressed", "at", "2026-11-13T13:24:03Z"),
	)
	recLow := `{"head":"` + headA + `","coverage":{"gaps":0},"risk":{"level":"low","rationale":"Low claim.","assessed_head":"` + headA + `"}}`
	dirWithheld := t.TempDir()
	if err := os.WriteFile(filepath.Join(dirWithheld, "record.json"), []byte(recLow), 0o644); err != nil {
		t.Fatal(err)
	}
	if band := risk.BundleBand(dirWithheld, headA, []any{withheld}); band != "critical" {
		t.Fatalf("BundleBand with withheld open critical = %q, want critical", band)
	}
}
