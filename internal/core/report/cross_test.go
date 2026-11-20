package reportcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	panelcore "github.com/stainless-code/verdict/internal/core/panel"
	"github.com/stainless-code/verdict/internal/jq"
)

func TestTierGateExcludesPendingCross(t *testing.T) {
	entry := jq.NewObj("sig", "abc", "status", "open", "severity", "important", "verification",
		jq.NewObj("quote", "verified", "cross", jq.NewObj("execution_id", "execution-A", "expected", []any{"finder-2"}, "checks", []any{})))
	if tierHit(entry, 2, "") {
		t.Fatal("pending cross check counted as a confirmed blocker")
	}
}

func TestInformationalGateRequiresCrossEvidence(t *testing.T) {
	entry := jq.NewObj("sig", "abc", "status", "open", "severity", "informational", "verification",
		jq.NewObj("quote", "verified", "cross", jq.NewObj("execution_id", "execution-A", "expected", []any{"finder-2"}, "checks", []any{})))
	st := &state{
		entries: []any{entry}, execution: &panelcore.Execution{Active: true},
		evidence: &evidenceState{kind: evidencePresent, receipt: &core.EvidenceReceipt{}},
	}
	st.flags.Check = "informational"
	if status := gateEvidenceCheck(st).dim.Status; status != "fail" {
		t.Fatalf("informational evidence gate = %q", status)
	}
}
