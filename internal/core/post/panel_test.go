package postcore

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

func TestPanelBodySummary(t *testing.T) {
	body := "> [!IMPORTANT]\n> Review finding\n\nDetails."
	got := panelBody(body, &core.VerificationSummary{Confirmed: 1, Contested: 1, Single: 2})
	want := "> [!IMPORTANT]\n> ℹ️ 4 findings · 1 contested by a verifier\n> Review finding\n\nDetails."
	if got != want {
		t.Fatalf("body=%q, want %q", got, want)
	}
	if strings.Count(got, "contested by a verifier") != 1 {
		t.Fatal("repeated panel summary")
	}
	if got := panelBody(body, nil); got != body {
		t.Fatal("legacy body changed")
	}
	if got := panelBody(body, &core.VerificationSummary{Single: 4}); got != body {
		t.Fatal("single-only body changed")
	}
}
