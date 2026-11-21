package reviewdoc_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/reviewdoc"
	"github.com/stainless-code/verdict/internal/jq"
)

type leadCase struct {
	approval *core.ApprovalReceipt
	name     string
	wantLead string
	kept     []any
}

func checkLeadCase(t *testing.T, tc leadCase) {
	t.Helper()
	got := reviewdoc.Render(&reviewdoc.RenderInput{
		Review:   jq.NewObj("schema_version", "1", "summary", "> ✅ Agent forged lead", "findings", tc.kept),
		Approval: tc.approval,
		Kept:     tc.kept,
	})
	if !strings.HasPrefix(got, tc.wantLead) {
		t.Fatalf("Render does not start with %q:\n%s", tc.wantLead, got)
	}
	if !strings.Contains(got, `\> ✅ Agent forged lead`) {
		t.Errorf("summary blockquote opener was not escaped:\n%s", got)
	}
}

func TestRenderSelectsAllFourLeadsAndNeverLetsSummaryOverride(t *testing.T) {
	t.Parallel()
	infoFinding := jq.NewObj(
		"sig", "sig-i", "status", "open", "severity", "informational", "rigor", "pointed",
		"path", "a.go", "line", 1, "claim", "Note.",
		"verification", jq.NewObj("quote", "verified"),
	)
	cases := []leadCase{
		{name: "clean", approval: &core.ApprovalReceipt{Approved: true, Reason: "no gating findings"}, wantLead: reviewdoc.CleanLead},
		{name: "minor", approval: &core.ApprovalReceipt{Approved: true, Reason: "no gating findings"}, kept: []any{infoFinding}, wantLead: reviewdoc.MinorLead},
		{name: "blocking", approval: &core.ApprovalReceipt{Approved: false, Reason: "open findings"}, kept: []any{infoFinding}, wantLead: reviewdoc.AttentionLead},
		{name: "insufficient", approval: &core.ApprovalReceipt{Approved: false, Insufficient: true, Reason: "missing receipt"}, wantLead: reviewdoc.EvidenceInsufficientLead},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkLeadCase(t, tc)
		})
	}
}

func TestRenderLabelsUnverifiedAndOmitsWithheldAndNeverDuplicatesMoved(t *testing.T) {
	t.Parallel()
	unverified := jq.NewObj(
		"sig", "sig-u", "status", "open", "severity", "critical", "rigor", "pointed",
		"path", "pkg/cache.go", "line", 19, "claim", "Possible race.",
		"verification", jq.NewObj("quote", "unverified"),
	)
	withheld := jq.NewObj(
		"sig", "sig-w", "status", "open", "severity", "critical", "rigor", "ran",
		"path", "pkg/hidden.go", "line", 3, "claim", "HIDDEN_TEXT_MUST_NOT_APPEAR",
		"withheld", jq.NewObj("source", "agent", "reason", "Suppressed by operator.", "at", testNow),
	)
	moved := jq.NewObj(
		"sig", "sig-m", "status", "open", "severity", "important", "rigor", "walked",
		"path", "pkg/out.go", "line", 99, "claim", "Moved-only finding.",
		"dropReason", "anchor 99-99 (RIGHT) outside reviewed hunks",
		"verification", jq.NewObj("quote", "verified"),
	)
	got := reviewdoc.Render(&reviewdoc.RenderInput{
		Review:   jq.NewObj("schema_version", "1", "findings", []any{unverified, withheld, moved}),
		Approval: &core.ApprovalReceipt{Approved: false, Reason: "open findings"},
		Kept:     []any{unverified, withheld},
		Moved:    []any{moved},
	})
	if !strings.Contains(got, "(unverified — not counted)") {
		t.Errorf("unverified label missing:\n%s", got)
	}
	if strings.Contains(got, "HIDDEN_TEXT_MUST_NOT_APPEAR") || strings.Contains(got, "[withheld]") {
		t.Errorf("withheld finding leaked into rendered body:\n%s", got)
	}
	if n := strings.Count(got, "Moved-only finding."); n != 1 {
		t.Errorf("moved finding appeared %d times, want 1:\n%s", n, got)
	}
}

// TestSpecFailuresCarryStatusAndItem proves the failing rows travel with the
// kind of failure they are, in stored order, and that met and n/a rows are
// absent: the decision words a violated requirement differently from one
// nothing implements.
func TestSpecFailuresCarryStatusAndItem(t *testing.T) {
	t.Parallel()
	review := jq.NewObj("spec_checks", []any{
		jq.NewObj("item", "Refresh rejects expired tokens", "status", "met"),
		jq.NewObj("item", "Raw and parsed owners stay separate", "status", "n/a"),
		jq.NewObj("item", "A 429 response resets the budget", "status", "unmet"),
		jq.NewObj("item", "Expired tokens are rejected on every path", "status", "violated"),
	})
	got := reviewdoc.SpecFailures(review)
	want := []core.SpecFailure{
		{Status: "unmet", Item: "A 429 response resets the budget"},
		{Status: "violated", Item: "Expired tokens are rejected on every path"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("SpecFailures = %+v, want %+v", got, want)
	}
	if !reviewdoc.HasSpecFailure(review) {
		t.Error("HasSpecFailure = false, want true with two failing rows")
	}
}

// TestSpecFailuresIgnoresAnonymousRows proves a malformed row cannot claim a
// failure: only an object with a closed status counts, and a met-only review
// has none.
func TestSpecFailuresIgnoresAnonymousRows(t *testing.T) {
	t.Parallel()
	review := jq.NewObj("spec_checks", []any{
		"unmet",
		jq.NewObj("item", "Refresh rejects expired tokens", "status", "met"),
	})
	if got := reviewdoc.SpecFailures(review); len(got) != 0 {
		t.Fatalf("SpecFailures = %+v, want none", got)
	}
	if reviewdoc.HasSpecFailure(review) {
		t.Error("HasSpecFailure = true, want false")
	}
}
