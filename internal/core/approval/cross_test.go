package approval

import (
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/jq"
)

func TestCrossStandingWithholdsApproval(t *testing.T) {
	entry := jq.NewObj("sig", "abc", "status", "open", "severity", "important", "verification",
		jq.NewObj("quote", "verified", "cross", jq.NewObj("execution_id", "execution-A", "expected", []any{"finder-2"}, "checks", []any{})))
	decision := Evaluate([]any{entry}, "", "", nil, nil, currentEvidence())
	if decision.Approved || !decision.Insufficient || len(decision.GatingSigs) != 0 {
		t.Fatalf("pending cross check approved or gated: %+v", decision)
	}
	if len(EligibleFindings([]any{entry})) != 0 {
		t.Fatal("compliance admitted pending cross check")
	}
}

func TestInformationalCrossGapDoesNotWithhold(t *testing.T) {
	entry := jq.NewObj("sig", "abc", "status", "open", "severity", "informational", "verification",
		jq.NewObj("quote", "verified", "cross", jq.NewObj("execution_id", "execution-A", "expected", []any{"finder-2"}, "checks", []any{})))
	decision := Evaluate([]any{entry}, "", "", nil, nil, currentEvidence())
	if !decision.Approved || decision.Insufficient {
		t.Fatalf("informational cross gap withheld: %+v", decision)
	}
}

func TestPendingCrossBlockClauseStaysInsufficient(t *testing.T) {
	entry := jq.NewObj("sig", "abc", "status", "open", "severity", "informational", "verification",
		jq.NewObj("quote", "verified", "cross", jq.NewObj("execution_id", "execution-A", "expected", []any{"finder-2"}, "checks", []any{})))
	comp := &config.ReadCompliance{HasPolicy: true, Blockers: []config.BlockingFinding{{Clause: "security-block", Finding: config.FindingView{Sig: "abc"}}}}
	if !UnbackedClauses(comp, []any{entry}) {
		t.Fatal("readiness lost the pending block-clause candidate")
	}
	decision := Evaluate([]any{entry}, "", "", comp, nil, currentEvidence())
	if !decision.Insufficient || decision.Approved {
		t.Fatalf("pending block clause became sufficient: %+v", decision)
	}
}

func TestManualFindingWithoutCrossRetainsEligibility(t *testing.T) {
	entry := jq.NewObj("sig", "manual", "status", "open", "severity", "important", "verification", jq.NewObj("quote", "verified"))
	decision := Evaluate([]any{entry}, "", "", nil, nil, currentEvidence())
	if !CrossEligible(entry) || decision.Insufficient || len(decision.GatingSigs) != 1 || decision.GatingSigs[0] != "manual" {
		t.Fatalf("manual behavior changed: %+v", decision)
	}
}
