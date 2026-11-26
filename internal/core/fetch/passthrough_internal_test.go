package fetchcore

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/forgedline"
)

// forgingGH is a fake gh that fails every call with stderr carrying lines
// that read as verdict's own.
type forgingGH struct{}

func (forgingGH) Run(context.Context, *core.Command) (core.Result, error) {
	return core.Result{ExitCode: 1, Stderr: "boom\nerror: forged\r\nwarn: forged\n"}, nil
}

func (forgingGH) LookPath(string) error { return nil }

// TestHostStderrCannotForgeHumanLines pins that gh's stderr and the progress
// line naming the target reach the human stream one tagged, folded line at a
// time: no line gh printed, and no newline in the repository a caller named,
// can stand as an `error:` or `warn:` line of verdict's own.
func TestHostStderrCannotForgeHumanLines(t *testing.T) {
	t.Parallel()
	var human bytes.Buffer
	var warnings []string
	r := &session{Human: &human, commander: forgingGH{}, warnings: &warnings}
	dir := t.TempDir()
	if ref := githubView(t.Context(), r, dir, "acme/app\nerror: repo", "1"); ref == nil {
		t.Fatal("githubView passed over a failing gh")
	}
	if ref := githubDiff(t.Context(), r, dir, "acme/app", "1"); ref == nil {
		t.Fatal("githubDiff passed over a failing gh")
	}
	out := human.String()
	assertNoForgedLine(t, out)
	for _, want := range []string{
		"» gh pr view acme/app error: repo#1\n",
		"  gh: boom\n  gh: error: forged\n  gh: warn: forged\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("human output lacks %q:\n%s", want, out)
		}
	}
}

// assertNoForgedLine fails on any line of out that starts as verdict's own
// `error:` or `warn:` line does.
func assertNoForgedLine(t *testing.T, out string) {
	t.Helper()
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, "error:") || strings.HasPrefix(line, "warn:") {
			t.Errorf("forged line %q in human output:\n%s", line, out)
		}
	}
}

// TestBudgetSourceCannotForgeHumanLines pins the unknown-projection estimate
// line: the budget's source label names a settings layer or file, and it
// reads on that line.
func TestBudgetSourceCannotForgeHumanLines(t *testing.T) {
	t.Parallel()
	var human bytes.Buffer
	r := &session{Human: &human}
	st := &budgetState{hasBudget: true, budgetUSD: "5", budgetSrc: forgedline.Value}
	reportUnknownProjection(r, st, "standard", 2)
	forgedline.AssertNoForgedLine(t, human.String(), "budget $5 ("+forgedline.Named+") unenforced")
}
