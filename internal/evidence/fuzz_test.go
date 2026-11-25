package evidence_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/evidence"
)

// FuzzMatchQuote holds the diff indexer and A2 quote matcher to safety and
// vocabulary invariants over arbitrary diff bytes, paths, sides, line windows,
// and quote strings.
func FuzzMatchQuote(f *testing.F) {
	f.Add(
		"diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,3 +1,3 @@\n ctx\n-old\n+newLineWithLongText\n end\n",
		"a.go",
		"RIGHT",
		2,
		0,
		"newLineWithLongText",
	)
	f.Add(
		"diff --git a/del.go b/del.go\ndeleted file mode 100644\n--- a/del.go\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-a\n-b\n",
		"del.go",
		"LEFT",
		1,
		1,
		"a\nb",
	)
	f.Add(
		"diff --git \"a/h\\303\\251llo\" \"b/h\\303\\251llo\"\n@@ -1 +1 @@\n-old\n\\ No newline at end of file\n+new\n",
		"héllo",
		"",
		1,
		0,
		"new",
	)
	// A start_line past the line it bounds is range-invalid; nightly fuzzing
	// found that reason missing from the vocabulary below.
	f.Add(
		"diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,3 +1,3 @@\n ctx\n-old\n+newLineWithLongText\n end\n",
		"a.go",
		"RIGHT",
		2,
		31,
		"newLineWithLongText",
	)

	f.Fuzz(func(t *testing.T, diff, path, side string, line, startLine int, quote string) {
		idx := evidence.IndexDiff([]byte(diff))
		res := evidence.MatchQuote(idx, evidence.QuoteRequest{
			Path:      path,
			Side:      side,
			Line:      line,
			StartLine: startLine,
			Quote:     quote,
		})
		assertValidQuoteResult(t, res)
	})
}

// assertValidQuoteResult verifies that every MatchQuote outcome uses only the
// documented status and reason vocabulary.
func assertValidQuoteResult(t *testing.T, res evidence.QuoteResult) {
	t.Helper()
	switch res.Status {
	case "verified":
		if res.Reason != "" {
			t.Fatalf("verified result carried reason %q", res.Reason)
		}
		if res.MatchTo < res.MatchFrom {
			t.Fatalf("verified result has inverted match span %d..%d", res.MatchFrom, res.MatchTo)
		}
	case "unverified":
		if !isKnownQuoteReason(res.Reason) {
			t.Fatalf("unverified result has unknown reason %q", res.Reason)
		}
	default:
		t.Fatalf("unexpected status %q (never unchecked)", res.Status)
	}
}

// isKnownQuoteReason reports whether reason belongs to the A2/§2.4 vocabulary.
func isKnownQuoteReason(reason string) bool {
	switch reason {
	case "path-not-in-bundle",
		"ambiguous-path",
		"line-not-in-bundle",
		"file-absent-at-head",
		"range-crosses-hunks",
		"deleted-line",
		"quote-too-large",
		"text-differs",
		"range-invalid":
		return true
	default:
		return false
	}
}
