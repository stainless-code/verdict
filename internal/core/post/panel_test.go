package postcore

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestPanelBodySummary pins the single evidence-free callout.
func TestPanelBodySummary(t *testing.T) {
	body := "> [!IMPORTANT]\n> Review finding\n\nDetails."
	got := panelBody(body, &core.PanelSummary{Confirmed: 1, Contested: 1, Single: 2})
	want := "> [!IMPORTANT]\n> ℹ️ 4 findings · 1 contested by a verifier\n> Review finding\n\nDetails."
	if got != want {
		t.Fatalf("body=%q, want %q", got, want)
	}
	if replay := panelBody(got, &core.PanelSummary{Confirmed: 1, Contested: 1, Single: 2}); replay != want {
		t.Fatalf("replayed body=%q, want %q", replay, want)
	}
	if got := panelBody(body, nil); got != body {
		t.Fatal("legacy body changed")
	}
	if got := panelBody(body, &core.PanelSummary{Single: 4}); got != body {
		t.Fatal("single-only body changed")
	}
}

func TestPanelSummaryReplayPlainBody(t *testing.T) {
	sum := &core.PanelSummary{Confirmed: 1, Contested: 1}
	first := panelBody("Details.", sum)
	want := "> ℹ️ 2 findings · 1 contested by a verifier\n\nDetails."
	if got := panelBody(first, sum); got != want {
		t.Fatalf("replayed plain body=%q, want %q", got, want)
	}
	updated := panelBody(first, &core.PanelSummary{Confirmed: 3, Contested: 2})
	if updated != "> ℹ️ 5 findings · 2 contested by a verifier\n\nDetails." {
		t.Fatalf("stale summary remains: %q", updated)
	}
	if got := panelBody(first, &core.PanelSummary{Single: 2}); strings.Contains(got, "contested by a verifier") {
		t.Fatalf("obsolete summary remains: %q", got)
	}
}
