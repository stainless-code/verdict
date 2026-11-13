package panelcore

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

func TestConfidenceKeepsFindings(t *testing.T) {
	tests := []struct {
		name     string
		verdicts []string
		want     string
	}{
		{"missing", nil, "single"}, {"insufficient", []string{"insufficient"}, "single"}, {"confirmed", []string{"confirmed", "insufficient"}, "confirmed"}, {"refuted", []string{"refuted"}, "contested"}, {"no majority", []string{"confirmed", "confirmed", "refuted"}, "contested"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := jq.NewObj("sig", "abc", "kind", "inline", "status", "open")
			file := &File{}
			for i, v := range tt.verdicts {
				file.Results = append(file.Results, Result{Sig: "abc", Model: string(rune('a' + i)), Verdict: v, Evidence: "literal evidence"})
			}
			summary, warnings := Apply([]any{entry}, file)
			if got, _ := entry.Get("confidence"); got != tt.want {
				t.Fatalf("confidence=%v, want %s", got, tt.want)
			}
			if status, _ := entry.Get("status"); status != "open" || len(warnings) != 0 || summary.Confirmed+summary.Contested+summary.Single != 1 {
				t.Fatalf("finding lost or blocked: %s %+v %v", jq.PrettyOrdered(entry), summary, warnings)
			}
			if len(tt.verdicts) == 0 && entry.Has("verification") {
				t.Fatal("missing verifier invented evidence")
			}
		})
	}
}

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
