package agentusage

import (
	"os"
	"path/filepath"
	"strings"
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
		{"claude result not the final line", agentcatalog.UsageClaudeJSON, "claude-result-not-last.out", "", Measurement{}, false},
		{"claude literal past the bound", agentcatalog.UsageClaudeJSON, "claude-long-number.out", "", Measurement{}, false},
		{"claude exponent past the bound", agentcatalog.UsageClaudeJSON, "claude-huge-exponent.out", "", Measurement{}, false},
		{"codex cut mid-stream", agentcatalog.UsageCodexJSONL, "codex-mid-truncated.jsonl", "gpt-6.2-sol", Measurement{}, false},
		{"claude output read as codex", agentcatalog.UsageCodexJSONL, "claude-success.out", "gpt-6.2-sol", Measurement{}, false},
		{"unknown format", "pi-json", "claude-success.out", "", Measurement{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := Measure(Capture(tc.format, fixture(t, tc.file)), tc.model)
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
		{agentcatalog.UsageCodexJSONL, "codex-two-messages.jsonl", "Here is the review: {\"findings\": []}\nDone.", true},
		{agentcatalog.UsageOpenCodeJSONL, "opencode.jsonl", "Reading the bundle now.\nWrote review.json.", true},
	}
	for _, tc := range cases {
		got, ok := Text(Capture(tc.format, fixture(t, tc.file)))
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

// TestTapReadsStdoutPastTheCaptureAndFlagsOverflow proves the tap's bounds:
// a long stream is read whole as it arrives (no tail cap drops the early
// steps), while an over-long line makes the stream unmeasurable rather than
// half-read.
func TestTapReadsStdoutPastTheCaptureAndFlagsOverflow(t *testing.T) {
	t.Parallel()
	tap := NewTap(agentcatalog.UsageOpenCodeJSONL)
	step := []byte(`{"type":"step_finish","part":{"type":"step-finish","cost":0.001}}` + "\n")
	noise := []byte(`{"type":"tool_use","part":{"state":{"output":"` + strings.Repeat("x", 4096) + `"}}}` + "\n")
	for range 1000 {
		tap.Feed(noise)
		tap.Feed(step)
	}
	if got, ok := Measure(tap, ""); !ok || got.USD != "1" {
		t.Fatalf("4 MiB stream = %+v %v, want every one of the 1000 steps (1)", got, ok)
	}
	long := NewTap(agentcatalog.UsageOpenCodeJSONL)
	long.Feed(step)
	long.Feed([]byte(`{"type":"text","part":{"id":"p","text":"` + strings.Repeat("y", maxLineBytes) + `"}}` + "\n"))
	long.Feed(step)
	if got, ok := Measure(long, ""); ok {
		t.Fatalf("over-long line measured %+v, want nothing", got)
	}
}

// TestCodexModelFollowsTheLaunchAndConfig proves the model a codex launch is
// priced for: an explicit flag or override first, then the selected profile,
// then config.toml's model; local and non-OpenAI providers are never priced.
func TestCodexModelFollowsTheLaunchAndConfig(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	config := "model = \"gpt-6.2-sol\"\nprofile = \"fast\"\n\n[profiles.fast]\nmodel = \"gpt-6-luna\"\n\n" +
		"[profiles.local]\nmodel_provider = \"ollama\"\nmodel = \"llama\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		model string
		args  []string
		ok    bool
	}{
		{name: "default profile", args: []string{"exec"}, model: "gpt-6-luna", ok: true},
		{name: "named profile", args: []string{"exec", "-p", "local"}},
		{name: "explicit model", args: []string{"exec", "--model=gpt-5"}, model: "gpt-5", ok: true},
		{name: "override against the profile", args: []string{"exec", "-c", `model="gpt-5-mini"`}},
		{name: "override without a profile", args: []string{"exec", "-p", "bare", "-c", "model=gpt-5-mini"}, model: "gpt-5-mini", ok: true},
		{name: "flag beats a later override", args: []string{"exec", "-m", "gpt-6.2-astra", "-c", "model=gpt-6-luna"}, model: "gpt-6.2-astra", ok: true},
		{name: "profile override", args: []string{"exec", "-c", "profile=local"}},
		{name: "flag and override disagree on the profile", args: []string{"exec", "-p", "fast", "-c", "profile=local"}},
		{name: "dotted model override", args: []string{"exec", "-c", "profiles.fast.model=gpt-6.2-astra"}},
		{name: "open-source run", args: []string{"exec", "--oss"}},
	}
	for _, tc := range cases {
		if model, ok := CodexModel(home, tc.args); model != tc.model || ok != tc.ok {
			t.Errorf("%s: CodexModel = %q %v, want %q %v", tc.name, model, ok, tc.model, tc.ok)
		}
	}
	if model, ok := CodexModel(t.TempDir(), []string{"exec"}); model != "" || ok {
		t.Errorf("no config = %q %v, want nothing", model, ok)
	}
}

// TestCodexConfigCommentsAndProfileFiles proves the config reader: trailing
// comments are stripped, a profile kept in its own <name>.config.toml is
// read, and a model, provider, or profile key it cannot read is no price.
func TestCodexConfigCommentsAndProfileFiles(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, config, profileFile, model string
		args                             []string
		ok                               bool
	}{
		{
			name:   "commented profile and header",
			config: "model = \"gpt-6.2-sol\"\nprofile = \"fast\" # default\n[profiles.fast] # cheap\nmodel = \"gpt-6-luna\" # small\n",
			model:  "gpt-6-luna", ok: true,
		},
		{name: "commented local provider", config: "model = \"gpt-6.2-sol\"\nmodel_provider = \"ollama\" # local\n"},
		{name: "hash inside the value", config: "model = \"gpt-6.2-sol#x\"\n", model: "gpt-6.2-sol#x", ok: true},
		{
			name: "profile file", config: "model = \"gpt-6.2-sol\"\n", profileFile: "model = \"gpt-6-luna\"\n",
			args: []string{"-p", "fast"}, model: "gpt-6-luna", ok: true,
		},
		{
			name: "profile file names another provider", config: "model = \"gpt-6.2-sol\"\n",
			profileFile: "model_provider = \"ollama\"\n", args: []string{"-p", "fast"},
		},
		{name: "unreadable model", config: "model = 5\n"},
		{name: "dotted profile model", config: "model = \"gpt-6.2-sol\"\nprofiles.fast.model = \"gpt-6-luna\"\n"},
		{name: "inline profile", config: "model = \"gpt-6.2-sol\"\n[profiles]\nfast = { model = \"gpt-6-luna\" }\n"},
	}
	for _, tc := range cases {
		home := t.TempDir()
		writeConfig(t, home, "config.toml", tc.config)
		if tc.profileFile != "" {
			writeConfig(t, home, "fast.config.toml", tc.profileFile)
		}
		if model, ok := CodexModel(home, append([]string{"exec"}, tc.args...)); model != tc.model || ok != tc.ok {
			t.Errorf("%s: CodexModel = %q %v, want %q %v", tc.name, model, ok, tc.model, tc.ok)
		}
	}
}

func writeConfig(t *testing.T, home, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
