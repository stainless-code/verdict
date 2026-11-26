package panelcore

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

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

func TestRepostPreservesAndReplaces(t *testing.T) {
	entry := jq.NewObj("sig", "abc", "status", "open", "verification", jq.NewObj("quote", "unverified", "panel", []any{jq.NewObj("model", "a", "verdict", "refuted", "evidence", "original counterexample")}))
	Apply([]any{entry}, &File{Results: []Result{{Sig: "abc", Checker: "b", Model: "b", Verdict: "confirmed", Evidence: "later confirmation"}}})
	Apply([]any{entry}, &File{})
	if got, _ := entry.Get("confidence"); got != "contested" {
		t.Fatalf("later confirmation outvoted refutation: %v", got)
	}
	Apply([]any{entry}, &File{Results: []Result{{Sig: "abc", Checker: "b", Model: "b", Verdict: "confirmed", Evidence: "replacement evidence"}}})
	wire := jq.PrettyOrdered(entry)
	if !strings.Contains(wire, "original counterexample") || !strings.Contains(wire, "replacement evidence") || strings.Contains(wire, "later confirmation") {
		t.Fatalf("repost=%s", wire)
	}
	Apply([]any{entry}, &File{Results: []Result{{Sig: "abc", Checker: "a", Model: "a", Verdict: "confirmed", Evidence: "same model second look"}}})
	if got, _ := entry.Get("confidence"); got != "contested" {
		t.Fatalf("second look erased refutation: %v", got)
	}
}

func TestTerminalAndLegacy(t *testing.T) {
	entry := jq.NewObj("sig", "abc", "status", "addressed")
	before := jq.PrettyOrdered(entry)
	summary, warnings := Apply([]any{entry}, &File{Results: []Result{{Sig: "abc", Checker: "a", Model: "a", Verdict: "refuted"}}})
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
		file.Results = append(file.Results, Result{Sig: "abc", Checker: string(rune('a' + i)), Model: string(rune('a' + i)), Verdict: v, Evidence: "literal evidence"})
	}
	return file
}

// TestPanelMergeKeepsTheQuoteStanding proves a panel merge never rewrites the
// quote standing record's evidence pass stamped: an unverified entry that gains
// verifier results stays unverified, so approval still keeps it out of the
// gating set.
func TestPanelMergeKeepsTheQuoteStanding(t *testing.T) {
	entry := jq.NewObj("sig", "abc", "kind", "inline", "status", "open",
		"verification", jq.NewObj("quote", "unverified"))
	Apply([]any{entry}, &File{Results: []Result{{Sig: "abc", Checker: "a", Model: "a", Verdict: "confirmed", Evidence: "literal evidence"}}})
	verification, _ := entry.Get("verification")
	record, ok := verification.(*jq.Obj)
	if !ok {
		t.Fatalf("verification = %v, want an object", verification)
	}
	quote, _ := record.Get("quote")
	if quote != "unverified" {
		t.Fatalf("quote = %v, want the unverified standing kept", quote)
	}
	if got, _ := entry.Get("confidence"); got != "confirmed" {
		t.Fatalf("confidence = %v, want confirmed beside the kept standing", got)
	}
}

func TestManualCheckerSeatsDoNotCollapseOneModel(t *testing.T) {
	entry := jq.NewObj("sig", "abc", "kind", "inline", "status", "open")
	file := &File{Results: []Result{
		{Sig: "abc", Checker: "seat-1", Model: "same-model", Verdict: "confirmed", Evidence: "First independent seat."},
		{Sig: "abc", Checker: "seat-2", Model: "same-model", Verdict: "confirmed", Evidence: "Second independent seat."},
	}}
	summary, _ := Apply([]any{entry}, file)
	if summary.Verifiers != 2 {
		t.Fatalf("verifiers = %d", summary.Verifiers)
	}
	verification, _ := entry.Get("verification")
	if len(jq.Elements(readcommon.FieldOf(verification, "panel"))) != 2 {
		t.Fatal("checker seats collapsed")
	}
}
