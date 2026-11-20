package untrusted

import (
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/fence"
)

const (
	htmlLT      = "&lt;"
	ellipsis    = "…"
	hrMinRun    = 3
	maxListDigs = 9
)

// InlineProse sanitizes untrusted text for a single-line Markdown context.
// It flattens line breaks, control runes, and bidi controls, preserves balanced
// single-backtick inline code spans, and neutralizes HTML, comments, fences,
// Markdown links/images, and @mentions outside those code spans.
func InlineProse(s string) string {
	flat := flattenInline(s)
	if flat == "" {
		return ""
	}
	var out strings.Builder
	out.Grow(len(flat))
	for i := 0; i < len(flat); {
		if flat[i] != '`' {
			next := strings.IndexByte(flat[i:], '`')
			if next < 0 {
				writeSanitizedProse(&out, flat[i:])
				break
			}
			writeSanitizedProse(&out, flat[i:i+next])
			i += next
			continue
		}
		i = writeBacktickSegment(&out, flat, i)
	}
	return out.String()
}

// Paragraph sanitizes untrusted text as one standalone Markdown paragraph.
// In addition to [InlineProse], it escapes any leading Markdown block opener
// so the paragraph cannot forge a callout, heading, list, or thematic break.
func Paragraph(s string) string {
	line := InlineProse(s)
	if line == "" {
		return ""
	}
	if startsBlockOpener(line) {
		return `\` + line
	}
	return line
}

// HTMLCommentLine sanitizes untrusted text for one line inside an HTML
// comment. It flattens line breaks and bidi controls, breaks every `--`
// sequence, and strips leading `>`/`->` and trailing `-` so HTML5 comment
// closure is impossible.
func HTMLCommentLine(s string) string {
	flat := flattenInline(s)
	if flat == "" {
		return ""
	}
	broken := breakDoubleDash(flat)
	for {
		prev := broken
		broken = strings.TrimLeft(broken, ">")
		broken = strings.TrimPrefix(broken, "->")
		broken = strings.TrimRight(broken, "-")
		broken = strings.TrimSpace(broken)
		if broken == prev {
			break
		}
	}
	return broken
}

// CodeSpan wraps untrusted text as an inline Markdown code span after
// flattening line breaks, controls, and bidi overrides. The delimiter uses a
// backtick run longer than any run inside the content.
func CodeSpan(s string) string {
	flat := flattenInline(s)
	delim := strings.Repeat("`", maxCharRun(flat, '`')+1)
	if flat == "" {
		return delim + delim
	}
	if flat[0] == '`' || flat[len(flat)-1] == '`' {
		return delim + " " + flat + " " + delim
	}
	return delim + flat + delim
}

// LinkLabel sanitizes untrusted text for the label inside `[label][ev-N]`,
// escaping brackets, HTML openers, fence runs, and @mentions so it cannot
// break out of the reference link.
func LinkLabel(s string) string {
	flat := flattenInline(s)
	if flat == "" {
		return ""
	}
	flat = fence.BreakRuns(fence.BreakRuns(flat, '`'), '~')
	var out strings.Builder
	out.Grow(len(flat))
	for i := 0; i < len(flat); {
		switch flat[i] {
		case '\\', '[', ']':
			out.WriteByte('\\')
			out.WriteByte(flat[i])
			i++
		case '<':
			out.WriteString(htmlLT)
			i++
		case '@':
			if opensMention(flat, i) {
				i = copyMention(&out, flat, i)
			} else {
				out.WriteByte('@')
				i++
			}
		default:
			out.WriteByte(flat[i])
			i++
		}
	}
	return out.String()
}

// EscapeURLPath percent-encodes each slash-delimited segment of p while
// preserving `/` separators, and also escapes `(` and `)` so the URL stays
// valid in both reference and inline Markdown links.
func EscapeURLPath(p string) string {
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		esc := url.PathEscape(seg)
		esc = strings.ReplaceAll(esc, "(", "%28")
		esc = strings.ReplaceAll(esc, ")", "%29")
		parts[i] = esc
	}
	return strings.Join(parts, "/")
}

// FenceFor returns a backtick fence at least minLen long (clamped to 3) and
// strictly longer than any backtick run in body.
func FenceFor(body string, minLen int) string {
	n := max(minLen, fenceRunMin)
	if longest := maxCharRun(body, '`'); longest >= n {
		n = longest + 1
	}
	return strings.Repeat("`", n)
}

