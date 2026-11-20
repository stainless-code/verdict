package reviewdoc

import (
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	panelcore "github.com/stainless-code/verdict/internal/core/panel"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/untrusted"
)

const crossExcludedTag = "(cross-check unresolved — not counted)"

// FinderLine renders coordinator-owned discovery and cross-check counts.
func FinderLine(summary *core.PanelSummary) string {
	if summary == nil || summary.Finders == nil {
		return ""
	}
	counts := summary.Finders
	return fmt.Sprintf("> Finders: %d completed; cross-checks: %d confirmed, %d contested, %d pending",
		counts.Completed, counts.Confirmed, counts.Contested, counts.Pending)
}

func crossOf(row *jq.Obj) any {
	return readcommon.FieldOf(readcommon.FieldOf(row, "verification"), "cross")
}

func crossLabel(row *jq.Obj) string {
	cross := crossOf(row)
	if cross == nil {
		return ""
	}
	origins := []string{}
	for _, origin := range jq.Elements(readcommon.FieldOf(cross, "origins")) {
		origins = append(origins, untrusted.InlineProse(jq.StrOf(origin)))
	}
	checks := []string{}
	for _, checker := range jq.Elements(readcommon.FieldOf(cross, "expected")) {
		name := jq.StrOf(checker)
		checks = append(checks, untrusted.InlineProse(name)+" "+renderCrossStatus(cross, name))
	}
	return "(origins: " + strings.Join(origins, commaSep) + "; checks: " + strings.Join(checks, commaSep) + ")"
}

const (
	crossVerified       = "verified"
	maxCrossExplanation = 500
	maxCrossQuote       = 2000
)

func renderCrossStatus(cross any, checker string) string {
	for _, check := range jq.Elements(readcommon.FieldOf(cross, "checks")) {
		if readcommon.FieldOf(check, "checker") != checker {
			continue
		}
		verdict := jq.StrOf(readcommon.FieldOf(check, "verdict"))
		if verdict == "refuted" {
			return "contested"
		}
		if crossConfirmed(check, verdict) {
			return "confirmed"
		}
	}
	return "pending"
}

func crossExcluded(row *jq.Obj) bool {
	cross := crossOf(row)
	return cross != nil && !panelcore.EligibleCross(cross)
}

func renderCrossDetails(row *jq.Obj, indent string) string {
	var lines []string
	for _, check := range jq.Elements(readcommon.FieldOf(crossOf(row), "checks")) {
		if readcommon.FieldOf(check, "verdict") != "refuted" {
			continue
		}
		lines = append(lines, refutationLines(check)...)
	}
	if len(lines) == 0 {
		return ""
	}
	lines = append([]string{"<details><summary>Cross-check evidence</summary>", ""}, lines...)
	lines = append(lines, "</details>")
	for i, line := range lines {
		if line != "" {
			lines[i] = indent + line
		}
	}
	return strings.Join(lines, lineBreak)
}

func refutationLines(check any) []string {
	name, _ := untrusted.Cap(jq.StrOf(readcommon.FieldOf(check, "checker")), maxCrossExplanation)
	checker := untrusted.InlineProse(name)
	explanation, _ := untrusted.Cap(jq.StrOf(readcommon.FieldOf(check, "evidence")), maxCrossExplanation)
	quote := readcommon.FieldOf(check, "quote")
	text, _ := untrusted.Cap(jq.StrOf(readcommon.FieldOf(quote, "text")), maxCrossQuote)
	locator := untrusted.InlineProse(jq.StrOf(readcommon.FieldOf(quote, "path")) + ":" + jq.R(readcommon.FieldOf(quote, "line")))
	standing := untrusted.InlineProse(jq.StrOf(readcommon.FieldOf(check, "quote_status")))
	return []string{
		checker + " refuted (" + standing + " quote): " + untrusted.InlineProse(explanation), "",
		"> " + locator + " — " + untrusted.InlineProse(text), "",
	}
}

func crossConfirmed(check any, verdict string) bool {
	return verdict == "confirmed" && readcommon.FieldOf(check, "quote_status") == crossVerified
}
