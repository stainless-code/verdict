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

// TestPendingCrossFailsGateWithoutLiveExecution proves the cross arm reads the
// findings, not the execution object: a nil, finished, or retired execution
// must not hide a finding the captured execution left unresolved.
func TestPendingCrossFailsGateWithoutLiveExecution(t *testing.T) {
	entry := jq.NewObj("sig", "abc", "status", "open", "severity", "important", "verification",
		jq.NewObj("quote", "verified", "cross", jq.NewObj("execution_id", "execution-A", "expected", []any{"finder-2"}, "checks", []any{})))
	cases := []struct {
		execution *panelcore.Execution
		name      string
	}{
		{name: "nil", execution: nil},
		{name: "finished", execution: &panelcore.Execution{CrossRequired: true, Gap: "captured execution artifacts missing"}},
		{name: "retired", execution: &panelcore.Execution{Retired: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertCrossGateFails(t, tc.execution, entry)
		})
	}
}

// assertCrossGateFails decides one pending cross with the given execution in
// hand and requires the gate to fail on it by name.
func assertCrossGateFails(t *testing.T, execution *panelcore.Execution, entry *jq.Obj) {
	t.Helper()
	st := &state{
		entries:   []any{entry},
		execution: execution,
		evidence:  &evidenceState{kind: evidencePresent, receipt: &core.EvidenceReceipt{}},
	}
	st.flags.Check = "important"
	check := gateEvidenceCheck(st)
	const wantLine = "check: a finding within the requested tier lacks a confirmed cross check — gate fails"
	if check.dim.Status != "fail" || check.line != wantLine {
		t.Fatalf("gate = %q / %q, want fail with %q", check.dim.Status, check.line, wantLine)
	}
}
