package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	root, err := moduleRoot()
	if err != nil {
		t.Fatalf("moduleRoot: %v", err)
	}
	stub := piStub(t, "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"<tool_call name=\\\"shell\\\">ls\"}]}}'\n")
	t.Setenv("BENCH_PI_MODEL", "fixture-good")
	t.Setenv("BENCH_PI_BIN", stub)
	t.Setenv("VERDICT_BENCH_MODEL", "fixture-good")
	t.Setenv("VERDICT_BENCH_PROMPT", filepath.Join(t.TempDir(), "prompt.md"))
	out := filepath.Join(t.TempDir(), "review.json")
	t.Setenv("VERDICT_BENCH_OUT", out)
	t.Setenv("VERDICT_BENCH_COST", filepath.Join(t.TempDir(), "cost.json"))
	cmd := exec.CommandContext(context.Background(), "sh", filepath.Join(root, "bench", "agents", "pi.sh"))
	cmd.Env = os.Environ()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("pi.sh accepted tool-call markup as a review")
	}
	want := "pi agent: final message is a tool call, but this sample has no tools: <tool_call name=\"shell\">ls"
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
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
