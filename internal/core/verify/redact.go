package verifycore

import (
	"regexp"
	"strings"
)

func sanitizeTail(tail string, environ []string) string {
	return stripControlChars(redactShapes(redactSecrets(tail, environ)))
}

const tokenShapes = `(?:gh[pousr]_[A-Za-z0-9]{16,}` +
	`|github_pat_[A-Za-z0-9_]{20,}` +
	`|glpat-[A-Za-z0-9_\-]{16,}` +
	`|(?:sk|pk|rk)-(?:ant-)?[A-Za-z0-9_\-]{16,}` +
	`|xox[abposr]-[A-Za-z0-9\-]{10,}` +
	`|(?:AKIA|ASIA)[A-Z0-9]{16}` +
	`|AIza[0-9A-Za-z_\-]{35}` +
	`|npm_[A-Za-z0-9]{36}` +
	`|dckr_pat_[A-Za-z0-9_\-]{20,})`

var shapeRedactions = []struct {
	re   *regexp.Regexp
	repl string
}{
	{
		re:   regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.\-]*://)[^\s/?#@:]+(?::[^\s/?#@]*)?@`),
		repl: `${1}` + redacted + `@`,
	},
	{
		re:   regexp.MustCompile(`(?i)((?:proxy-)?authorization\s*:\s*)[^\r\n]+`),
		repl: `${1}` + redacted,
	},
	{
		re:   regexp.MustCompile(`(?i)\b(bearer)\s+[A-Za-z0-9._~+/=\-]{8,}`),
		repl: `${1} ` + redacted,
	},
	{
		re:   regexp.MustCompile(`\b` + tokenShapes),
		repl: redacted,
	},
}

func redactShapes(tail string) string {
	if isASCII(tail) {
		return redactShapesASCII(tail)
	}
	return redactShapesRegexp(tail)
}

func redactShapesRegexp(tail string) string {
	for _, r := range shapeRedactions {
		tail = r.re.ReplaceAllString(tail, r.repl)
	}
	return tail
}

const (
	escByte = 0x1b // ESC opens every escape sequence
	belByte = 0x07 // BEL terminates an OSC string
	delByte = 0x7f // DEL sits above the C0 range
	// CSI parameters run up to the first byte in the final-byte range.
	csiFinalLow  = 0x40
	csiFinalHigh = 0x7e
)

func stripControlChars(s string) string {
	if !hasControlChars(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	rest := s
	for rest != "" {
		c := rest[0]
		switch {
		case c == escByte:
			rest = skipEscape(rest)
		case c == '\n' || c == '\t':
			_ = b.WriteByte(c)
			rest = rest[1:]
		case c < ' ' || c == delByte:
			rest = rest[1:]
		default:
			_ = b.WriteByte(c)
			rest = rest[1:]
		}
	}
	return b.String()
}

func hasControlChars(s string) bool {
	for _, c := range []byte(s) {
		if (c < ' ' && c != '\n' && c != '\t') || c == delByte {
			return true
		}
	}
	return false
}

func skipEscape(s string) string {
	if len(s) < 2 {
		return ""
	}
	var last int
	switch s[1] {
	case '[':
		last = skipCSI(s, 2)
	case ']':
		last = skipOSC(s, 2)
	default:
		last = 1
	}
	return s[last+1:]
}

// skipCSI returns the index of a CSI sequence's final byte, or the last byte
// of s when the sequence never ends.
func skipCSI(s string, from int) int {
	for j := from; j < len(s); j++ {
		if s[j] >= csiFinalLow && s[j] <= csiFinalHigh {
			return j
		}
	}
	return len(s) - 1
}

// skipOSC returns the index of the byte closing an OSC string (BEL or the
// two-byte ST), or the last byte of s when it never closes.
func skipOSC(s string, from int) int {
	for j := from; j < len(s); j++ {
		switch {
		case s[j] == belByte:
			return j
		case strings.HasPrefix(s[j:], "\x1b\\"):
			return j + 1
		}
	}
	return len(s) - 1
}
