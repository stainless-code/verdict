package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/golden"
)

// TestToolFreePromptPinsShippedReview derives the tool-free prompt from the
// real prompt files and pins it: any shipped edit forces a reviewed golden
// diff.
func TestToolFreePromptPinsShippedReview(t *testing.T) {
	root, err := moduleRoot()
	if err != nil {
		t.Fatalf("moduleRoot: %v", err)
	}
	var sb strings.Builder
	for _, rel := range promptFiles {
		raw, err := os.ReadFile(filepath.Join(root, prefixPrompts, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		sb.Write(raw)
	}
	got, err := toToolFreePrefix(sb.String())
	if err != nil {
		t.Fatalf("toToolFreePrefix: %v", err)
	}
	if !strings.Contains(got, "You have no tools") {
		t.Error("derived prompt carries no no-tools statement: every adapter must inherit it")
	}
	golden.Check(t, "tool-free-prompt", got)
}

// TestToolFreeTransformNeedsItsAnchors removes one anchor at a time: each
// removal names its heading instead of drifting silently.
func TestToolFreeTransformNeedsItsAnchors(t *testing.T) {
	sentence := "read related files, grep callers of changed symbols, check tests on touched paths,\n   fetch related GitHub state."
	for _, tc := range []struct{ remove, want string }{
		{"2. **Checkout / context.**", "2. **Checkout / context.**"},
		{"3. **Triage.**", "3. **Triage.**"},
		{sentence, "3. **Triage.**"},
		{"4. **Specialist decision.**", "4. **Specialist decision.**"},
		{"6. **Aggregate", "6. **Aggregate"},
		{"7. **Submit.**", "7. **Submit.**"},
		{"## Review body format", "## Review body format"},
	} {
		got, err := toToolFreePrefix(strings.Replace(fakePrefix, tc.remove, "", 1))
		if err == nil {
			t.Errorf("remove %q: transform accepted a prefix without it", tc.remove)
			continue
		}
		if want := "transform anchor missing: " + tc.want; !strings.Contains(err.Error(), want) {
			t.Errorf("remove %q: error = %v, want %q", tc.remove, err, want)
		}
		if got != "" {
			t.Errorf("remove %q: transform returned bytes with its error", tc.remove)
		}
	}
}

// TestToolFreeTransformRewritesTheWorkflow pins the rewritten steps on the
// fixture prefix.
func TestToolFreeTransformRewritesTheWorkflow(t *testing.T) {
	got, err := toToolFreePrefix(fakePrefix)
	if err != nil {
		t.Fatalf("toToolFreePrefix: %v", err)
	}
	for _, want := range []string{
		"You have no tools",
		"your\nfinal message must be exactly one review.json document",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("derived prompt omits %q", want)
		}
	}
	for _, dropped := range []string{
		"Run `verdict fetch owner/repo N`",
		"Emit every subagent call in a single turn",
		"run `verdict post owner/repo N`",
	} {
		if strings.Contains(got, dropped) {
			t.Errorf("derived prompt still carries %q", dropped)
		}
	}
}

// TestPiAdapterRefusesToolCallMarkup drives pi.sh with a stub whose final
// message is tool-call markup: exit 1, the exact refusal, no review file.
func TestPiAdapterRefusesToolCallMarkup(t *testing.T) {
	needPOSIX(t)
	out, stderr, err := runPiAdapter(t, "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"<tool_call name=\\\"shell\\\">ls\"}]}}'\n")
	if err == nil {
		t.Fatal("pi.sh accepted tool-call markup as a review")
	}
	want := "pi agent: final message is a tool call, but this sample has no tools: <tool_call name=\"shell\">ls"
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("pi.sh wrote %s for a tool-call refusal", out)
	}
}

// TestRunnerLabelsAdapterToolCall fails a tool-call sample with its own kind.
func TestRunnerLabelsAdapterToolCall(t *testing.T) {
	needPOSIX(t)
	restoreWD(t)
	cfg := benchConfig(t, fakeCorpus(t), `printf '<tool_call name="shell">ls</tool_call>'; exit 3`)
	fake := &fakeGrader{model: cfg.Model, panel: cfg.Panel, prefix: fakePrefix}
	rep, err := runGraded(context.Background(), cfg, fake)
	if err == nil {
		t.Fatal("runGraded reported success after a tool-call agent")
	}
	if rep == nil || len(rep.Failures) != 1 {
		t.Fatalf("failures = %+v, want one labeled failure", rep)
	}
	got := rep.Failures[0]
	if got.Kind != "adapter_tool_call" {
		t.Errorf("kind = %q, want adapter_tool_call", got.Kind)
	}
	if !strings.Contains(got.Error, "agent exit 3") {
		t.Errorf("error = %q, want the adapter exit", got.Error)
	}
}

