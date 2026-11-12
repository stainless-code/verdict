package core_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// TestRungOfSaidForNilIsLiteral pins the recording default: an absent, null,
// or false rigor reads said, and a value no rung names reads as no rung
// rather than the weakest recorded one.
func TestRungOfSaidForNilIsLiteral(t *testing.T) {
	t.Parallel()
	cases := []struct {
		entry any
		name  string
	}{
		{entry: jq.NewObj("sig", "ab12"), name: "absent"},
		{entry: jq.NewObj("rigor", nil), name: "null"},
		{entry: jq.NewObj("rigor", false), name: "false"},
	}
	for _, tc := range cases {
		if got := core.RungOf(tc.entry); got != "said" {
			t.Errorf("RungOf(%s) = %q, want %q", tc.name, got, "said")
		}
	}
	if got := core.RungOf(jq.NewObj("rigor", "reproduced")); got != "reproduced" {
		t.Errorf("RungOf(published) = %q, want %q", got, "reproduced")
	}
	if got := core.RungOf(jq.NewObj("rigor", "traced")); got != "" {
		t.Errorf("RungOf(unpublished) = %q, want %q", got, "")
	}
}

// TestRungRankOrdersThePublishedList pins the precedence the ranking reads:
// strongest first, with an unnamed rigor ahead of every recorded rung.
func TestRungRankOrdersThePublishedList(t *testing.T) {
	t.Parallel()
	want := map[string]int{
		"reproduced": 0,
		"ran":        1,
		"walked":     2,
		"pointed":    3,
		"said":       4,
		"traced":     -1,
	}
	for value, rank := range want {
		if got := core.RungRank(value); got != rank {
			t.Errorf("RungRank(%q) = %d, want %d", value, got, rank)
		}
	}
	if got := core.RungRank(nil); got != 4 {
		t.Errorf("RungRank(nil) = %d, want 4", got)
	}
	if got := core.RungRank(false); got != 4 {
		t.Errorf("RungRank(false) = %d, want 4", got)
	}
}
