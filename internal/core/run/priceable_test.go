package run

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/spend"
)

// TestUnpriceableNamesWhatNoMeasurementCanPrice pins which launches a cost
// cap can be enforced over: a CLI that reports its own cost, and codex on a
// model the price table carries, named by -m/--model or its config.toml.
func TestUnpriceableNamesWhatNoMeasurementCanPrice(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configured := t.TempDir()
	writeUnder(t, configured, filepath.Join(".codex", "config.toml"), "model = \"gpt-5\"\n")
	other := t.TempDir()
	writeUnder(t, other, filepath.Join(".codex", "config.toml"), "model = \"gpt-5\"\nmodel_provider = \"ollama\"\n")
	for _, tc := range []struct {
		agent, home, want string
		pass              []string
	}{
		{agent: "claude", home: home},
		{agent: "opencode", home: home},
		{agent: "codex", home: home, pass: []string{"-m", "gpt-5"}},
		{agent: "codex", home: home, pass: []string{"--model=gpt-5-mini"}},
		{agent: "codex", home: configured},
		{agent: "codex", home: home, want: "codex reports only token usage and verdict cannot price it: no model is named"},
		{agent: "codex", home: home, pass: []string{"--oss", "-m", "gpt-5"}, want: "codex reports only token usage " +
			"and verdict cannot price it: --oss or --local-provider runs a local model"},
		{agent: "codex", home: other, want: "codex reports only token usage and verdict cannot price it: its " +
			"model_provider is not openai"},
		{agent: "codex", home: home, pass: []string{"-c", "profiles.x.model=gpt-5"}, want: "codex reports only " +
			"token usage and verdict cannot price it: a `-c` override names a model, provider, or profile key"},
		{
			agent: "codex", home: home, pass: []string{"-m", "not-a-priced-model"},
			want: "codex's model not-a-priced-model is not in verdict's built-in price table",
		},
		{agent: "pi", home: home, want: "pi reports neither its cost nor token usage verdict can price"},
		{
			agent: "claude", home: home, pass: []string{"--output-format", "text"},
			want: "your `--` arguments choose claude's output format",
		},
		{agent: "nope", home: home},
	} {
		req := &core.RunRequest{Agent: tc.agent, PassThrough: tc.pass}
		req.Runtime = &core.Runtime{Env: map[string]string{"HOME": tc.home}}
		got := Unpriceable(req, nil)
		if tc.want == "" && got != "" || tc.want != "" && !strings.HasPrefix(got, tc.want) {
			t.Errorf("%s %q (home %s): Unpriceable = %q, want %q", tc.agent, tc.pass, tc.home, got, tc.want)
		}
	}
}

// TestRunRefusesAnUnpriceableLaunchUnderAStrictCeiling proves a run whose
// monthly ceiling requires a known estimate refuses budget-unpriceable before
// launching anything when its agent cannot be priced (pi, codex with no
// model), while claude proceeds, consent accepts the unknown spend, and
// neither a ceiling alone nor require_known_estimate alone refuses.
func TestRunRefusesAnUnpriceableLaunchUnderAStrictCeiling(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		agent, ceiling string
		strict         bool
		consent        bool
		refuse         bool
	}{
		{agent: "pi", ceiling: "10", strict: true, refuse: true},
		{agent: "codex", ceiling: "10", strict: true, refuse: true},
		{agent: "claude", ceiling: "10", strict: true},
		{agent: "pi", ceiling: "10", strict: true, consent: true},
		{agent: "pi", ceiling: "10"},
		{agent: "pi", strict: true},
	} {
		root, dir, proj := setupMonthlyBundle(t, tc.ceiling, "")
		st := monthlyState(t, root, &scripted{}, tc.strict)
		st.req.Agent, st.req.ConsentOverBudget = tc.agent, tc.consent
		st.req.Env["HOME"] = t.TempDir()
		ref := st.authorizeProjection(t.Context(), dir, mustCaptured(t, dir), 1, []spend.Phase{}, proj)
		switch {
		case tc.refuse && (ref == nil || ref.Code != codeBudgetUnpriceable):
			t.Errorf("%+v: refusal = %v, want budget-unpriceable", tc, ref)
		case tc.refuse && !strings.Contains(ref.Message, "--consent-over-budget"):
			t.Errorf("%+v: refusal %q names no consent", tc, ref.Message)
		case !tc.refuse && ref != nil && ref.Code == codeBudgetUnpriceable:
			t.Errorf("%+v: refused budget-unpriceable: %v", tc, ref)
		}
	}
}
