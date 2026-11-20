package recordcore

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/reviewdoc"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func TestFloorWithholdsAndWarnsForNull(t *testing.T) {
	info := jq.NewObj("sig", "info", "path", "a.go", "severity", "informational", "status", "open", "history", []any{})
	unlabelled := jq.NewObj("sig", "unknown", "path", "b.go", "severity", nil, "status", "open", "history", []any{})
	st := &state{cmd: "post", minSeverity: "important", entries: []any{info, unlabelled}, now: "2026-11-13T12:00:00Z"}
	st.applyPostFloor()
	if readcommon.WithheldSource(info) != "below-severity" || suppressionString(info, "status") != "open" {
		t.Fatalf("info = %s", info.MarshalCompact())
	}
	if unlabelled.Has("withheld") {
		t.Fatal("null severity was withheld")
	}
	if strings.Join(st.warnings, "\n") != "1 finding(s) carry no severity; the floor does not apply" {
		t.Fatalf("warnings = %q", st.warnings)
	}
	st.minSeverity = "off"
	st.applyPostFloor()
	if info.Has("withheld") {
		t.Fatal("off did not clear below-severity withholding")
	}
}

func TestFloorWithheldFindingsOmittedFromRenderedBody(t *testing.T) {
	low := jq.NewObj("sig", "low", "path", "a.go", "line", 1, "severity", "informational", "status", "open", "body", "PRIVATE_LOW_CLAIM",
		"withheld", jq.NewObj("source", "below-severity", "reason", "below floor", "at", "2026-11-13T12:00:00Z"))
	high := jq.NewObj("sig", "high", "path", "b.go", "line", 2, "severity", "important", "status", "open", "body", "PUBLIC_HIGH_CLAIM",
		"verification", jq.NewObj("quote", "verified"))
	got := reviewdoc.Render(&reviewdoc.RenderInput{
		Review:   jq.NewObj("findings", []any{low, high}),
		Approval: &core.ApprovalReceipt{Approved: false, Reason: "open findings"},
		Kept:     []any{low, high},
	})
	if strings.Contains(got, "PRIVATE_LOW_CLAIM") || strings.Contains(got, "[withheld]") {
		t.Fatalf("withheld finding leaked into rendered body:\n%s", got)
	}
	if !strings.Contains(got, "PUBLIC_HIGH_CLAIM") {
		t.Fatalf("visible finding missing from rendered body:\n%s", got)
	}
}

func TestFloorDoesNotWarnForAlreadyWithheldNullSeverity(t *testing.T) {
	agentWithheld := jq.NewObj("sig", "a1", "path", "a.go", "severity", nil, "status", "open",
		"withheld", jq.NewObj("source", "agent", "reason", "An intentional compatibility exception.", "at", "2026-11-13T12:00:00Z"))
	exclWithheld := jq.NewObj("sig", "a2", "path", "vendor/a.go", "severity", nil, "status", "open",
		"withheld", jq.NewObj("source", "exclusion", "reason", "excluded path", "at", "2026-11-13T12:00:00Z"))
	st := &state{cmd: "post", minSeverity: "important", entries: []any{agentWithheld, exclWithheld}, now: "2026-11-13T12:00:00Z"}
	st.applyPostFloor()
	if len(st.warnings) != 0 {
		t.Fatalf("warnings = %q; want none for already-withheld unlabelled findings", st.warnings)
	}
}
