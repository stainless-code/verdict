package backtest

import "strings"

// The eight published corpus kinds plus the honest fallback. A kind is a
// deterministic guess from the fix subject first, then the fix diff; the
// manual publishes every word and pattern below.
const (
	kindAuthBypass    = "auth-bypass"
	kindMissingErr    = "missing-error-check"
	kindNilDeref      = "nil-deref"
	kindOffByOne      = "off-by-one"
	kindIndexRange    = "index-out-of-range"
	kindPathTraversal = "path-traversal"
	kindRace          = "race"
	kindInjection     = "injection"
	kindUnclassified  = "unclassified"
)

// kindRule names one kind and the subject words that classify it.
type kindRule struct {
	kind  string
	words []string
}

// subjectKinds is the ordered subject rule table: first match wins.
var subjectKinds = []kindRule{
	{kind: kindAuthBypass, words: []string{"auth", "authoriz", "authentic", "permission", "privilege", "bypass", "admin", "role"}},
	{kind: kindPathTraversal, words: []string{"traversal", "zip slip", "path escape", "directory traversal", "confine", "path join"}},
	{kind: kindInjection, words: []string{"inject", "sql", "xss", "command execution", "sanitiz"}},
	{kind: kindRace, words: []string{"race", "concurrent", "goroutine", "mutex", "deadlock", "lock"}},
	{kind: kindNilDeref, words: []string{"nil", "null pointer", "nil pointer", "dereference"}},
	{kind: kindOffByOne, words: []string{"off-by-one", "off by one", "loop bound", "fencepost", "boundary"}},
	{kind: kindIndexRange, words: []string{"index out of range", "index-out-of-range", "out of range", "panic"}},
	{kind: kindMissingErr, words: []string{"error", "err", "unchecked", "ignored", "handle failure"}},
}

// classifyKind classifies one fix: the subject table first, then the diff
// table, then unclassified.
func classifyKind(subject string, deleted, added []string) string {
	lower := strings.ToLower(subject)
	for _, rule := range subjectKinds {
		if anyWordStart(lower, rule.words) {
			return rule.kind
		}
	}
	return diffKind(deleted, added)
}

// diffKind is the ordered diff rule table: first matching kind wins.
func diffKind(deleted, added []string) string {
	switch {
	case anyContains(added, "err != nil", "err == nil"):
		return kindMissingErr
	case anyWordStartLine(deleted, "nil", "null"):
		return kindNilDeref
	case offByOneDiff(deleted, added):
		return kindOffByOne
	case anyContains(added, ">= len(", "<= 0"):
		return kindIndexRange
	case anyContains(added, "sync.Mutex", "sync.RWMutex", "sync/atomic", "atomic."):
		return kindRace
	case anyContains(added, "filepath.Clean", "filepath.Rel"):
		return kindPathTraversal
	case injectionDiff(added):
		return kindInjection
	case anyWordStartLine(added, "authorized", "permission", "admin"):
		return kindAuthBypass
	default:
		return kindUnclassified
	}
}

// offByOneDiff recognizes a bound comparison tightened from <= to <.
func offByOneDiff(deleted, added []string) bool {
	return anyContains(deleted, "<=") && anyContains(deleted, "len(") &&
		anyContains(added, "<") && anyContains(added, "len(")
}

// injectionDiff recognizes escaping or a parameterized query in the fix.
func injectionDiff(added []string) bool {
	if anyContains(added, "html.EscapeString", "strconv.Quote") {
		return true
	}
	for _, line := range added {
		upper := strings.ToUpper(line)
		if strings.Contains(line, "?") &&
			(strings.Contains(upper, "SELECT") || strings.Contains(upper, "INSERT") || strings.Contains(upper, "UPDATE")) {
			return true
		}
	}
	return false
}

// anyWordStart reports whether any token appears at a word start in s.
func anyWordStart(s string, tokens []string) bool {
	for _, token := range tokens {
		if hasWordStart(s, token) {
			return true
		}
	}
	return false
}

// anyWordStartLine reports whether any line carries any token at a word start.
func anyWordStartLine(lines []string, tokens ...string) bool {
	for _, line := range lines {
		lower := strings.ToLower(line)
		if anyWordStart(lower, tokens) {
			return true
		}
	}
	return false
}

// anyContains reports whether any line carries any needle.
func anyContains(lines []string, needles ...string) bool {
	for _, line := range lines {
		for _, needle := range needles {
			if strings.Contains(line, needle) {
				return true
			}
		}
	}
	return false
}

// hasWordStart reports whether token appears in s after a non-word byte (or at
// the start), so `race` never matches inside `trace`.
func hasWordStart(s, token string) bool {
	if token == "" {
		return false
	}
	for from := 0; from+len(token) <= len(s); {
		at := strings.Index(s[from:], token)
		if at < 0 {
			return false
		}
		pos := from + at
		if pos == 0 || !wordByte(s[pos-1]) {
			return true
		}
		from = pos + 1
	}
	return false
}

// wordByte reports a byte that continues a word.
func wordByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
