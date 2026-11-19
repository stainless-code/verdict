package reviewdoc_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/reviewdoc"
	"github.com/stainless-code/verdict/internal/jq"
)

// alignCase is one lead-alignment leg: a rendered body, the decision the merge
// computed for it, and the literal expected body.
type alignCase struct {
	approval *core.ApprovalReceipt
	name     string
	body     string
	want     string
}

// TestAlignLead proves the rendered lead follows the decision: evidence
// insufficiency takes precedence over both CleanLead and AttentionLead, a
// withheld non-insufficient decision swaps CleanLead to AttentionLead, an
// approving decision leaves the body alone, and a claim that quotes a marker
// is never rewritten.
func TestAlignLead(t *testing.T) {
	withheld := &core.ApprovalReceipt{Approved: false, Reason: "open findings at-or-above important", Tier: "important"}
	approved := &core.ApprovalReceipt{Approved: true, Reason: "no gating findings", Tier: "important"}
	insuf := &core.ApprovalReceipt{
		Approved:     false,
		Insufficient: true,
		Reason:       "review-insufficient: missing receipt",
		Tier:         "important",
	}

	const (
		clean   = "> ✅ No new issues found.\n> Basis: 0 0 0 0 0 0 0\n"
		attn    = "> ⚠️ Findings require attention.\n\n1. **important** `a.go:10` — Original claim text.\n"
		drifted = "> ⚠️ Note: commits landed after this review's fetch.\n\n" + clean
		quoted  = "> ⚠️ Findings require attention.\n\n1. **important** `notes.txt:2` — a claim quoting > ✅ No new issues found.\n"
	)

	cases := []alignCase{
		{
			name:     "clean lead, withheld",
			body:     clean,
			approval: withheld,
			want:     "> ⚠️ Findings require attention.\n> Basis: 0 0 0 0 0 0 0\n",
		},
		{
			name:     "clean lead, approved",
			body:     clean,
			approval: approved,
			want:     clean,
		},
		{
			name:     "clean lead, insufficient",
			body:     clean,
			approval: insuf,
			want:     "> ⛔ Evidence checks insufficient — no approval.\n> Basis: 0 0 0 0 0 0 0\n",
		},
		{
			name:     "attention lead, insufficient",
			body:     attn,
			approval: insuf,
			want:     "> ⛔ Evidence checks insufficient — no approval.\n\n1. **important** `a.go:10` — Original claim text.\n",
		},
		{
			name:     "lead after drift note, insufficient",
			body:     drifted,
			approval: insuf,
			want:     "> ⚠️ Note: commits landed after this review's fetch.\n\n> ⛔ Evidence checks insufficient — no approval.\n> Basis: 0 0 0 0 0 0 0\n",
		},
		{
			name:     "lead after drift note, withheld",
			body:     drifted,
			approval: withheld,
			want:     "> ⚠️ Note: commits landed after this review's fetch.\n\n> ⚠️ Findings require attention.\n> Basis: 0 0 0 0 0 0 0\n",
		},
		{
			name:     "marker inside claim untouched",
			body:     quoted,
			approval: withheld,
			want:     quoted,
		},
		{
			name: "no decision",
			body: clean,
			want: clean,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := reviewdoc.AlignLead(tc.body, tc.approval); got != tc.want {
				t.Fatalf("AlignLead =\n%q\nwant:\n%q", got, tc.want)
			}
		})
	}
}

func TestRenderInsufficientLeadPreservesSummaryAndLabelsUnverified(t *testing.T) {
	review := jq.NewObj(
		"schema_version", "1",
		"summary", "Agent summary kept verbatim.",
		"findings", []any{
			jq.NewObj(
				"path", "pkg/auth.go",
				"line", 42,
				"severity", "important",
				"claim", "Token check skips expiry.",
				"verification", jq.NewObj("quote", "verified"),
			),
			jq.NewObj(
				"path", "pkg/cache.go",
				"line", 19,
				"severity", "critical",
				"claim", "Possible race on map write.",
				"verification", jq.NewObj("quote", "unverified"),
			),
		},
	)
	insuf := &core.ApprovalReceipt{
		Approved:     false,
		Insufficient: true,
		Reason:       "review-insufficient: quote mismatch at pkg/cache.go:19",
		Tier:         "important",
	}

	got := reviewdoc.Render(review, insuf)
	const want = "> ⛔ Evidence checks insufficient — no approval.\n\n" +
		"Agent summary kept verbatim.\n\n" +
		"1. **important** `pkg/auth.go:42` — Token check skips expiry.\n" +
		"2. **critical** `pkg/cache.go:19` (unverified — not counted) — Possible race on map write.\n"
	if got != want {
		t.Fatalf("Render =\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderInsufficientOverridesQuotedSummaryLead(t *testing.T) {
	review := jq.NewObj(
		"schema_version", "1",
		"summary", "> ✅ Looks great from the agent.",
		"findings", []any{},
	)
	insuf := &core.ApprovalReceipt{
		Approved:     false,
		Insufficient: true,
		Reason:       "review-insufficient: missing receipt",
		Tier:         "important",
	}

	got := reviewdoc.Render(review, insuf)
	const want = "> ⛔ Evidence checks insufficient — no approval.\n\n> ✅ Looks great from the agent.\n"
	if got != want {
		t.Fatalf("Render =\n%q\nwant:\n%q", got, want)
	}
}
