package verifycore

import (
	"regexp"
	"strings"

	"github.com/stainless-code/verdict/internal/readcommon"
)

// gateText carries the normalized verifyCmd texts every arm matches
// against. Built once per Check call:
// flat (whitespace-collapsed), lc (ASCII-lowercased), scan (obfuscation
// folds), bare (quote-completed words, backticks spaced), csbare (the
// case-preserved twin for upload flags), and adj (sentinel-marked
// substitution spans).
type gateText struct {
	flat   string
	lc     string
	scan   string
	bare   string
	csbare string
	adj    string
	// deepSubst reports substitution nesting beyond maxSentinelPasses, so adj
	// still carries unreplaced spans a hidden tool word could ride behind.
	deepSubst bool
	// unreduced reports a surviving $(…)/${…} span the patterns cannot match at
	// all (an inner bare paren, say) that sits against a word character: the
	// same mid-word concatenation hazard, hidden by the pattern's gap.
	unreduced bool
}

// normalize builds every gate text from the raw command.
func normalize(raw string) *gateText {
	g := &gateText{}
	g.flat = flatOf(raw)
	g.lc = readcommon.ASCIILower(g.flat)
	g.scan = scanOf(g.lc)
	g.bare = bareOf(g.scan, false)
	g.csbare = bareOf(g.flat, true)
	g.adj, g.deepSubst = adjOf(g.scan)
	g.unreduced = gluedSpan(g.adj)
	return g
}

// flatOf collapses newlines, tabs, and carriage returns to spaces, squeezes
// runs, and trims the ends.
func flatOf(raw string) string {
	var sb strings.Builder
	sb.Grow(len(raw))
	for i := range len(raw) {
		appendFlatByte(&sb, flatByte(raw[i]))
	}
	return strings.Trim(sb.String(), spaceStr)
}

// flatByte folds newlines, tabs, and carriage returns to a space.
func flatByte(c byte) byte {
	if c == '\n' || c == '\t' || c == '\r' {
		return ' '
	}
	return c
}

// appendFlatByte appends one byte, squeezing space runs like tr -s.
func appendFlatByte(sb *strings.Builder, c byte) {
	if c == ' ' && sb.Len() > 0 {
		if prev, _ := lastByte(sb); prev == ' ' {
			return
		}
	}
	sb.WriteByte(c)
}

// lastByte reads the last byte written to sb.
func lastByte(sb *strings.Builder) (byte, bool) {
	s := sb.String()
	if s == "" {
		return 0, false
	}
	return s[len(s)-1], true
}

// scanOf owns the scan pipeline: @@null@@ for the discard sink, =op for
// the [[ =~ ]] operator, backslash stripping, IFS folding, and $@/$*/$N
// deletion.
func scanOf(lc string) string {
	s := strings.ReplaceAll(lc, "/dev/null", "@@null@@")
	s = reTildeOp.ReplaceAllString(s, "=op ")
	s = strings.ReplaceAll(s, "\\", "")
	s = strings.ReplaceAll(s, "${ifs}", spaceStr)
	s = reIfsVar.ReplaceAllString(s, spaceStr+groupRef)
	s = strings.ReplaceAll(s, "$@", "")
	s = strings.ReplaceAll(s, "$*", "")
	s = strings.ReplaceAll(s, "${@}", "")
	s = strings.ReplaceAll(s, "${*}", "")
	s = rePosParam.ReplaceAllString(s, "")
	return s
}

// bareOf owns the bare/csbare pipeline: backslash deletion (csbare reads
// $flat, which still carries them; bare reads $scan, already stripped, so
// the deletion is a no-op there), unwrap $'..' and $"..", delete remaining
// quotes, space-fold backticks, squeeze spaces. upper selects the
// IFS-variable case (csbare matches on un-lowercased text).
func bareOf(s string, upper bool) string {
	s = strings.ReplaceAll(s, "\\", "")
	s = reLocaleSingle.ReplaceAllString(s, groupRef)
	s = reLocaleDouble.ReplaceAllString(s, groupRef)
	s = reQuotes.ReplaceAllString(s, "")
	if upper {
		s = strings.ReplaceAll(s, "${IFS}", spaceStr)
		s = reIFSVarUpper.ReplaceAllString(s, spaceStr+groupRef)
	} else {
		s = strings.ReplaceAll(s, "${ifs}", spaceStr)
		s = reIfsVar.ReplaceAllString(s, spaceStr+groupRef)
	}
	s = strings.ReplaceAll(s, "$@", "")
	s = strings.ReplaceAll(s, "$*", "")
	s = strings.ReplaceAll(s, "${@}", "")
	s = strings.ReplaceAll(s, "${*}", "")
	s = rePosParam.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "`", spaceStr)
	return squeezeSpaces(s)
}

// squeezeSpaces collapses space runs, mirroring `tr -s ' '`.
func squeezeSpaces(s string) string {
	if !strings.Contains(s, "  ") {
		return s
	}
	return reSpaces.ReplaceAllString(s, spaceStr)
}

