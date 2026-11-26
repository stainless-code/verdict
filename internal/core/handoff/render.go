package handoff

import (
	"fmt"
	"strings"
)

// render writes the projection as markdown: one section per finding, each
// carrying the brief text fix prints verbatim, and the untrusted-data caution
// before any quoted finding prose.
func render(d *Data) []string {
	lines := []string{
		"# handoff: " + d.Bundle,
		"",
		scopeLine(d),
		"",
		"> " + d.Caution,
	}
	for i := range d.Rows {
		lines = append(lines, rowLines(&d.Rows[i])...)
	}
	return lines
}

// scopeLine names the floor and, when the request scoped it, the globs.
func scopeLine(d *Data) string {
	above := "at or above " + d.Floor
	if d.Floor == FloorOff {
		above = "with no severity floor"
	}
	line := fmt.Sprintf("%d open inline finding(s) %s", d.Count, above)
	if len(d.Paths) > 0 {
		line += " under " + strings.Join(d.Paths, ", ")
	}
	return line + "."
}

// rowLines writes one finding: a heading with the sig, severity, and locator,
// the evidence and fix line, then the brief block.
func rowLines(r *Row) []string {
	lines := []string{
		"",
		"## " + r.Sig + " — " + r.Severity + ", " + r.Locator,
		"",
		"- rigor: " + r.Rigor,
		"- kb: " + joinOrNone(r.KBRules),
		"- verify: " + verifyOf(r),
		"",
		"fix: " + r.FixText,
	}
	if r.Suggestion != nil {
		lines = append(lines, "", "suggested code:", "")
		lines = append(lines, codeLines(*r.Suggestion)...)
	}
	lines = append(lines, "")
	if r.BriefText == "" {
		return append(lines, "brief: none stored")
	}
	return append(lines, strings.Split(strings.TrimRight(r.BriefText, newline), newline)...)
}

// codeIndent opens each line of a suggestion: four spaces make a markdown
// indented code block, which renders the code exactly (markdown strips the
// indent) and which no backtick run inside the code can close.
const codeIndent = "    "

// newline ends one line of a stored block.
const newline = "\n"

// codeLines renders a suggestion as an indented code block, one element per
// line of code, so the code keeps its lines when the human surface folds
// each element to one. The indent also leads every line, so a line of the
// suggestion (the reviewing agent's text) cannot start as verdict's own
// `error:` or `warn:` line. A blank line stays blank.
func codeLines(code string) []string {
	split := strings.Split(strings.TrimRight(code, newline), newline)
	out := make([]string, 0, len(split))
	for _, line := range split {
		if strings.TrimSpace(line) == "" {
			out = append(out, "")
			continue
		}
		out = append(out, codeIndent+strings.TrimRight(line, "\r"))
	}
	return out
}

// verifyOf names the recorded rerun command, or says there is none.
func verifyOf(r *Row) string {
	if r.VerifyCmd == nil || *r.VerifyCmd == "" {
		return "none recorded"
	}
	return *r.VerifyCmd
}

// joinOrNone renders a list on one line, "none" when empty.
func joinOrNone(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}
