package agentusage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/spend"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestMeasureReadsEachCLIsOwnShape pins each parser to a real-shaped capture:
// the CLI's own cost when it reports one, tokens priced from the table when it
// reports only usage, and nothing for a capture that does not match the
// documented shape.
func TestMeasureReadsEachCLIsOwnShape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, format, file, model string
		want                      Measurement
		ok                        bool
	}{
		{
			"claude result", agentcatalog.UsageClaudeJSON, "claude-success.out", "",
			Measurement{USD: "0.4213875", PricedBy: spend.PricedMeasured},
			true,
		},
		{
			"claude error arm still spent", agentcatalog.UsageClaudeJSON, "claude-error.out", "",
			Measurement{USD: "1.07", PricedBy: spend.PricedMeasured},
			true,
		},
		{
			"claude after noise", agentcatalog.UsageClaudeJSON, "claude-noise.txt", "",
			Measurement{USD: "0.4213875", PricedBy: spend.PricedMeasured},
			true,
		},
		{"claude truncated", agentcatalog.UsageClaudeJSON, "claude-truncated.txt", "", Measurement{}, false},
		{"claude without a cost", agentcatalog.UsageClaudeJSON, "claude-nocost.out", "", Measurement{}, false},
		{"claude negative cost", agentcatalog.UsageClaudeJSON, "claude-negative.out", "", Measurement{}, false},
		{"claude string cost", agentcatalog.UsageClaudeJSON, "claude-string-cost.out", "", Measurement{}, false},
		// The last turn.completed is the thread's running total: 315 fresh
		// input at $2, 24448 cached at $0.10, 122 output at $10, per million.
		{
			"codex tokens", agentcatalog.UsageCodexJSONL, "codex.jsonl", "gpt-6.2-sol",
			Measurement{USD: "0.0042948", PricedBy: spend.PricedTokens, Model: "gpt-6.2-sol"},
			true,
		},
		{"codex without a model", agentcatalog.UsageCodexJSONL, "codex.jsonl", "", Measurement{}, false},
		{"codex unpriced model", agentcatalog.UsageCodexJSONL, "codex.jsonl", "gpt-99", Measurement{}, false},
		{"codex cache writes", agentcatalog.UsageCodexJSONL, "codex-cache-write.jsonl", "gpt-6.2-sol", Measurement{}, false},
		{"codex zero usage", agentcatalog.UsageCodexJSONL, "codex-zero.jsonl", "gpt-6.2-sol", Measurement{}, false},
		{"codex truncated", agentcatalog.UsageCodexJSONL, "codex-truncated.jsonl", "gpt-6.2-sol", Measurement{}, false},
		{"codex string tokens", agentcatalog.UsageCodexJSONL, "codex-string-tokens.jsonl", "gpt-6.2-sol", Measurement{}, false},
		{
			"opencode steps", agentcatalog.UsageOpenCodeJSONL, "opencode.jsonl", "",
			Measurement{USD: "0.0163", PricedBy: spend.PricedMeasured},
			true,
		},
		{"opencode step without a cost", agentcatalog.UsageOpenCodeJSONL, "opencode-nocost.jsonl", "", Measurement{}, false},
		{"opencode truncated", agentcatalog.UsageOpenCodeJSONL, "opencode-truncated.jsonl", "", Measurement{}, false},
		{"claude output read as codex", agentcatalog.UsageCodexJSONL, "claude-success.out", "gpt-6.2-sol", Measurement{}, false},
		{"unknown format", "pi-json", "claude-success.out", "", Measurement{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := Measure(tc.format, fixture(t, tc.file), tc.model)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("Measure = %+v, %v; want %+v, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestTextReadsTheAgentsFinalMessage proves the text a review salvage reads
// comes out of each structured stream.
func TestTextReadsTheAgentsFinalMessage(t *testing.T) {
	t.Parallel()
	cases := []struct {
		format, file, want string
		ok                 bool
	}{
		{agentcatalog.UsageClaudeJSON, "claude-success.out", "Wrote .verdict/state/local-x/review.json.", true},
		{agentcatalog.UsageClaudeJSON, "claude-error.out", "", false},
		{agentcatalog.UsageCodexJSONL, "codex.jsonl", "Wrote review.json with one finding.", true},
		{agentcatalog.UsageOpenCodeJSONL, "opencode.jsonl", "Reading the bundle now.\nWrote review.json.", true},
	}
	for _, tc := range cases {
		got, ok := Text(tc.format, fixture(t, tc.file))
		if ok != tc.ok || got != tc.want {
			t.Errorf("Text(%s) = %q, %v; want %q, %v", tc.file, got, ok, tc.want, tc.ok)
		}
	}
}

// TestSumIsExactAndTokenPricedWhenAnyPartIs proves the meter's total.
func TestSumIsExactAndTokenPricedWhenAnyPartIs(t *testing.T) {
	t.Parallel()
	usd, by, ok := Sum([]Measurement{
		{USD: "0.1", PricedBy: spend.PricedMeasured},
		{USD: "0.2", PricedBy: spend.PricedTokens},
	})
	if !ok || usd != "0.3" || by != spend.PricedTokens {
		t.Fatalf("Sum = %s %s %v, want 0.3 tokens", usd, by, ok)
	}
	if _, _, ok := Sum(nil); ok {
		t.Error("an empty sum measured something")
	}
}
