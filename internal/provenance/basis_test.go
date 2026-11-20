package provenance_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/provenance"
)

func TestFormatBasisLine(t *testing.T) {
	t.Parallel()
	const now = "2026-11-20T09:00:00Z"
	review := jq.NewObj(
		"findings", []any{
			jq.NewObj("rigor", "ran"),
			jq.NewObj("rigor", "walked"),
			jq.NewObj("rigor", "pointed"),
		},
	)
	entries := []any{
		jq.NewObj(
			"kb", []any{"rule-a"},
			"status", "open",
			"history", []any{jq.NewObj("at", now, "event", "reported")},
		),
		jq.NewObj(
			"status", "addressed",
			"history", []any{jq.NewObj("at", now, "event", "addressed")},
		),
	}
	got := provenance.FormatBasisLine(review, entries, now)
	const want = "0 reproduced · 1 ran · 1 walked · 1 pointed · 0 said · 1 rules cited · 1 verified fixes"
	if got != want {
		t.Fatalf("FormatBasisLine = %q, want %q", got, want)
	}
}
