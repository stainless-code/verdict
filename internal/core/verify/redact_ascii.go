package verifycore

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// tailRedaction is one match: bytes [start, keep) survive, lead, the marker and
// suffix replace [keep, end).
type tailRedaction struct {
	lead, suffix     string
	start, end, keep int
}

const (
	urlSeparator        = "://"
	authorizationHeader = "authorization"
	bearerScheme        = "bearer"
	bearerMinLen        = 8
)

// redactShapesASCII is the ASCII-only scanner. The regexp path folds the URL
// scheme's [a-z] to LONG S and KELVIN SIGN, which a byte scanner cannot, so
// [redactShapes] sends any non-ASCII tail there.
func redactShapesASCII(tail string) string {
	tail = redactURLs(tail)
	tail = redactAuthorization(tail)
	tail = redactBearer(tail)
	return redactTokens(tail)
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func isASCIIWord(c byte) bool {
	return isAlnumByte(c) || c == '_'
}

func isAlnumByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func isLetterByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isSpaceByte(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\f', '\r':
		return true
	}
	return false
}

func isNewlineByte(c byte) bool {
	return c == '\r' || c == '\n'
}

func isSchemeByte(c byte) bool {
	return isAlnumByte(c) || c == '+' || c == '.' || c == '-'
}

// urlStop reports the bytes that end the userinfo classes: whitespace, /, ?,
// #, @, and, only in the first class, the colon that opens a password.
func urlStop(c byte, beforePassword bool) bool {
	if isSpaceByte(c) || c == '/' || c == '?' || c == '#' || c == '@' {
		return true
	}
	return beforePassword && c == ':'
}

func scanURLClass(tail string, from int, beforePassword bool) int {
	j := from
	for j < len(tail) && !urlStop(tail[j], beforePassword) {
		j++
	}
	return j
}

func redactURLs(tail string) string {
	if !strings.Contains(tail, urlSeparator) {
		return tail
	}
	var reds []tailRedaction
	for cursor := 0; cursor < len(tail); {
		i := strings.Index(tail[cursor:], urlSeparator)
		if i < 0 {
			break
		}
		i += cursor
		r, ok := urlMatch(tail, i)
		if !ok {
			cursor = i + len(urlSeparator)
			continue
		}
		reds = append(reds, r)
		cursor = r.end
	}
	return applyRedactions(tail, reds)
}

func urlMatch(tail string, separator int) (tailRedaction, bool) {
	start := urlSchemeStart(tail, separator)
	if start < 0 {
		return tailRedaction{}, false
	}
	end, ok := urlUserinfoEnd(tail, separator+len(urlSeparator))
	if !ok {
		return tailRedaction{}, false
	}
	return tailRedaction{
		start:  start,
		end:    end,
		keep:   separator + len(urlSeparator),
		suffix: "@",
	}, true
}

func urlSchemeStart(tail string, separator int) int {
	from := separator
	for from > 0 && isSchemeByte(tail[from-1]) {
		from--
	}
	for i := from; i < separator; i++ {
		if isLetterByte(tail[i]) && (i == 0 || !isASCIIWord(tail[i-1])) {
			return i
		}
	}
	return -1
}

func urlUserinfoEnd(tail string, from int) (int, bool) {
	j := scanURLClass(tail, from, true)
	if j == from {
		return 0, false
	}
	if j < len(tail) && tail[j] == ':' {
		j = scanURLClass(tail, j+1, false)
	}
	if j < len(tail) && tail[j] == '@' {
		return j + 1, true
	}
	return 0, false
}

func redactAuthorization(tail string) string {
	lower := strings.ToLower(tail)
	var reds []tailRedaction
	for cursor := 0; cursor < len(tail); {
		i := strings.Index(lower[cursor:], authorizationHeader)
		if i < 0 {
			break
		}
		i += cursor
		r, ok := authorizationMatch(tail, i)
		if !ok {
			cursor = i + len(authorizationHeader)
			continue
		}
		reds = append(reds, r)
		cursor = r.end
	}
	return applyRedactions(tail, reds)
}

