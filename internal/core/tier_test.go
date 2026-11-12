package core_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// TestTierOfUntaggedIsLiteral proves the severity bucket never quotes an
// unpublished value back: absent, null, unrecognized, and non-string
// severities all bucket as untagged.
func TestTierOfUntaggedIsLiteral(t *testing.T) {
	t.Parallel()
	cases := []struct {
		entry any
		name  string
	}{
		{entry: jq.NewObj("kind", "inline"), name: "absent"},
		{entry: jq.NewObj("severity", nil), name: "null"},
		{entry: jq.NewObj("severity", "blocker"), name: "unpublished"},
		{entry: jq.NewObj("severity", 7), name: "non-string"},
		{entry: "critical", name: "non-object"},
	}
	for _, tc := range cases {
		if got := core.TierOf(tc.entry); got != "untagged" {
			t.Errorf("TierOf(%s) = %q, want %q", tc.name, got, "untagged")
		}
	}
	if got := core.TierOf(jq.NewObj("severity", "important")); got != "important" {
		t.Errorf("TierOf(published) = %q, want %q", got, "important")
	}
}

// TestTierRankOrdersThePublishedList pins the strengths the gate threshold
// and the sort read: critical, important, informational, then everything
// else.
func TestTierRankOrdersThePublishedList(t *testing.T) {
	t.Parallel()
	want := map[string]int{"critical": 0, "important": 1, "informational": 2, "blocker": 3}
	for value, rank := range want {
		if got := core.TierRank(value); got != rank {
			t.Errorf("TierRank(%q) = %d, want %d", value, got, rank)
		}
	}
	if got := core.TierRank(nil); got != 3 {
		t.Errorf("TierRank(nil) = %d, want 3", got)
	}
	if got := core.TierRank(false); got != 3 {
		t.Errorf("TierRank(false) = %d, want 3", got)
	}
}
