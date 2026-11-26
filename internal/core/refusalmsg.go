package core

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Msgf builds a refusal message, and every [RefusalError.Message] in the tree
// is built by it (internal/archtest holds the line). The format is verdict's
// own text and keeps its line breaks, because some refusals lay out several
// lines; every argument's formatted text is folded by [FoldLine], because an
// argument may be a path, a PR title, a host's reply, or an agent's value,
// and a line break inside one would end the `error:` line and start a second
// that reads as verdict's own. A [Trusted] argument passes through unfolded.
//
// The arguments are wrapped in place and forwarded as they came, which is
// what lets `go vet` see Msgf as a printf wrapper and check every call's
// verbs; a caller that spreads a slice it reuses (`Msgf(f, xs...)`) hands
// that slice over. Every such caller in the tree forwards its own variadic
// parameter, a fresh slice per call.
func Msgf(format string, args ...any) string {
	for i, arg := range args {
		if _, trusted := arg.(Trusted); !trusted {
			args[i] = lineArg{arg}
		}
	}
	return fmt.Sprintf(format, args...)
}

// Trusted marks a [Msgf] argument that verdict composed itself and that may
// span lines: a refusal message already built by Msgf, or a hint verdict
// writes. Each use is an exception a reviewer can grep for; a value any part
// of which came from outside verdict is never Trusted.
type Trusted string

// lineArg formats its value as the verb asks, then folds the result.
type lineArg struct{ v any }

// Format implements [fmt.Formatter]. fmt answers %T and %p before it asks
// a Formatter, so those two verbs see lineArg itself; a refusal message has
// no use for either.
func (a lineArg) Format(f fmt.State, verb rune) {
	fmt.Fprint(f, FoldLine(fmt.Sprintf(fmt.FormatString(f, verb), a.v)))
}

// FoldLine replaces every line break ([IsLineBreak]: the controls, CR and LF
// among them, and U+2028/U+2029) and every bidirectional formatting control
// ([IsBidiControl]) with a space, so the value stays on the one line it is
// printed in and cannot reorder it. A tab stays: it separates columns. Bytes
// that are not valid UTF-8 pass through unchanged.
func FoldLine(s string) string {
	i := firstLineFold(s)
	if i < 0 {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	sb.WriteString(s[:i])
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if foldsInLine(r) {
			sb.WriteByte(' ')
		} else {
			sb.WriteString(s[i : i+size])
		}
		i += size
	}
	return sb.String()
}

// firstLineFold is the byte index of the first rune FoldLine replaces, or -1.
func firstLineFold(s string) int {
	for i, r := range s {
		if foldsInLine(r) {
			return i
		}
	}
	return -1
}

// foldsInLine reports whether FoldLine replaces r. U+FFFD, which is also
// what an invalid byte decodes to, is neither a break nor a bidi control, so
// such a byte is copied as it is.
func foldsInLine(r rune) bool {
	return r != '\t' && (IsLineBreak(r) || IsBidiControl(r))
}

// IsBidiControl reports whether r is a bidirectional formatting control: the
// embeddings and overrides (U+202A–U+202E), the isolates (U+2066–U+2069), and
// the marks (U+200E, U+200F, U+061C). U+202E reorders the text after it on a
// bidi-capable terminal, so a line reads as another (Trojan Source).
func IsBidiControl(r rune) bool {
	if r >= bidiEmbedFirst && r <= bidiEmbedLast {
		return true
	}
	if r >= bidiIsolateFirst && r <= bidiIsolateLast {
		return true
	}
	return r == bidiLRM || r == bidiRLM || r == bidiALM
}

// The bidirectional controls [IsBidiControl] names, by code point.
const (
	bidiEmbedFirst   = 0x202a // LEFT-TO-RIGHT EMBEDDING
	bidiEmbedLast    = 0x202e // RIGHT-TO-LEFT OVERRIDE
	bidiIsolateFirst = 0x2066 // LEFT-TO-RIGHT ISOLATE
	bidiIsolateLast  = 0x2069 // POP DIRECTIONAL ISOLATE
	bidiLRM          = 0x200e // LEFT-TO-RIGHT MARK
	bidiRLM          = 0x200f // RIGHT-TO-LEFT MARK
	bidiALM          = 0x061c // ARABIC LETTER MARK
)
