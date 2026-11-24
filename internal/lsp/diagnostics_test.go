package lsp

import (
	"strings"
	"testing"
)

// TestSeverityTable pins finding severity → DiagnosticSeverity literally:
// untagged is a Hint, never inferred upward.
func TestSeverityTable(t *testing.T) {
	t.Parallel()
	for severity, want := range map[string]int{
		"critical": 1, "important": 2, "informational": 3, "": 4, "urgent": 4, "Critical": 4,
	} {
		if got := severityFor(severity); got != want {
			t.Errorf("severityFor(%q) = %d, want %d", severity, got, want)
		}
	}
}

// TestStatusTable pins which statuses publish and at what severity:
// deferred only on request and always as a Hint; addressed and triaged
// statuses never.
func TestStatusTable(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		status       string
		severity     string
		want         int
		showDeferred bool
		publishes    bool
	}{
		{"open", "critical", 1, false, true},
		{"open", "important", 2, true, true},
		{"open", "", 4, false, true},
		{"deferred", "critical", 0, false, false},
		{"deferred", "critical", 4, true, true},
		{"addressed", "critical", 0, true, false},
		{"wont-fix", "critical", 0, true, false},
		{"false-positive", "critical", 0, true, false},
		{"", "critical", 0, true, false},
	} {
		got, ok := publishable(tc.status, tc.severity, tc.showDeferred)
		if ok != tc.publishes || (ok && got != tc.want) {
			t.Errorf("publishable(%q, %q, %v) = %d, %v; want %d, %v",
				tc.status, tc.severity, tc.showDeferred, got, ok, tc.want, tc.publishes)
		}
	}
}

// TestMessageIsOnePlainLine pins the message shape: first body line,
// control and bidi runes dropped, capped, then the rung and any quote status
// other than verified.
func TestMessageIsOnePlainLine(t *testing.T) {
	t.Parallel()
	verified := record{Body: "Unbounded read.\nSecond line.", Rigor: "walked"}
	verified.Verification.Quote = "verified"
	rlo, pdf := string(rune(0x202E)), string(rune(0x202C))
	unverified := record{Body: "Bidi " + rlo + "evil" + pdf + " and\ttab \x07bell", Rigor: "said"}
	unverified.Verification.Quote = "unverified"
	for _, tc := range []struct {
		name string
		want string
		rec  record
	}{
		{"first line and rung", "Unbounded read. · rigor walked", verified},
		{"sanitized with quote status", "Bidi evil and tab bell · rigor said · quote unverified", unverified},
		{"empty body", "important finding c1111111 at notes.txt:2", record{Severity: "important", Sig: "c1111111aaaa", Locator: "notes.txt:2"}},
		{"empty untagged", "untagged finding abc at x:1", record{Sig: "abc", Locator: "x:1"}},
		{"capped", strings.Repeat("é", 159) + "…", record{Body: strings.Repeat("é", 200)}},
	} {
		if got := message(&tc.rec); got != tc.want {
			t.Errorf("%s: message = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestUTF16Len pins LSP's default position unit against literals: a BMP rune
// is one unit, an astral rune two.
func TestUTF16Len(t *testing.T) {
	t.Parallel()
	for s, want := range map[string]int{"": 0, "abc": 3, "é": 1, "日本": 2, "😀": 2, "a😀b": 4} {
		if got := utf16Len(s); got != want {
			t.Errorf("utf16Len(%q) = %d, want %d", s, got, want)
		}
	}
}

// TestDiagnosticRangeAndDrift pins the range forms and the drift check: a
// whole-line range without text; with text, an exact anchor tightens to the
// last line's UTF-16 end and span_matches compares the reviewed hash.
func TestDiagnosticRangeAndDrift(t *testing.T) {
	t.Parallel()
	exact, _ := joinFinding(&record{Sig: "s", Status: "open", Path: "notes.txt", Line: 2}, fixtureFiles())
	unmapped, _ := joinFinding(&record{Sig: "u", Status: "open", Path: "other.go", Line: 2}, fixtureFiles())
	yes, no := true, false
	for _, tc := range []struct {
		matches *bool
		f       *finding
		name    string
		lines   []string
		end     position
	}{
		{nil, &exact, "no text keeps the whole line", nil, position{Line: 2}},
		{&yes, &exact, "matching text tightens", []string{"top", "new line", "bottom"}, position{Line: 1, Character: 8}},
		{&no, &exact, "drifted text counts UTF-16", []string{"top", "new line 😀", "bottom"}, position{Line: 1, Character: 11}},
		{&no, &exact, "a short document cannot match", []string{"top"}, position{Line: 2}},
		{nil, &unmapped, "unmapped never tightens", []string{"a", "b"}, position{Line: 2}},
	} {
		d := diagnosticOf(tc.f, tc.lines)
		checkDiagnosticRange(t, tc.name, &d, tc.end, tc.matches)
	}
	same := diagnosticOf(&exact, []string{"top", "new line", "bottom"})
	if same.Data.SpanSHA256 != hashNewLine || same.Code != "s" || same.Source != "verdict" || same.Data.Bundle != "local-1" {
		t.Errorf("identity: %+v", same)
	}
}

func diagnosticOf(f *finding, lines []string) Diagnostic {
	root := newServedRoot("/r", false)
	return diagnostic(&root, &bundleState{bundle: "local-1", digest: "d"}, f, severityError, lines)
}

func checkDiagnosticRange(t *testing.T, name string, d *Diagnostic, end position, matches *bool) {
	t.Helper()
	if d.Range.Start.Line != 1 || d.Range.End != end {
		t.Errorf("%s: range %+v, want start line 1 end %+v", name, d.Range, end)
	}
	switch {
	case matches == nil && d.Data.SpanMatches != nil:
		t.Errorf("%s: span_matches = %v, want absent", name, *d.Data.SpanMatches)
	case matches != nil && (d.Data.SpanMatches == nil || *d.Data.SpanMatches != *matches):
		t.Errorf("%s: span_matches = %v, want %v", name, d.Data.SpanMatches, *matches)
	}
}

// TestDocumentLinesStripCR pins that a CRLF buffer hashes like an LF one.
func TestDocumentLinesStripCR(t *testing.T) {
	t.Parallel()
	doc := document{text: "top\r\nnew line\r\nbottom\r\n", hasText: true}
	if got := strings.Join(doc.lines(), "|"); got != "top|new line|bottom|" {
		t.Fatalf("lines = %q", got)
	}
}
