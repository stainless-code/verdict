package verifycore

import (
	"regexp"
	"strings"

	"github.com/stainless-code/verdict/internal/readcommon"
)

type gateText struct {
	raw       string
	flat      string
	lc        string
	scan      string
	bare      string
	csbare    string
	seg       string
	segExact  string
	qseg      string
	qsegExact string
	adj       string
	deepSubst bool
	unreduced bool
}

func normalize(raw string) *gateText {
	g := &gateText{}
	g.raw = raw
	g.flat = flatOf(raw)
	g.lc = readcommon.ASCIILower(g.flat)
	g.scan = scanOf(g.lc)
	g.bare = bareOf(g.scan, false)
	g.csbare = bareOf(g.flat, true)
	g.adj, g.deepSubst = adjOf(g.scan)
	g.unreduced = gluedSpan(g.adj)
	// The flattened texts lose the newline command separator, and the gate
	// reads one command per line: keep unflattened texts for segmentation —
	// a de-quoted pair for whole-text greps, a quote-preserving pair for the
	// arms that must keep one shell word one word.
	joined := joinContinuations(raw)
	g.seg, g.segExact = g.bare, g.csbare
	g.qseg, g.qsegExact = g.scan, scanOf(raw)
	if strings.ContainsAny(raw, "\n\r") {
		g.seg = bareOf(scanOf(readcommon.ASCIILower(joined)), false)
		g.segExact = bareOf(scanOf(joined), true)
		g.qseg = scanOf(readcommon.ASCIILower(joined))
		g.qsegExact = scanOf(joined)
	}
	return g
}

// joinContinuations drops each unescaped backslash-newline pair: bash reads it
// as a line continuation, not as a command separator.
func joinContinuations(raw string) string {
	if !strings.Contains(raw, "\\\n") {
		return raw
	}
	var sb strings.Builder
	sb.Grow(len(raw))
	i := 0
	for i < len(raw) {
		if raw[i] == '\\' && i+1 < len(raw) && raw[i+1] == '\n' && !escapedBackslash(raw, i) {
			i += 2
			continue
		}
		sb.WriteByte(raw[i])
		i++
	}
	return sb.String()
}

func escapedBackslash(raw string, i int) bool {
	n := 0
	for j := i - 1; j >= 0 && raw[j] == '\\'; j-- {
		n++
	}
	return n%2 == 1
}

func segments(text string) []string {
	var out []string
	start := 0
	for i := range len(text) {
		if !isSegmentBreak(text, i) {
			continue
		}
		if seg := text[start:i]; seg != "" {
			out = append(out, seg)
		}
		start = i + 1
	}
	if seg := text[start:]; seg != "" {
		out = append(out, seg)
	}
	return out
}

// isSegmentBreak reports a command separator. The `&` of a fd-duplication
// (`2>&1`) or a redirect (`&> f`) is not one: it belongs to its redirect.
func isSegmentBreak(text string, i int) bool {
	switch text[i] {
	case ';', '|', '\n', '\r':
		return true
	case '&':
		if i > 0 && (text[i-1] == '>' || text[i-1] == '<') {
			return false
		}
		return i+1 >= len(text) || text[i+1] != '>'
	}
	return false
}

func flatOf(raw string) string {
	var sb strings.Builder
	sb.Grow(len(raw))
	for i := range len(raw) {
		appendFlatByte(&sb, flatByte(raw[i]))
	}
	return strings.Trim(sb.String(), spaceStr)
}

func flatByte(c byte) byte {
	if c == '\n' || c == '\t' || c == '\r' {
		return ' '
	}
	return c
}

func appendFlatByte(sb *strings.Builder, c byte) {
	if c == ' ' && sb.Len() > 0 {
		if prev, _ := lastByte(sb); prev == ' ' {
			return
		}
	}
	sb.WriteByte(c)
}

func lastByte(sb *strings.Builder) (byte, bool) {
	s := sb.String()
	if s == "" {
		return 0, false
	}
	return s[len(s)-1], true
}

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

func spanOpen(s string, i int) bool {
	if s[i] != '$' || (s[i+1] != '(' && s[i+1] != '{') {
		return false
	}
	return s[i+1] != '(' || i+2 >= len(s) || s[i+2] != '('
}

func gluedLeft(s string, i int) bool {
	return i > 0 && isWordByte(s[i-1])
}

func glueAt(s string, open, end int) bool {
	return open > 0 && isWordByte(s[open-1]) || end+1 < len(s) && isWordByte(s[end+1])
}

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

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_'
}

const markStr = "\x01"

const spaceStr = " "

const groupRef = "$1"

const maxSentinelPasses = 8

func grepO(re *regexp.Regexp, s string) string {
	return strings.Join(re.FindAllString(s, -1), "\n")
}

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
