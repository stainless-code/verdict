package reportcore

import (
	"fmt"
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
	if status := gateEvidenceCheck(st).Status; status != "fail" {
		t.Fatalf("informational evidence gate = %q", status)
	}
}

func pendingCrossEntry(sig, quote string) *jq.Obj {
	return jq.NewObj("sig", sig, "status", "open", "severity", "important", "verification",
		jq.NewObj("quote", quote, "cross", jq.NewObj("execution_id", "execution-A", "expected", []any{"finder-2"}, "checks", []any{})))
}

// TestPendingCrossFailsGateWithoutLiveExecution proves the cross arm reads the
// findings, not the execution object: a nil, finished, or retired execution
// must not hide a finding the captured execution left unresolved.
func TestPendingCrossFailsGateWithoutLiveExecution(t *testing.T) {
	cases := []struct {
		execution *panelcore.Execution
		name      string
	}{
		{name: "nil"},
		{name: "finished", execution: &panelcore.Execution{CrossRequired: true, Gap: "captured execution artifacts missing"}},
		{name: "retired", execution: &panelcore.Execution{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertCrossGateFails(t, tc.execution, 1, pendingCrossEntry("abc", "verified"))
		})
	}
}

// TestPendingCrossCountsEveryQuoteStatus proves an unverified quote does not
// exempt a finding from its assigned cross check, and that a tally names
// itself instead of reading as one finding.
func TestPendingCrossCountsEveryQuoteStatus(t *testing.T) {
	assertCrossGateFails(t, nil, 2, pendingCrossEntry("abc", "verified"), pendingCrossEntry("def", "unverified"))
}

func assertCrossGateFails(t *testing.T, execution *panelcore.Execution, want int, entries ...any) {
	t.Helper()
	st := &state{
		entries: entries, execution: execution,
		evidence: &evidenceState{kind: evidencePresent, receipt: &core.EvidenceReceipt{}},
	}
	st.flags.Check = "important"
	wantDetail := "a finding within the requested tier lacks a confirmed cross check"
	if want > 1 {
		wantDetail = fmt.Sprintf("%d findings within the requested tier lack a confirmed cross check", want)
	}
	dim := gateEvidenceCheck(st)
	if dim.Status != "fail" || dim.Detail != wantDetail || dim.Count == nil || *dim.Count != want {
		t.Fatalf("gate = %s %q count %v, want fail %q count %d", dim.Status, dim.Detail, dim.Count, wantDetail, want)
	}
}