// CapRunes truncates s to at most maxRunes Unicode runes, appending `…` when
// truncated.
func CapRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	if maxRunes == 1 {
		return ellipsis
	}
	count := 0
	for idx := range s {
		if count == maxRunes-1 {
			return s[:idx] + ellipsis
		}
		count++
	}
	return s
}

func writeBacktickSegment(out *strings.Builder, flat string, i int) int {
	run := runLen(flat[i:], '`')
	if run == 1 {
		if !isEscaped(flat, i) {
			if closeIdx := findClosingSingleTick(flat, i+1); closeIdx > i {
				out.WriteString(fence.Safe(flat[i : closeIdx+1]))
				return closeIdx + 1
			}
		}
		out.WriteByte('`')
		return i + 1
	}
	out.WriteString(fence.BreakRuns(flat[i:i+run], '`'))
	return i + run
}

func findClosingSingleTick(s string, from int) int {
	for i := from; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		run := runLen(s[i:], '`')
		if run == 1 {
			return i
		}
		i += run
	}
	return -1
}

func writeSanitizedProse(out *strings.Builder, chunk string) {
	chunk = fence.BreakRuns(chunk, '~')
	for i := 0; i < len(chunk); {
		if adv := writeNeutralizedPair(out, chunk[i:]); adv > 0 {
			i += adv
			continue
		}
		if chunk[i] == '@' && opensMention(chunk, i) {
			i = copyMention(out, chunk, i)
			continue
		}
		out.WriteByte(chunk[i])
		i++
	}
}

func writeNeutralizedPair(out *strings.Builder, rest string) int {
	if rest[0] == '<' {
		out.WriteString(htmlLT)
		return 1
	}
	if len(rest) < 2 {
		return 0
	}
	switch rest[:2] {
	case "](", "][", "]:", "![":
		out.WriteByte(rest[0])
		out.WriteString(zeroWidthSpace)
		out.WriteByte(rest[1])
		return 2
	default:
		return 0
	}
}

func flattenInline(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	lastSpace := true
	for _, r := range s {
		if isInlineSpace(r) {
			if !lastSpace {
				out.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		out.WriteRune(r)
		lastSpace = false
	}
	return strings.TrimRight(out.String(), " ")
}

func isInlineSpace(r rune) bool {
	if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
		return true
	}
	return core.IsLineBreak(r) || isBidiControl(r)
}

func isBidiControl(r rune) bool {
	if r >= '\u202a' && r <= '\u202e' {
		return true
	}
	if r >= '\u2066' && r <= '\u2069' {
		return true
	}
	return r == '\u200e' || r == '\u200f' || r == '\u061c'
}

func startsBlockOpener(s string) bool {
	switch s[0] {
	case '>', '#':
		return true
	case '-', '*', '+':
		if len(s) == 1 || s[1] == ' ' {
			return true
		}
		return isThematicBreak(s, s[0])
	case '_':
		return isThematicBreak(s, '_')
	}
	return startsOrderedList(s)
}

func isThematicBreak(s string, ch byte) bool {
	count := 0
	for i := range len(s) {
		switch s[i] {
		case ch:
			count++
		case ' ':
		default:
			return false
		}
	}
	return count >= hrMinRun
}

func startsOrderedList(s string) bool {
	digits := 0
	for digits < len(s) && digits < maxListDigs && s[digits] >= '0' && s[digits] <= '9' {
		digits++
	}
	if digits == 0 || digits >= len(s) {
		return false
	}
	if s[digits] != '.' && s[digits] != ')' {
		return false
	}
	return digits+1 == len(s) || s[digits+1] == ' '
}

func breakDoubleDash(s string) string {
	if !strings.Contains(s, "--") {
		return s
	}
	var out strings.Builder
	out.Grow(len(s))
	prevDash := false
	for i := range len(s) {
		if s[i] == '-' {
			if prevDash {
				out.WriteString(zeroWidthSpace)
			}
			out.WriteByte('-')
			prevDash = true
			continue
		}
		out.WriteByte(s[i])
		prevDash = false
	}
	return out.String()
}

func maxCharRun(s string, ch byte) int {
	best, run := 0, 0
	for i := range len(s) {
		if s[i] == ch {
			run++
			best = max(best, run)
			continue
		}
		run = 0
	}
	return best
}
