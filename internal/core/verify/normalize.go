package verifycore

import (
	"regexp"
	"strings"
)

// gateText carries the normalized verifyCmd texts every arm matches
// against. Built once per Check call, mirroring the shell pipeline:
// flat (whitespace-collapsed), lc (ASCII-lowercased), scan (obfuscation
// folds), bare (quote-completed words, backticks spaced), csbare (the
// case-preserved twin for upload flags), and adj (sentinel-marked
// substitution spans).
type gateText struct {
	raw    string
	flat   string
	lc     string
	scan   string
	bare   string
	csbare string
	adj    string
}

// normalize builds every gate text from the raw command.
func normalize(raw string) *gateText {
	g := &gateText{raw: raw}
	g.flat = flatOf(raw)
	g.lc = lowerASCII(g.flat)
	g.scan = scanOf(g.lc)
	g.bare = bareOf(g.scan, false)
	g.csbare = bareOf(g.flat, true)
	g.adj = adjOf(g.scan)
	return g
}

// flatOf ports `tr '\n\t\r' ' ' | tr -s ' ' | sed 's/^ //; s/ $//'`.
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

// lowerASCII lowercases ASCII letters only, mirroring tr under the C
// locale. Non-ASCII bytes pass through untouched (gate inputs are ASCII;
// invalid UTF-8 must survive per R2).
func lowerASCII(s string) string {
	if !hasUpper(s) {
		return s
	}
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// hasUpper reports whether s holds an ASCII uppercase letter.
func hasUpper(s string) bool {
	for i := range len(s) {
		if s[i] >= 'A' && s[i] <= 'Z' {
			return true
		}
	}
	return false
}

// scanOf ports the scan pipeline: @@null@@ for the discard sink, =op for
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

// bareOf ports the bare/csbare pipeline: backslash deletion (csbare reads
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

// adjOf ports the adj pipeline: quote handling like bare (backticks kept),
// then sentinel replacement of innermost $(…), ${…}, $VAR, and `…` spans.
func adjOf(scan string) string {
	s := reLocaleSingle.ReplaceAllString(scan, groupRef)
	s = reLocaleDouble.ReplaceAllString(s, groupRef)
	s = reQuotes.ReplaceAllString(s, "")
	s = squeezeSpaces(s)
	for range maxSentinelPasses {
		nxt := reDollarParen.ReplaceAllString(s, markStr)
		if nxt == s {
			break
		}
		s = nxt
	}
	for range maxSentinelPasses {
		nxt := reDollarBrace.ReplaceAllString(s, markStr)
		if nxt == s {
			break
		}
		s = nxt
	}
	s = reDollarVar.ReplaceAllString(s, markStr)
	s = reBacktick.ReplaceAllString(s, markStr)
	return s
}

// markStr is the substitution sentinel (the shell's $'\001').
const markStr = "\x01"

// spaceStr is the single space folded/squeezed separators use.
const spaceStr = " "

// groupRef is the whole-match backreference for sed unwraps.
const groupRef = "$1"

// maxSentinelPasses bounds the nested-span replacement loops.
const maxSentinelPasses = 8

// grepO ports `grep -E -m1 -o -e PAT`: every non-overlapping match on the
// (single-line) input, joined by newlines. The shell's -m1 stops after the
// first matching line, and -o prints each match on its own line, so
// alternations matching twice yield a multiline fragment (e.g.
// protocol.ext.allow plus ext::). First-match-only ports silently drop the
// later matches and their bytes diverge.
func grepO(re *regexp.Regexp, s string) string {
	return strings.Join(re.FindAllString(s, -1), "\n")
}

// Normalization patterns, transliterated from the shell seds.
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
