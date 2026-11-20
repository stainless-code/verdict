package postcore

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/reviewdoc"
	"github.com/stainless-code/verdict/internal/jq"
)

func TestPanelSummaryRenderedInLeadBlock(t *testing.T) {
	t.Parallel()
	render := func(sum *core.PanelSummary) string {
		return reviewdoc.Render(&reviewdoc.RenderInput{
			Review:       jq.NewObj("summary", "Details.", "findings", []any{}),
			Approval:     &core.ApprovalReceipt{Approved: false, Reason: "open findings"},
			Verification: sum,
		})
	}
	got := render(&core.PanelSummary{Confirmed: 1, Contested: 1, Single: 2})
	if !strings.Contains(got, "> ℹ️ 4 findings · 1 contested by a verifier\n") {
		t.Fatalf("missing panel summary line:\n%s", got)
	}
	if legacy := render(nil); strings.Contains(legacy, "contested by a verifier") {
		t.Fatalf("nil panel summary emitted line:\n%s", legacy)
	}
	if single := render(&core.PanelSummary{Single: 4}); strings.Contains(single, "contested by a verifier") {
		t.Fatalf("single-only panel summary emitted line:\n%s", single)
	}
}
