package panelcore

import (
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func (s *Execution) completedFinders(manifest *jq.Obj) {
	s.Completed = 2
	failed := map[string]bool{}
	for _, row := range jq.Elements(readcommon.FieldOf(manifest, "failures")) {
		phase := jq.StrOf(readcommon.FieldOf(row, "phase"))
		if phase == "x-finder-1" || phase == "x-finder-2" {
			failed[phase] = true
		}
	}
	s.Completed -= len(failed)
}

// Attach publishes the same derived execution counts through every consumer.
func (s *Execution) Attach(summary *core.PanelSummary) *core.PanelSummary {
	if s == nil || !s.Active {
		return summary
	}
	if summary == nil {
		summary = &core.PanelSummary{}
	}
	counts := &core.FinderSummary{Completed: s.Completed}
	for sig, expected := range s.Expected {
		sumCrossChecks(counts, expected, s.Checked[sig])
	}
	summary.Finders = counts
	return summary
}

func sumCrossChecks(counts *core.FinderSummary, expected []string, checks []any) {
	for _, checker := range expected {
		status := crossStatus(checker, checks)
		switch status {
		case confirmed:
			counts.Confirmed++
		case refuted:
			counts.Contested++
		default:
			counts.Pending++
		}
	}
}

func crossStatus(checker string, checks []any) string {
	for _, check := range checks {
		if readcommon.FieldOf(check, fieldChecker) != checker {
			continue
		}
		if readcommon.FieldOf(check, fieldVerdict) == refuted {
			return refuted
		}
		if confirmedQuote(check) {
			return confirmed
		}
	}
	return insufficient
}

func hasRefutation(checks []any) bool {
	for _, check := range checks {
		if readcommon.FieldOf(check, fieldVerdict) == refuted {
			return true
		}
	}
	return false
}
