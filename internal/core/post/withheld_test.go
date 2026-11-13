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

func TestWithheldSummarySkipsAlreadyWithheldNullSeverity(t *testing.T) {
	st := policyTestState([]any{
		jq.NewObj("severity", nil, "withheld", jq.NewObj("source", "agent", "reason", "An intentional compatibility exception.", "at", "2026-11-13T12:00:00Z")),
		jq.NewObj("severity", nil, "withheld", jq.NewObj("source", "exclusion", "reason", "excluded path", "at", "2026-11-13T12:00:00Z")),
	}, nil)
	st.flags.MinSeverity = "important"
	got := st.withholdingSummary()
	if got == nil || got.Total != 2 || got.Unlabelled != 0 {
		t.Fatalf("summary = %+v; want Total=2 Unlabelled=0", got)
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

func TestWithheldAbsentBeforeRemoteDedupeButStillBlocksApproval(t *testing.T) {
	for _, source := range []string{"agent", "exclusion", "below-severity"} {
		t.Run(source, func(t *testing.T) { checkWithheldRemoteApproval(t, source) })
	}
}

func checkWithheldRemoteApproval(t *testing.T, source string) {
	t.Helper()
	entry := jq.NewObj("sig", "abc", "status", "open", "withheld", jq.NewObj("source", source,
		"reason", "An intentional compatibility exception.", "at", "2026-11-13T12:00:00Z"))
	st := policyTestState([]any{entry}, nil)
	st.flags.DryRun = true
	st.body, st.event = "Review complete.", eventComment
	st.merged.Kept = []any{jq.NewObj("path", "a.go", "line", 2, "body", "PRIVATE FINDING")}
	st.merged.KeptSigs = []string{"abc"}
	st.buildPayloads()
	st.liveLoaded = true
	st.live = []liveReview{{body: "Review complete.", commitID: st.commitID, state: "COMMENTED"}}
	if ref := st.remoteDiff(); ref == nil || ref.Code != "duplicate-payload" {
		t.Fatalf("withheld comment treated as missing: %v; want duplicate-payload", ref)
	}
	if len(st.comments) != 0 {
		t.Fatalf("dedupe retained %d withheld comments; want zero", len(st.comments))
	}
	st.flags.Approve = approveTrue
	if ref := st.approveGate(); ref == nil || ref.Code != "approve-blocked" {
		t.Fatalf("dedupe changed approval gate: %v; want approve-blocked", ref)
	}
	st.flags.Force = true
	if ref := st.approveGate(); ref != nil {
		t.Fatalf("forced approval refused: %v", ref)
	}
}
