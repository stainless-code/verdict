package postcore

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

func TestWithheldOpenStillBlocksApproval(t *testing.T) {
	e := jq.NewObj("path", "a.go", "status", "open", "withheld", jq.NewObj("source", "below-severity", "reason", "severity informational below floor important", "at", "2026-11-13T12:00:00Z"))
	st := policyTestState([]any{e}, nil)
	st.flags.Approve = approveTrue
	if ref := st.approveGate(); ref == nil || ref.Code != "approve-blocked" {
		t.Fatalf("gate = %v; want approve-blocked", ref)
	}
	st.flags.Force = true
	if ref := st.approveGate(); ref != nil {
		t.Fatalf("force gate = %v", ref)
	}
}

func TestWithheldSummaryCountsNullSeverity(t *testing.T) {
	st := policyTestState([]any{
		jq.NewObj("severity", "informational", "withheld", jq.NewObj("source", "below-severity", "reason", "below floor", "at", "2026-11-13T12:00:00Z")),
		jq.NewObj("severity", nil),
	}, nil)
	st.flags.MinSeverity = "important"
	got := st.withholdingSummary()
	if got == nil || got.Total != 1 || got.ByReason["below-severity"] != 1 || got.Unlabelled != 1 {
		t.Fatalf("summary = %+v", got)
	}
}

func TestWithheldFindingAbsentFromPayload(t *testing.T) {
	e := jq.NewObj("sig", "abc", "withheld", jq.NewObj("source", "agent", "reason", "An intentional compatibility exception.", "at", "2026-11-13T12:00:00Z"))
	st := policyTestState([]any{e}, nil)
	st.flags.DryRun = true
	st.body = "Review complete."
	st.merged.Kept = []any{jq.NewObj("path", "a.go", "line", 2, "body", "PRIVATE FINDING")}
	st.merged.KeptSigs = []string{"abc"}
	st.buildPayloads()
	if strings.Contains(st.payload, "PRIVATE FINDING") {
		t.Fatalf("payload leaked finding: %s", st.payload)
	}
	var payload struct {
		Comments []any `json:"comments"`
	}
	if err := json.Unmarshal([]byte(st.payload), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Comments) != 0 {
		t.Fatalf("payload = %s; want no inline comments", st.payload)
	}
}