// TestValidReviewWithToolMarkupStaysPass keeps a passing sample a pass when
// its log mentions tool markup: the kind only ever labels a failure.
func TestValidReviewWithToolMarkupStaysPass(t *testing.T) {
	needPOSIX(t)
	restoreWD(t)
	t.Setenv("BENCH_TEST_REVIEW", reviewBody)
	cfg := benchConfig(t, fakeCorpus(t),
		`printf '%s' "$BENCH_TEST_REVIEW" > "$VERDICT_BENCH_OUT"; printf '<tool_call name="shell">stray transcript line</tool_call>'`)
	fake := &fakeGrader{model: cfg.Model, panel: cfg.Panel, prefix: fakePrefix}
	rep, err := runGraded(context.Background(), cfg, fake)
	if err != nil {
		t.Fatalf("runGraded: %v", err)
	}
	if rep.Samples != 1 || len(rep.Failures) != 0 {
		t.Fatalf("samples=%d failures=%v, want one clean sample", rep.Samples, rep.Failures)
	}
}

// dsmlSample is the verbatim STA-271 failure: DSML tool markup with fullwidth
// bars (U+FF5C) between the brackets and the tag names.
const dsmlSample = "<｜｜DSML｜｜ calls><｜｜DSML｜｜ invoke name=\"bg_run\"><｜｜DSML｜｜ parameter name=\"command\" string=\"true\">ls -la; echo ---; cat go.mod 2>/dev/null; …"

// TestPiAdapterRefusesDSMLMarkup drives pi.sh with a stub whose final message
// is the STA-271 DSML sample: exit 1, the tool-call refusal, no review file.
func TestPiAdapterRefusesDSMLMarkup(t *testing.T) {
	needPOSIX(t)
	text := strings.ReplaceAll(dsmlSample, `"`, `\"`)
	out, stderr, err := runPiAdapter(t, "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\""+text+"\"}]}}'\n")
	if err == nil {
		t.Fatal("pi.sh accepted DSML markup as a review")
	}
	want := "pi agent: final message is a tool call, but this sample has no tools: " + dsmlSample
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("pi.sh wrote %s for a tool-call refusal", out)
	}
}

// TestPiAdapterAcceptsFencedReviewWithToolMarkup drives pi.sh with a stub
// whose final message is fenced valid JSON that mentions tool-call markup:
// the fence strip accepts it, exit 0, and the out file holds the unfenced
// bytes.
func TestPiAdapterAcceptsFencedReviewWithToolMarkup(t *testing.T) {
	needPOSIX(t)
	inner := `{"schema_version":"1","body":"mentions tool_call and <function> in review","approved":false,"comments":[]}`
	text := "```json\\n" + strings.ReplaceAll(inner, `"`, `\"`) + "\\n```"
	out, stderr, err := runPiAdapter(t, "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\""+text+"\"}]}}'\n")
	if err != nil {
		t.Fatalf("pi.sh refused fenced valid review with tool markup: %v, stderr %q", err, stderr)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read %s: %v", out, err)
	}
	want := "{\"schema_version\":\"1\",\"body\":\"mentions tool_call and <function> in review\",\"approved\":false,\"comments\":[]}\n\n"
	if string(got) != want {
		t.Errorf("review.json = %q, want %q", got, want)
	}
}

// TestPiAdapterRefusesFencedNonJSONWithToolMark drives pi.sh with a stub
// whose final message is a fenced non-JSON answer containing a tool mark:
// exit 1, the tool-call refusal, no review file.
func TestPiAdapterRefusesFencedNonJSONWithToolMark(t *testing.T) {
	needPOSIX(t)
	text := "```\\n" + strings.ReplaceAll(`<tool_call name="shell">ls`, `"`, `\"`) + "\\n```"
	out, stderr, err := runPiAdapter(t, "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\""+text+"\"}]}}'\n")
	if err == nil {
		t.Fatal("pi.sh accepted fenced non-JSON tool-call markup as a review")
	}
	want := "pi agent: final message is a tool call, but this sample has no tools: ```<tool_call name=\"shell\">ls```"
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("pi.sh wrote %s for a tool-call refusal", out)
	}
}

