package run

import (
	"testing"
)

// TestSalvageParserProbes pins the byte-level shapes the fenced-block scanner
// has to survive. It is deliberately unit-shaped: salvage_test.go covers the
// run, record and refusal paths, and these cases only ask whether
// lastFindingsBlock finds a block at all.
//
// The stdout values are built by concatenation because a Go raw string cannot
// hold a backtick. Each case's bytes are a literal fenced document on stdout.
func TestSalvageParserProbes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		stdout string
		want   bool
	}{
		{
			// The doubled fence between the two documents must not let the
			// second one be swallowed as a stray close line.
			name: "nested fences",
			stdout: `
` + "```json" + `
{ "schema_version": "1", "findings": [{"path": "p", "line": 1, "claim": "c", "quote": "q", "severity": "critical", "confidence": "high"}] }
` + "```" + `
` + "```" + `
` + "```json" + `
{ "schema_version": "1", "findings": [] }
` + "```" + `
`,
			want: true,
		},
		{
			// A fence line carrying a CRLF terminator is still a fence, and the
			// body keeps its CRLF for the decoder to trim.
			name:   "CRLF",
			stdout: "```json\r\n{ \"schema_version\": \"1\", \"findings\": [] }\r\n```",
			want:   true,
		},
		{
			// A fence quoted inside an inline code span is noise, not an
			// opening fence, so the real block after it is still found.
			name: "json fence inside quoted block",
			stdout: `
Some text
` + "`" + "```json" + "`" + `
{...}
` + "`" + "```" + "`" + `
` + "```json" + `
{ "schema_version": "1", "findings": [] }
` + "```" + `
`,
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, got := lastFindingsBlock(tt.stdout)
			if got != tt.want {
				t.Errorf("lastFindingsBlock() = %v, want %v", got, tt.want)
			}
		})
	}
}