func authorizationMatch(tail string, i int) (tailRedaction, bool) {
	value, end, ok := authorizationValue(tail, i+len(authorizationHeader))
	if !ok {
		return tailRedaction{}, false
	}
	return tailRedaction{start: i, end: end, keep: value}, true
}

// authorizationValue returns the start of the value the header class consumes
// and the end of the match. The class is [^\r\n]+, so a trailing whitespace run
// yields its last non-newline byte when nothing follows it.
func authorizationValue(tail string, from int) (int, int, bool) {
	colon, ok := headerColon(tail, from)
	if !ok {
		return 0, 0, false
	}
	value := authorizationClassStart(tail, colon+1)
	if value < 0 {
		return 0, 0, false
	}
	return value, lineEnd(tail, value), true
}

func headerColon(tail string, from int) (int, bool) {
	j := from
	for j < len(tail) && isSpaceByte(tail[j]) {
		j++
	}
	if j < len(tail) && tail[j] == ':' {
		return j, true
	}
	return 0, false
}

func authorizationClassStart(tail string, from int) int {
	value := -1
	j := from
	for j < len(tail) && isSpaceByte(tail[j]) {
		if !isNewlineByte(tail[j]) {
			value = j
		}
		j++
	}
	if j < len(tail) && !isNewlineByte(tail[j]) {
		value = j
	}
	return value
}

func lineEnd(tail string, from int) int {
	j := from
	for j < len(tail) && !isNewlineByte(tail[j]) {
		j++
	}
	return j
}

func isBearerByte(c byte) bool {
	return isAlnumByte(c) || c == '.' || c == '_' || c == '~' ||
		c == '+' || c == '/' || c == '=' || c == '-'
}

func redactBearer(tail string) string {
	lower := strings.ToLower(tail)
	var reds []tailRedaction
	for cursor := 0; cursor < len(tail); {
		i := strings.Index(lower[cursor:], bearerScheme)
		if i < 0 {
			break
		}
		i += cursor
		r, ok := bearerMatch(tail, i)
		if !ok {
			cursor = i + len(bearerScheme)
			continue
		}
		reds = append(reds, r)
		cursor = r.end
	}
	return applyRedactions(tail, reds)
}

func bearerMatch(tail string, i int) (tailRedaction, bool) {
	if i > 0 && isASCIIWord(tail[i-1]) {
		return tailRedaction{}, false
	}
	end, ok := bearerEnd(tail, i+len(bearerScheme))
	if !ok {
		return tailRedaction{}, false
	}
	return tailRedaction{
		start: i,
		end:   end,
		keep:  i + len(bearerScheme),
		lead:  " ",
	}, true
}

func bearerEnd(tail string, from int) (int, bool) {
	j := from
	for j < len(tail) && isSpaceByte(tail[j]) {
		j++
	}
	if j == from {
		return 0, false
	}
	start := j
	for j < len(tail) && isBearerByte(tail[j]) {
		j++
	}
	if j-start < bearerMinLen {
		return 0, false
	}
	return j, true
}

type tokenRule struct {
	ok     func(byte) bool
	prefix string
	minLen int
	exact  int
}

func isTokenByte(c byte) bool {
	return isAlnumByte(c) || c == '_'
}

func isTokenDashByte(c byte) bool {
	return isTokenByte(c) || c == '-'
}

func isAlnumDashByte(c byte) bool {
	return isAlnumByte(c) || c == '-'
}

