package reviewdoc_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/reviewdoc"
	"github.com/stainless-code/verdict/internal/golden"
	"github.com/stainless-code/verdict/internal/jq"
)

func TestRenderFinderGoldens(t *testing.T) {
	cases := []struct {
		input *reviewdoc.RenderInput
		name  string
	}{
		{input: finderDuplicate(), name: "finders-duplicate"},
		{input: finderDisagreement(), name: "finders-disagreement"},
		{input: finderPartial(), name: "finders-partial"},
		{input: finderClean(), name: "finders-clean"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { golden.Check(t, tc.name, reviewdoc.Render(tc.input)+"\n") })
	}
}

func finderEntry(origins, expected, checks []any) *jq.Obj {
	return jq.NewObj("sig", "literal-signature", "status", "open", "severity", "important", "path", "src/auth.go", "line", 42,
		"body", "Token refresh skips expiration validation.", "verification", jq.NewObj("quote", "verified", "cross",
			jq.NewObj("execution_id", "execution-A", "origins", origins, "expected", expected, "checks", checks)))
}

func finderCheck(checker, verdict string) *jq.Obj {
	return jq.NewObj("checker", checker, "verdict", verdict, "quote_status", "verified", "evidence",
		"The unchanged guard rejects expired tokens before refresh.", "quote", jq.NewObj("path", "src/auth.go", "line", 41, "side", "RIGHT", "text", "if expired { return ErrExpired }"))
}

func finderBody(entry *jq.Obj, counts *core.FinderSummary) *reviewdoc.RenderInput {
	in := cleanRenderInput()
	in.Review = jq.NewObj("summary", "Two-finder review of token refresh.")
	in.Kept = []any{entry}
	in.Entries = []any{entry}
	in.SpecChecks = nil
	in.Verification = &core.PanelSummary{Finders: counts}
	in.Approval = &core.ApprovalReceipt{Tier: "important", Reason: "open findings at-or-above important"}
	return in
}

func finderDuplicate() *reviewdoc.RenderInput {
	entry := finderEntry([]any{"finder-1", "finder-2"}, []any{"finder-1", "finder-2"},
		[]any{finderCheck("finder-1", "confirmed"), finderCheck("finder-2", "confirmed")})
	return finderBody(entry, &core.FinderSummary{Completed: 2, Confirmed: 2})
}

func finderDisagreement() *reviewdoc.RenderInput {
	entry := finderEntry([]any{"finder-1"}, []any{"finder-2"}, []any{finderCheck("finder-2", "refuted")})
	in := finderBody(entry, &core.FinderSummary{Completed: 2, Contested: 1})
	in.Approval.Insufficient = true
	in.Approval.Reason = "blocking cross check contested"
	return in
}

func finderPartial() *reviewdoc.RenderInput {
	entry := finderEntry([]any{"finder-2"}, []any{"finder-1"}, []any{})
	entry.Set("verification", jq.NewObj("quote", "unverified", "cross", jq.NewObj("execution_id", "execution-A",
		"origins", []any{"finder-2"}, "expected", []any{"finder-1"}, "checks", []any{})))
	in := finderBody(entry, &core.FinderSummary{Completed: 1, Pending: 1})
	in.Approval.Insufficient = true
	in.Approval.Reason = "execution incomplete"
	in.Evidence.Insufficient = true
	in.Evidence.Gaps = []string{"execution: finder-1 failed"}
	return in
}

func finderClean() *reviewdoc.RenderInput {
	in := cleanRenderInput()
	in.Verification = &core.PanelSummary{Finders: &core.FinderSummary{Completed: 2}}
	return in
}
