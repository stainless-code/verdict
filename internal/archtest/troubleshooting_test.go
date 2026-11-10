package archtest

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// troubleshootingHeading matches one troubleshooting entry: the refusal code
// as an exact level-2 heading. The backticks keep a code-shaped word in prose
// from counting as an entry.
var troubleshootingHeading = regexp.MustCompile("^## `([a-z0-9-]+)`$")

// TestTroubleshootingCoversRefusalCodes proves docs/troubleshooting.md names
// exactly the refusal codes the binary can emit, in both directions, with a
// cause line and a fix line per entry. The doc is hand-written prose; this
// test is the sync mechanism, the same shape as the generated-union guard in
// refusalcodes_test.go.
func TestTroubleshootingCoversRefusalCodes(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join(moduleRoot(t), "docs", "troubleshooting.md"))
	if err != nil {
		t.Fatal(err)
	}
	entries, order := troubleshootingEntries(t, string(raw))
	registered := core.RefusalCodes()
	checkSubset(t, "troubleshooting entry", order, "registered refusal code", registered)
	checkIncludes(t, "troubleshooting doc", order, "registered refusal code", registered)
	checkEntryOrder(t, order)
	for _, code := range registered {
		if body, ok := entries[code]; ok {
			checkEntryBody(t, code, body)
		}
	}
}

// troubleshootingEntries splits the doc at its entry headings, returning each
// entry's body keyed by code plus the heading order for the sortedness check.
func troubleshootingEntries(t *testing.T, doc string) (map[string]string, []string) {
	t.Helper()
	entries := map[string]string{}
	var order []string
	for _, entry := range splitTroubleshooting(doc) {
		if _, dup := entries[entry.code]; dup {
			t.Errorf("docs/troubleshooting.md repeats the %q entry", entry.code)
		}
		entries[entry.code] = entry.body
		order = append(order, entry.code)
	}
	if len(entries) == 0 {
		t.Fatal("docs/troubleshooting.md carries no entry headings; the guard would pass vacuously")
	}
	return entries, order
}

// troubleshootingEntry is one doc section: the code its heading names and the
// lines under it.
type troubleshootingEntry struct {
	code string
	body string
}

// entryAccum gathers sections in doc order; flush closes the open one.
type entryAccum struct {
	out     []troubleshootingEntry
	current string
	body    []string
}

// flush closes the open section, if any.
func (a *entryAccum) flush() {
	if a.current == "" {
		return
	}
	a.out = append(a.out, troubleshootingEntry{a.current, strings.Join(a.body, "\n")})
}

// splitTroubleshooting cuts the doc at its entry headings, in doc order.
func splitTroubleshooting(doc string) []troubleshootingEntry {
	a := &entryAccum{}
	for line := range strings.SplitSeq(doc, "\n") {
		if m := troubleshootingHeading.FindStringSubmatch(line); m != nil {
			a.flush()
			a.current, a.body = m[1], nil
			continue
		}
		if a.current != "" {
			a.body = append(a.body, line)
		}
	}
	a.flush()
	return a.out
}

// checkEntryOrder fails when the entries leave registry order, so the diff of
// a newly registered code lands where a reader expects it.
func checkEntryOrder(t *testing.T, order []string) {
	t.Helper()
	if !slices.IsSorted(order) {
		t.Error("docs/troubleshooting.md entries are not sorted; keep registry order")
	}
}

// checkEntryBody fails when an entry carries no cause or no fix, so a bare
// heading cannot satisfy the coverage check.
func checkEntryBody(t *testing.T, code, body string) {
	t.Helper()
	if !hasPrefixedLine(body, "Cause: ") {
		t.Errorf("docs/troubleshooting.md entry %q has no Cause: line", code)
	}
	if !hasPrefixedLine(body, "Fix: ") {
		t.Errorf("docs/troubleshooting.md entry %q has no Fix: line", code)
	}
}

// hasPrefixedLine reports whether body carries a non-empty line under prefix.
func hasPrefixedLine(body, prefix string) bool {
	for line := range strings.SplitSeq(body, "\n") {
		if v, ok := strings.CutPrefix(line, prefix); ok && strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

// TestTroubleshootingHeadingsPinTheParser guards the parser above against
// silent drift: a heading-shaped line without backticks must not count, and
// the two real headings must parse in doc order with their bodies attached.
func TestTroubleshootingHeadingsPinTheParser(t *testing.T) {
	t.Parallel()
	doc := "## b-code is prose, not an entry\n\n## `b-code`\n\nCause: x\nFix: y\n## `a-code`\n\nCause: x\nFix: y\n"
	entries, order := troubleshootingEntries(t, doc)
	if !slices.Equal(order, []string{"b-code", "a-code"}) {
		t.Errorf("order = %v; want the two fixture headings in doc order", order)
	}
	for _, code := range []string{"a-code", "b-code"} {
		body, ok := entries[code]
		if !ok {
			t.Errorf("entries miss %q", code)
			continue
		}
		checkEntryBody(t, code, body)
	}
}