// TestRunnerLabelsDSMLToolCall labels the DSML sample adapter_tool_call.
func TestRunnerLabelsDSMLToolCall(t *testing.T) {
	needPOSIX(t)
	restoreWD(t)
	cfg := benchConfig(t, fakeCorpus(t), `printf '%s' '<｜｜DSML｜｜ calls><｜｜DSML｜｜ invoke name="bg_run"><｜｜DSML｜｜ parameter name="command" string="true">ls -la; echo ---; cat go.mod 2>/dev/null; …'; exit 3`)
	fake := &fakeGrader{model: cfg.Model, panel: cfg.Panel, prefix: fakePrefix}
	rep, err := runGraded(context.Background(), cfg, fake)
	if err == nil {
		t.Fatal("runGraded reported success after a DSML tool-call agent")
	}
	if rep == nil || len(rep.Failures) != 1 {
		t.Fatalf("failures = %+v, want one labeled failure", rep)
	}
	got := rep.Failures[0]
	if got.Kind != "adapter_tool_call" {
		t.Errorf("kind = %q, want adapter_tool_call", got.Kind)
	}
	if !strings.Contains(got.Error, "agent exit 3") {
		t.Errorf("error = %q, want the adapter exit", got.Error)
	}
}

// piAdapterTimeout bounds one pi.sh run: the adapter is a shell script over a
// stub binary, so anything slower is a hang, not work.
const piAdapterTimeout = 30 * time.Second

// piAdapterLinger bounds the wait for the adapter's captured stderr once the
// process exits with its pipes still open.
const piAdapterLinger = 5 * time.Second

// runPiAdapter drives bench/agents/pi.sh against a stub whose final message is
// body, and returns the review path it was told to write, its stderr, and its
// exit error.
func runPiAdapter(t *testing.T, body string) (out, stderr string, runErr error) {
	t.Helper()
	root, err := moduleRoot()
	if err != nil {
		t.Fatalf("moduleRoot: %v", err)
	}
	t.Setenv("BENCH_PI_MODEL", "fixture-good")
	t.Setenv("BENCH_PI_BIN", piStub(t, body))
	t.Setenv("VERDICT_BENCH_MODEL", "fixture-good")
	t.Setenv("VERDICT_BENCH_PROMPT", filepath.Join(t.TempDir(), "prompt.md"))
	out = filepath.Join(t.TempDir(), "review.json")
	t.Setenv("VERDICT_BENCH_OUT", out)
	t.Setenv("VERDICT_BENCH_COST", filepath.Join(t.TempDir(), "cost.json"))

	ctx, cancel := context.WithTimeout(t.Context(), piAdapterTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", filepath.Join(root, "bench", "agents", "pi.sh"))
	cmd.Env = os.Environ()
	var captured strings.Builder
	cmd.Stderr = &captured
	cmd.WaitDelay = piAdapterLinger
	runErr = cmd.Run()
	return out, captured.String(), runErr
}

// piAdapterMarks reads the tool-call pattern out of pi.sh.
func piAdapterMarks(t *testing.T) []string {
	t.Helper()
	root, err := moduleRoot()
	if err != nil {
		t.Fatalf("moduleRoot: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "bench", "agents", "pi.sh"))
	if err != nil {
		t.Fatalf("read pi.sh: %v", err)
	}
	const anchor = "grep -qiE '"
	text := string(raw)
	_, rest, found := strings.Cut(text, anchor)
	if !found {
		t.Fatal("pi.sh carries no grep -qiE tool-call pattern")
	}
	marks, _, found := strings.Cut(rest, "'")
	if !found {
		t.Fatal("pi.sh tool-call pattern is unterminated")
	}
	return strings.Split(marks, "|")
}

// TestToolCallMarksMatchPiAdapter pins the runner marks to the pi.sh grep
// pattern: the two lists name the same markup and cannot drift apart.
func TestToolCallMarksMatchPiAdapter(t *testing.T) {
	got := piAdapterMarks(t)
	if len(got) != len(toolCallMarks) {
		t.Fatalf("pi.sh marks %q vs toolCallMarks %q", got, toolCallMarks)
	}
	for i := range got {
		if got[i] != toolCallMarks[i] {
			t.Errorf("mark %d: pi.sh %q vs toolCallMarks %q", i, got[i], toolCallMarks[i])
		}
	}
}
