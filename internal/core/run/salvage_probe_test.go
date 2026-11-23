package run

import (
	"testing"
)

// A backtick run cannot live inside a Go raw string, so the fenced lines below
// are assembled from these pieces.
const (
	jsonFence  = "```json"
	fenceClose = "```"
	// inlineTick delimits a CommonMark inline code span whose content is
	// ```json: a backtick run three wide nested inside one four wide.
	inlineTick = "````"
)

// TestSalvageParserProbes pins the byte-level shapes the fenced-block scanner
// has to survive. It is deliberately unit-shaped: salvage_test.go covers the
// run, record and refusal paths, and these cases ask what lastFindingsBlock
// hands back for a document the agent printed but never saved.
//
// Each want is the documented contract, not a readback of the scanner: a block
// that already carries schema_version comes back as the trimmed original bytes.
func TestSalvageParserProbes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		stdout string
		want   string
	}{
		{
			// The doubled fence between the two documents must not let the
			// second one be swallowed as a stray close line. Both documents
			// are valid, so the expected bytes are also what separates a
			// backward scan from one that returns the first hit.
			name: "nested fences",
			stdout: "\n" +
				jsonFence + "\n" +
				`{ "schema_version": "1", "findings": [{"path": "p", "line": 1, "claim": "c", "quote": "q", "severity": "critical", "confidence": "high"}] }` + "\n" +
				fenceClose + "\n" +
				fenceClose + "\n" +
				jsonFence + "\n" +
				`{ "schema_version": "1", "findings": [] }` + "\n" +
				fenceClose + "\n",
			want: `{ "schema_version": "1", "findings": [] }`,
		},
		{
			// A fence line carrying a CRLF terminator is still a fence, and
			// the body keeps its CRLF for the trim to remove.
			name:   "CRLF",
			stdout: jsonFence + "\r\n" + `{ "schema_version": "1", "findings": [] }` + "\r\n" + fenceClose,
			want:   `{ "schema_version": "1", "findings": [] }`,
		},
		{
			// A fence quoted inside an inline code span is noise, not an
			// opening fence, so the real block after it is still found. A
			// second quoted opening, this time followed by a document and a
			// stray close fence, must not be captured either: the scanner may
			// not return a block the agent never opened.
			name: "json fence inside quoted block",
			stdout: "\nSome text\n" +
				inlineTick + " " + jsonFence + " " + inlineTick + "\n" +
				"{...}\n" +
				inlineTick + " " + fenceClose + " " + inlineTick + "\n" +
				jsonFence + "\n" +
				`{ "schema_version": "1", "findings": [{"path": "p", "line": 1, "claim": "c", "quote": "q", "severity": "critical", "confidence": "high"}] }` + "\n" +
				fenceClose + "\n" +
				"A quoted opening and a plain close: " + inlineTick + " " + jsonFence + " " + inlineTick + "\n" +
				`{ "schema_version": "1", "findings": [] }` + "\n" +
				fenceClose + "\n",
			want: `{ "schema_version": "1", "findings": [{"path": "p", "line": 1, "claim": "c", "quote": "q", "severity": "critical", "confidence": "high"}] }`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := lastFindingsBlock(tt.stdout)
			assertSalvagedDocument(t, tt.stdout, got, ok, tt.want)
		})
	}
}

// assertSalvagedDocument pins the exact bytes the scanner returns. A boolean
// alone cannot tell a first-block scanner from a last-block one, so the
// document itself is the assertion.
func assertSalvagedDocument(t *testing.T, stdout string, got []byte, ok bool, want string) {
	t.Helper()
	if !ok {
		t.Fatalf("lastFindingsBlock(%q) found no document, want %q", stdout, want)
	}
	if string(got) != want {
		t.Errorf("lastFindingsBlock(%q) = %q, want %q", stdout, got, want)
	}
}
