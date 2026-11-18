package reviewdoc_test

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/reviewdoc"
)

// alignCase is one lead-alignment leg: a rendered body, the decision the merge
// computed for it, and whether the lead must move.
type alignCase struct {
	approval *core.ApprovalReceipt
	name     string
	body     string
	withheld bool
}

// TestAlignLead proves the rendered lead follows the decision: the clean marker
// becomes the attention marker wherever the lead sits, an approving decision
// leaves the body alone, and a claim that quotes the marker is never rewritten.
func TestAlignLead(t *testing.T) {
	withheld := &core.ApprovalReceipt{Approved: false, Reason: "open findings at-or-above important", Tier: "important"}
	approved := &core.ApprovalReceipt{Approved: true, Reason: "no gating findings", Tier: "important"}
	clean := "> ✅ No new issues found.\n> Basis: 0 0 0 0 0 0 0\n"
	drifted := "> ⚠️ Note: commits landed after this review's fetch.\n\n" + clean
	quoted := "> ⚠️ Findings require attention.\n\n1. **important** `notes.txt:2` — a claim quoting > ✅ No new issues found.\n"

	cases := []alignCase{
		{name: "clean lead, withheld", body: clean, approval: withheld, withheld: true},
		{name: "clean lead, approved", body: clean, approval: approved},
		{name: "lead after the drift note", body: drifted, approval: withheld, withheld: true},
		{name: "marker inside a claim", body: quoted, approval: withheld},
		{name: "no decision", body: clean},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkAlignLead(t, &tc)
		})
	}
}

// checkAlignLead runs one leg: an unmoved lead compares against the input, and
// a moved lead compares against the input with its first clean marker swapped.
func checkAlignLead(t *testing.T, tc *alignCase) {
	t.Helper()
	got := reviewdoc.AlignLead(tc.body, tc.approval)
	want := tc.body
	if tc.withheld {
		want = strings.Replace(tc.body, reviewdoc.CleanLead, reviewdoc.AttentionLead, 1)
	}
	if got != want {
		t.Fatalf("AlignLead = %q, want %q", got, want)
	}
	if !tc.withheld && got != tc.body {
		t.Fatalf("AlignLead rewrote an approving or unrelated body: %q", got)
	}
}