// adjOf owns the adj pipeline: quote handling like bare (backticks kept),
// then sentinel replacement of innermost $(…), ${…}, $VAR, and `…` spans.
// The bool reports nesting deeper than maxSentinelPasses: the bounded loops
// stop with spans left, which the caller must treat as hostile (fail closed)
// rather than as clean text.
func adjOf(scan string) (string, bool) {
	s := reLocaleSingle.ReplaceAllString(scan, groupRef)
	s = reLocaleDouble.ReplaceAllString(s, groupRef)
	s = reQuotes.ReplaceAllString(s, "")
	s = squeezeSpaces(s)
	var deepParen, deepBrace bool
	s, deepParen = replaceSpans(s, reDollarParen)
	s, deepBrace = replaceSpans(s, reDollarBrace)
	s = reDollarVar.ReplaceAllString(s, markStr)
	s = reBacktick.ReplaceAllString(s, markStr)
	return s, deepParen || deepBrace
}

// replaceSpans replaces innermost spans up to maxSentinelPasses times and
// reports whether another pass would still have changed the text.
func replaceSpans(s string, re *regexp.Regexp) (string, bool) {
	for range maxSentinelPasses {
		nxt := re.ReplaceAllString(s, markStr)
		if nxt == s {
			return s, false
		}
		s = nxt
	}
	return s, re.MatchString(s)
}

// gluedSpan reports a substitution span that survived adjOf's replacement
// loops and sits against a word character, so bash would concatenate it into a
// word the gate cannot read. The span-body patterns cannot match a `$(` whose
// body carries a bare paren (`$( (echo m) )`), and a failed match leaves the
// span as clean-looking text; the bounded loops never see a pass to count, so
// the depth bound does not fire. Such a span is unscannable, not clean.
func gluedSpan(s string) bool {
	for i := 0; i+1 < len(s); i++ {
		if !spanOpen(s, i) {
			continue
		}
		end := spanClose(s, i+1)
		if end < 0 {
			return gluedLeft(s, i)
		}
		if glueAt(s, i, end) {
			return true
		}
		i = end
	}
	return false
}

// spanOpen reports whether a command-substitution span opens at i (`$(` or
// `${`), excluding the `$((…))` arithmetic form.
func spanOpen(s string, i int) bool {
	if s[i] != '$' || (s[i+1] != '(' && s[i+1] != '{') {
		return false
	}
	return s[i+1] != '(' || i+2 >= len(s) || s[i+2] != '('
}

// gluedLeft reports a word character immediately before the span that opens
// at i (an unparsable span is hostile only when it can glue onto that word).
func gluedLeft(s string, i int) bool {
	return i > 0 && isWordByte(s[i-1])
}

// glueAt reports a word character immediately before the span, or after the
// byte that closes it.
func glueAt(s string, open, end int) bool {
	return open > 0 && isWordByte(s[open-1]) || end+1 < len(s) && isWordByte(s[end+1])
}

// spanClose returns the index of the byte closing the span opened at open
// (`(` or `{`), or -1 when it never closes.
func spanClose(s string, open int) int {
	closeByte, depth := byte(')'), 0
	if s[open] == '{' {
		closeByte = '}'
	}
	for j := open; j < len(s); j++ {
		switch s[j] {
		case s[open]:
			depth++
		case closeByte:
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

// isWordByte reports a byte the sentinel arm treats as a word character.
func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_'
}

// markStr is the substitution sentinel.
const markStr = "\x01"

// spaceStr is the single space folded/squeezed separators use.
const spaceStr = " "

// groupRef is the whole-match backreference for sed unwraps.
const groupRef = "$1"

// maxSentinelPasses bounds the nested-span replacement loops.
const maxSentinelPasses = 8

// grepO reports every non-overlapping match on the (single-line) input,
// joined by newlines. Only the first matching line counts, and each match
// lands on its own line, so an alternation matching twice yields a multiline
// fragment (e.g. protocol.ext.allow plus ext::). A first-match-only scan
// would silently drop the later matches.
func grepO(re *regexp.Regexp, s string) string {
	return strings.Join(re.FindAllString(s, -1), "\n")
}

// Normalization patterns for the collapsed text.
var (
	reTildeOp      = regexp.MustCompile(`=~[[:space:]][[:space:]]*`)
	reIfsVar       = regexp.MustCompile(`\$ifs([^a-z0-9_]|$)`)
	reIFSVarUpper  = regexp.MustCompile(`\$IFS([^a-zA-Z0-9_]|$)`)
	rePosParam     = regexp.MustCompile(`\$[0-9]`)
	reLocaleSingle = regexp.MustCompile(`\$'([^']*)'`)
	reLocaleDouble = regexp.MustCompile(`\$"([^"]*)"`)
	reQuotes       = regexp.MustCompile(`['"]`)
	reSpaces       = regexp.MustCompile(` {2,}`)
	reDollarParen  = regexp.MustCompile(`\$\([^()]*\)`)
	reDollarBrace  = regexp.MustCompile(`\$\{[^{}]*}`)
	reDollarVar    = regexp.MustCompile(`\$[a-z_][a-z0-9_]*`)
	reBacktick     = regexp.MustCompile("`[^`]*`")
)