func isUpperTokenByte(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

var tokenRules = []tokenRule{
	{prefix: "ghp_", ok: isAlnumByte, minLen: 16},
	{prefix: "ghu_", ok: isAlnumByte, minLen: 16},
	{prefix: "ghs_", ok: isAlnumByte, minLen: 16},
	{prefix: "gho_", ok: isAlnumByte, minLen: 16},
	{prefix: "ghr_", ok: isAlnumByte, minLen: 16},
	{prefix: "github_pat_", ok: isTokenByte, minLen: 20},
	{prefix: "glpat-", ok: isTokenDashByte, minLen: 16},
	{prefix: "sk-", ok: isTokenDashByte, minLen: 16},
	{prefix: "pk-", ok: isTokenDashByte, minLen: 16},
	{prefix: "rk-", ok: isTokenDashByte, minLen: 16},
	{prefix: "xoxa-", ok: isAlnumDashByte, minLen: 10},
	{prefix: "xoxb-", ok: isAlnumDashByte, minLen: 10},
	{prefix: "xoxp-", ok: isAlnumDashByte, minLen: 10},
	{prefix: "xoxo-", ok: isAlnumDashByte, minLen: 10},
	{prefix: "xoxs-", ok: isAlnumDashByte, minLen: 10},
	{prefix: "xoxr-", ok: isAlnumDashByte, minLen: 10},
	{prefix: "AKIA", ok: isUpperTokenByte, exact: 16},
	{prefix: "ASIA", ok: isUpperTokenByte, exact: 16},
	{prefix: "AIza", ok: isTokenDashByte, exact: 35},
	{prefix: "npm_", ok: isAlnumByte, exact: 36},
	{prefix: "dckr_pat_", ok: isTokenDashByte, minLen: 20},
}

func redactTokens(tail string) string {
	positions := tokenPositions(tail)
	var reds []tailRedaction
	for _, p := range positions {
		if covered(reds, p) || glued(tail, p) {
			continue
		}
		r, ok := tokenMatch(tail, p)
		if !ok {
			continue
		}
		reds = append(reds, r)
	}
	return applyRedactions(tail, reds)
}

func covered(reds []tailRedaction, at int) bool {
	return len(reds) > 0 && at < reds[len(reds)-1].end
}

func glued(tail string, at int) bool {
	return at > 0 && isASCIIWord(tail[at-1])
}

func tokenMatch(tail string, at int) (tailRedaction, bool) {
	end := tokenEnd(tail, at)
	if end < 0 {
		return tailRedaction{}, false
	}
	return tailRedaction{start: at, end: end, keep: at}, true
}

func tokenPositions(tail string) []int {
	var positions []int
	for _, rule := range tokenRules {
		for from := 0; from < len(tail); {
			i := strings.Index(tail[from:], rule.prefix)
			if i < 0 {
				break
			}
			from += i
			positions = append(positions, from)
			from++
		}
	}
	slices.Sort(positions)
	return positions
}

func tokenEnd(tail string, at int) int {
	rest := tail[at:]
	for _, rule := range tokenRules {
		if !strings.HasPrefix(rest, rule.prefix) {
			continue
		}
		if rule.exact > 0 {
			return tokenExact(tail, at+len(rule.prefix), rule.ok, rule.exact)
		}
		return tokenRun(tail, at+len(rule.prefix), rule.ok, rule.minLen)
	}
	return -1
}

func tokenRun(tail string, from int, ok func(byte) bool, minLen int) int {
	j := from
	for j < len(tail) && ok(tail[j]) {
		j++
	}
	if j-from < minLen {
		return -1
	}
	return j
}

func tokenExact(tail string, from int, ok func(byte) bool, n int) int {
	if from+n > len(tail) {
		return -1
	}
	for i := from; i < from+n; i++ {
		if !ok(tail[i]) {
			return -1
		}
	}
	return from + n
}

func applyRedactions(tail string, reds []tailRedaction) string {
	if len(reds) == 0 {
		return tail
	}
	var b strings.Builder
	b.Grow(len(tail))
	last := 0
	for _, r := range reds {
		b.WriteString(tail[last:r.start])
		b.WriteString(tail[r.start:r.keep])
		b.WriteString(r.lead)
		b.WriteString(redacted)
		b.WriteString(r.suffix)
		last = r.end
	}
	b.WriteString(tail[last:])
	return b.String()
}
