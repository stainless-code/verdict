package panelcore

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// TestConfidenceKeepsFindings derives confidence without voting or dropping
// findings.
func TestConfidenceKeepsFindings(t *testing.T) {
	tests := []struct {
		name     string
		want     string
		verdicts []string
	}{
		{name: "missing", want: "single"},
		{name: "insufficient", verdicts: []string{"insufficient"}, want: "single"},
		{name: "confirmed", verdicts: []string{"confirmed", "insufficient"}, want: "confirmed"},
		{name: "refuted", verdicts: []string{"refuted"}, want: "contested"},
		{name: "no majority", verdicts: []string{"confirmed", "confirmed", "refuted"}, want: "contested"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertConfidence(t, tt.verdicts, tt.want)
		})
	}
}

// TestRepostPreservesAndReplaces preserves refutations and replaces current
// opinions.
func TestRepostPreservesAndReplaces(t *testing.T) {
	entry := jq.NewObj("sig", "abc", "status", "open", "verification", []any{jq.NewObj("model", "a", "verdict", "refuted", "evidence", "original counterexample")})
	Apply([]any{entry}, &File{Results: []Result{{Sig: "abc", Model: "b", Verdict: "confirmed", Evidence: "later confirmation"}}})
	Apply([]any{entry}, &File{})
	if got, _ := entry.Get("confidence"); got != "contested" {
		t.Fatalf("later confirmation outvoted refutation: %v", got)
	}
	Apply([]any{entry}, &File{Results: []Result{{Sig: "abc", Model: "b", Verdict: "confirmed", Evidence: "replacement evidence"}}})
	wire := jq.PrettyOrdered(entry)
	if !strings.Contains(wire, "original counterexample") || !strings.Contains(wire, "replacement evidence") || strings.Contains(wire, "later confirmation") {
		t.Fatalf("repost=%s", wire)
	}
	Apply([]any{entry}, &File{Results: []Result{{Sig: "abc", Model: "a", Verdict: "confirmed", Evidence: "same model second look"}}})
	if got, _ := entry.Get("confidence"); got != "contested" {
		t.Fatalf("second look erased refutation: %v", got)
	}
}

// TestTerminalAndLegacy keeps terminal and legacy findings unchanged.
func TestTerminalAndLegacy(t *testing.T) {
	entry := jq.NewObj("sig", "abc", "status", "addressed")
	before := jq.PrettyOrdered(entry)
	summary, warnings := Apply([]any{entry}, &File{Results: []Result{{Sig: "abc", Model: "a", Verdict: "refuted"}}})
	if jq.PrettyOrdered(entry) != before || len(warnings) != 1 || summary.Verifiers != 0 {
		t.Fatalf("terminal finding changed: %v %v", summary, warnings)
	}
	summary, warnings = Apply([]any{entry}, nil)
	if summary != nil || len(warnings) != 0 || jq.PrettyOrdered(entry) != before {
		t.Fatal("legacy changed")
	}
}

func assertConfidence(t *testing.T, verdicts []string, want string) {
	t.Helper()
	entry := jq.NewObj("sig", "abc", "kind", "inline", "status", "open")
	file := confidenceFile(verdicts)
	summary, warnings := Apply([]any{entry}, file)
	if got, _ := entry.Get("confidence"); got != want {
		t.Fatalf("confidence=%v, want %s", got, want)
	}
	if status, _ := entry.Get("status"); status != "open" || len(warnings) != 0 || summary.Confirmed+summary.Contested+summary.Single != 1 {
		t.Fatalf("finding lost or blocked: %s %+v %v", jq.PrettyOrdered(entry), summary, warnings)
	}
	if len(verdicts) == 0 && entry.Has("verification") {
		t.Fatal("missing verifier invented evidence")
	}
}

func confidenceFile(verdicts []string) *File {
	file := &File{}
	for i, v := range verdicts {
		file.Results = append(file.Results, Result{Sig: "abc", Model: string(rune('a' + i)), Verdict: v, Evidence: "literal evidence"})
	}
	return file
}
