package reportcore_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

// writeCompliancePolicy writes a real resolved capture with one critical
// security block clause and the head-current required check.
func writeCompliancePolicy(t *testing.T) {
	t.Helper()
	writeCompliancePolicyWith(t, &core.BlockClause{
		Name: "security-critical", Threshold: "critical", Categories: []string{"security"},
	})
}

// writeCompliancePolicyWith writes a real resolved capture carrying clause and
// the head-current required check.
func writeCompliancePolicyWith(t *testing.T, clause *core.BlockClause) {
	t.Helper()
	repo := &core.Config{
		SchemaVersion: "1",
		Requirements: &core.ConfigRequirements{
			BlockClauses:  []core.BlockClause{*clause},
			RequireChecks: []string{"head-current"},
		},
	}
	resolved, ref := config.Resolve(&config.Inputs{
		Repo:  repo,
		Files: []config.FileInput{{Path: "notes.txt"}},
	})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	rs := &config.RunState{
		BaseSHA:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Trusted:  true,
		Resolved: resolved,
	}
	dir := filepath.Join(".", ".verdict", "state", "5101")
	if serr := config.WriteRunState(dir, rs); serr != nil {
		t.Fatalf("WriteRunState: %v", serr)
	}
	if _, cref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: dir, Root: ".", BaseSHA: rs.BaseSHA, AllowHead: true,
	}); cref != nil {
		t.Fatalf("controls.Resolve: %v", cref)
	}
}

// securityFinding is an open critical security finding on the clause path,
// carrying the verified quote standing the evidence pass stamps.
const securityFinding = `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
	`"anchored":true,"body":"Body.","suggestion":null,"inBounds":true,"rigor":"walked",` +
	`"severity":"critical","category":"security","sig":"c111111111111111111111111111111111111111","sig_version":"1",` +
	`"verification":{"quote":"verified"},` +
	`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}`

// TestReportComplianceBlocksOnFullFindings proves report evaluates captured
// compliance over the unfiltered stored set even when no display floor or
// check flag is set, and the typed gate dimension fails.
func TestReportComplianceBlocksOnFullFindings(t *testing.T) {
	req := fixture(t, "["+securityFinding+"]")
	writeCompliancePolicy(t)
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Check: "critical"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.Data.Compliance == nil {
		t.Fatal("report data omits captured compliance")
	}
	got := resp.Data.Compliance
	if !got.Policy || got.Blockers != 1 || got.Satisfied {
		t.Fatalf("compliance = %+v, want one blocker and unsatisfied", got)
	}
	requireComplianceStatus(t, resp, "fail")
}

// TestReportComplianceIgnoresUnverifiedFinding proves an unverified finding
// matching a captured block clause is filtered before EvaluateReadCompliance
// and never enters compliance blockers.
func TestReportComplianceIgnoresUnverifiedFinding(t *testing.T) {
	const unverifiedSecurity = `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
		`"anchored":true,"body":"Body.","suggestion":null,"inBounds":true,"rigor":"walked",` +
		`"severity":"critical","category":"security","sig":"c111111111111111111111111111111111111111","sig_version":"1",` +
		`"verification":{"quote":"unverified"},` +
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}`
	req := fixture(t, "["+unverifiedSecurity+"]")
	writeCompliancePolicy(t)
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Check: "critical"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.Data.Compliance == nil {
		t.Fatal("report data omits captured compliance")
	}
	if resp.Data.Compliance.Blockers != 0 {
		t.Fatalf("compliance blockers = %d, want 0 for unverified finding", resp.Data.Compliance.Blockers)
	}
}

// TestReportUnverifiedClauseNeverApproves proves R1 on the report side with a
// clause that only an unverified finding matches: the compliance dimension
// keeps the eligible view and shows no blocker, while the approval decision
// reads the unfiltered store and refuses.
func TestReportUnverifiedClauseNeverApproves(t *testing.T) {
	const unverifiedInfo = `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
		`"anchored":true,"body":"Body.","suggestion":null,"inBounds":true,"rigor":"walked",` +
		`"severity":"informational","sig":"b222222222222222222222222222222222222222","sig_version":"1",` +
		`"verification":{"quote":"unverified"},` +
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}`
	req := fixture(t, "["+unverifiedInfo+"]")
	writeCompliancePolicyWith(t, &core.BlockClause{Name: "any-block", Threshold: "informational"})
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Check: "critical", Live: "skip"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.Data.Compliance == nil || resp.Data.Compliance.Blockers != 0 {
		t.Fatalf("compliance = %+v, want an eligible view with no blocker", resp.Data.Compliance)
	}
	requireClauseRefusal(t, resp.Data.Approval)
}

// requireClauseRefusal proves the approval decision refused the clause it could
// not gate: withheld, insufficient, with no sig and no clause attached.
func requireClauseRefusal(t *testing.T, got *core.ApprovalReceipt) {
	t.Helper()
	if got == nil {
		t.Fatal("report data omits the approval decision")
	}
	if got.Approved || !got.Insufficient {
		t.Fatalf("approval = %+v, want Approved=false Insufficient=true", got)
	}
	if len(got.GatingSigs) != 0 || len(got.Clauses) != 0 {
		t.Fatalf("approval = %+v, want no gating sigs and no clauses", got)
	}
}

// requireComplianceStatus asserts the gate failed and the compliance
// dimension carries the expected status.
func requireComplianceStatus(t *testing.T, resp *reportcore.Response, want string) {
	t.Helper()
	if !resp.GateFailed {
		t.Fatal("gate passed while captured requirements were unmet")
	}
	for _, dim := range resp.Data.Gate.Dimensions {
		if dim.ID == "compliance" {
			if dim.Status != want {
				t.Fatalf("compliance dimension = %q, want %q", dim.Status, want)
			}
			return
		}
	}
	t.Fatalf("gate dimensions = %+v, want a compliance dimension", resp.Data.Gate.Dimensions)
}

// TestReportCompliancePendingCheckIsUnknownNotPass proves a required check
// with no observation for the saved head reports unknown, never satisfied,
// and the gate fails rather than passing.
func TestReportCompliancePendingCheckIsUnknownNotPass(t *testing.T) {
	req := fixture(t, "[]")
	writeCompliancePolicy(t)
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Check: "critical"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	got := resp.Data.Compliance
	if got == nil || got.Pending != 1 || got.Satisfied {
		t.Fatalf("compliance = %+v, want one pending check and unsatisfied", got)
	}
	requireComplianceStatus(t, resp, "unknown")
}

// TestReportLegacyBundleInventsNoCompliance proves a bundle with no typed
// policy adds no compliance view or dimension: absence invents nothing.
func TestReportLegacyBundleInventsNoCompliance(t *testing.T) {
	req := fixture(t, "["+securityFinding+"]")
	resp, ref := reportcore.Run(context.Background(), &reportcore.Request{
		ReadRequest: req,
		Flags:       reportcore.Flags{Check: "critical"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.Data.Compliance != nil {
		t.Fatalf("legacy bundle invented compliance: %+v", resp.Data.Compliance)
	}
	for _, dim := range resp.Data.Gate.Dimensions {
		if dim.ID == "compliance" {
			t.Fatalf("legacy bundle invented a compliance dimension: %+v", dim)
		}
	}
}
