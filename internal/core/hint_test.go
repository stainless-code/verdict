package core

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/golden"
)

// TestHintForSpendAndBudget proves the two spend refusals keep distinct
// hints: the personal ceiling names the operator limit and the consent
// override, while the ordinary estimate guard keeps its budget hint.
func TestHintForSpendAndBudget(t *testing.T) {
	cases := []struct {
		name string
		code string
		want []string
		deny []string
	}{
		{
			name: "personal ceiling",
			code: "spend-capped",
			want: []string{"--consent-over-budget", "personal spend ceiling", "operator settings"},
			deny: []string{"REVIEW_BUDGET_USD", "budget_usd default"},
		},
		{
			name: "ordinary estimate guard",
			code: "budget-exceeded",
			want: []string{"--consent-over-budget", "REVIEW_BUDGET_USD"},
			deny: []string{"personal spend ceiling", "operator settings"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertHintFragments(t, HintFor(tc.code), tc.code, tc.want, tc.deny)
		})
	}
}

// TestHintForPostSecretShaped proves the refusal's hint offers both ways out —
// strip the value, or publish it on purpose — and prints nothing that could be
// the value itself.
func TestHintForPostSecretShaped(t *testing.T) {
	assertHintFragments(t, HintFor("post-secret-shaped"), "post-secret-shaped",
		[]string{"remove the value", "--allow-secret-values"},
		[]string{"AKIA", "ghp_", "sk-ant"})
}

// assertHintFragments proves the hint contains every required fragment and
// none of the forbidden ones.
func assertHintFragments(t *testing.T, hint, code string, want, deny []string) {
	t.Helper()
	for _, frag := range want {
		if !strings.Contains(hint, frag) {
			t.Errorf("HintFor(%q) = %q, want %q", code, hint, frag)
		}
	}
	for _, frag := range deny {
		if strings.Contains(hint, frag) {
			t.Errorf("HintFor(%q) = %q, must not name %q", code, hint, frag)
		}
	}
}

// TestHintGoldenPinsEveryRefusalCode walks the registry and pins what each
// code resolves to: an exact row, an open family, or the generic fallback.
// Deleting a row — `bad-since` included, whose family would otherwise absorb
// it — moves the golden instead of silently degrading the operator's hint.
func TestHintGoldenPinsEveryRefusalCode(t *testing.T) {
	codes := RefusalCodes()
	if len(codes) == 0 {
		t.Fatal("RefusalCodes returned nothing; the golden would pin no code")
	}
	var sb strings.Builder
	for _, code := range codes {
		sb.WriteString(code + "\t" + HintFor(code) + "\n")
	}
	golden.Check(t, "hints", sb.String())
}

// TestHintForUnknownCodeIsTheFallback proves a code outside the table and
// outside every prefix family still gets the generic remediation line.
func TestHintForUnknownCodeIsTheFallback(t *testing.T) {
	const fallback = "try: address the cause above, then re-run the same command"
	for _, code := range []string{"not-a-real-code", "zzz-nothing"} {
		if got := HintFor(code); got != fallback {
			t.Errorf("HintFor(%q) = %q, want the fallback", code, got)
		}
	}
}
