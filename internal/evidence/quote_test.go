package evidence_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/evidence"
)

func TestNormalizeQuote(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		in        string
		wantText  string
		wantLines []string
	}{
		{
			name:      "crlf zero-width space collapse and trim blank ends",
			in:        "\r\n  \t \r\n\tfoo\u200bbar\u00a0  baz  \r\n   \r\n  qux\t\tquux \r\n\r\n",
			wantText:  "foobar baz\n\nqux quux",
			wantLines: []string{"foobar baz", "", "qux quux"},
		},
		{
			name:      "only whitespace and zero-width spaces",
			in:        " \t\u200b\r\n  ",
			wantText:  "",
			wantLines: nil,
		},
		{
			name:      "preserves punctuation case and markdown fence literally",
			in:        "```go\nReturnVal(X, Y)\n```",
			wantText:  "```go\nReturnVal(X, Y)\n```",
			wantLines: []string{"```go", "ReturnVal(X, Y)", "```"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertNormalized(t, tc.in, tc.wantText, tc.wantLines)
		})
	}
}

func assertNormalized(t *testing.T, in, wantText string, wantLines []string) {
	t.Helper()
	gotText, gotLines := evidence.NormalizeQuote(in)
	if gotText != wantText {
		t.Fatalf("NormalizeQuote text = %q, want %q", gotText, wantText)
	}
	if !slices.Equal(gotLines, wantLines) {
		t.Fatalf("NormalizeQuote lines = %q, want %q", gotLines, wantLines)
	}
}

func TestMatchQuoteA2(t *testing.T) {
	t.Parallel()
	diff := "" +
		"diff --git a/pkg/service.go b/pkg/service.go\n" +
		"--- a/pkg/service.go\n" +
		"+++ b/pkg/service.go\n" +
		"@@ -10,12 +10,12 @@ func handle(ctx context.Context) error {\n" +
		" \tline10 := alpha()\n" +
		" \tline11 := beta()\n" +
		" \tline12 := gamma()\n" +
		" \tline13 := delta()\n" +
		"-\toldRemovedSecretCheck(userToken)\n" +
		"+\tresult := computePrimaryDigest(payload, salt)\n" +
		" \tif result == \"\" {\n" +
		" \t\treturn errEmptyDigest\n" +
		" \t}\n" +
		" \tline18 := omega()\n" +
		" \tline19 := zeta()\n" +
		" \tline20 := theta()\n" +
		" \tline21 := iota()\n" +
		"@@ -40,3 +40,3 @@ func cleanup() {\n" +
		" \tcloseChannel()\n" +
		"-\tflushLegacyBuffer()\n" +
		"+\tflushActiveBuffer()\n" +
		" \treturn\n" +
		"diff --git a/pkg/gone.go b/pkg/gone.go\n" +
		"deleted file mode 100644\n" +
		"--- a/pkg/gone.go\n" +
		"+++ /dev/null\n" +
		"@@ -1,2 +0,0 @@\n" +
		"-package gone\n" +
		"-func removedForever() {}\n"

	idx := evidence.IndexDiff([]byte(diff))

	tests := []struct {
		name       string
		wantStatus string
		wantReason string
		req        evidence.QuoteRequest
	}{
		{
			name: "exact single line default right side",
			req: evidence.QuoteRequest{
				Path:  "pkg/service.go",
				Line:  14,
				Quote: "result := computePrimaryDigest(payload, salt)",
			},
			wantStatus: "verified",
			wantReason: "",
		},
		{
			name: "normalization crlf u200b tabs and blank ends",
			req: evidence.QuoteRequest{
				Path:  "pkg/service.go",
				Line:  14,
				Quote: "\r\n  result\u200b :=\tcomputePrimaryDigest(payload,   salt) \r\n",
			},
			wantStatus: "verified",
			wantReason: "",
		},
		{
			name: "fabricated quote fails with text-differs",
			req: evidence.QuoteRequest{
				Path:  "pkg/service.go",
				Line:  14,
				Quote: "result := fabricatedFunction(payload)",
			},
			wantStatus: "unverified",
			wantReason: "text-differs",
		},
		{
			name: "off-by-3 above matches",
			req: evidence.QuoteRequest{
				Path:  "pkg/service.go",
				Line:  17,
				Quote: "result := computePrimaryDigest(payload, salt)",
			},
			wantStatus: "verified",
			wantReason: "",
		},
		{
			name: "off-by-3 below matches",
			req: evidence.QuoteRequest{
				Path:  "pkg/service.go",
				Line:  11,
				Quote: "result := computePrimaryDigest(payload, salt)",
			},
			wantStatus: "verified",
			wantReason: "",
		},
		{
			name: "off-by-4 above fails",
			req: evidence.QuoteRequest{
				Path:  "pkg/service.go",
				Line:  18,
				Quote: "result := computePrimaryDigest(payload, salt)",
			},
			wantStatus: "unverified",
			wantReason: "text-differs",
		},
		{
			name: "off-by-4 below fails",
			req: evidence.QuoteRequest{
				Path:  "pkg/service.go",
				Line:  10,
				Quote: "result := computePrimaryDigest(payload, salt)",
			},
			wantStatus: "unverified",
			wantReason: "text-differs",
		},
		{
			name: "short fragment 11 non-space chars fails as substring",
			req: evidence.QuoteRequest{
				Path:  "pkg/service.go",
				Line:  14,
				Quote: "computePrim",
			},
			wantStatus: "unverified",
			wantReason: "text-differs",
		},
		{
			name: "short fragment with spaces still only 11 non-space chars fails",
			req: evidence.QuoteRequest{
				Path:  "pkg/service.go",
				Line:  14,
				Quote: "result := com",
			},
			wantStatus: "unverified",
			wantReason: "text-differs",
		},
		{
			name: "short full line under 12 chars matches exactly",
			req: evidence.QuoteRequest{
				Path:  "pkg/service.go",
				Line:  17,
				Quote: "}",
			},
			wantStatus: "verified",
			wantReason: "",
		},
		{
			name: "long fragment 12 non-space chars matches as substring",
			req: evidence.QuoteRequest{
				Path:  "pkg/service.go",
				Line:  14,
				Quote: "computePrima",
			},
			wantStatus: "verified",
			wantReason: "",
		},
		{
			name: "multi-line partial first suffix exact interior partial last prefix without start_line",
			req: evidence.QuoteRequest{
				Path:  "pkg/service.go",
				Line:  16,
				Quote: "computePrimaryDigest(payload, salt)\nif result == \"\" {\nreturn errEmpty",
			},
			wantStatus: "verified",
			wantReason: "",
		},
		{
			name: "multi-line mismatched interior fails",
			req: evidence.QuoteRequest{
				Path:  "pkg/service.go",
				Line:  16,
				Quote: "computePrimaryDigest(payload, salt)\nif result != \"\" {\nreturn errEmpty",
			},
			wantStatus: "unverified",
			wantReason: "text-differs",
		},
		{
			name: "multi-line first line not a suffix fails",
			req: evidence.QuoteRequest{
				Path:  "pkg/service.go",
				Line:  15,
				Quote: "result := computePrimary\nif result == \"\" {",
			},
			wantStatus: "unverified",
			wantReason: "text-differs",
		},
		{
			name: "multi-line last line not a prefix fails",
			req: evidence.QuoteRequest{
				Path:  "pkg/service.go",
				Line:  16,
				Quote: "if result == \"\" {\nerrEmptyDigest",
			},
			wantStatus: "unverified",
			wantReason: "text-differs",
		},
		{
			name: "left side matches deleted line",
			req: evidence.QuoteRequest{
				Path:  "pkg/service.go",
				Side:  "LEFT",
				Line:  14,
				Quote: "oldRemovedSecretCheck(userToken)",
			},
			wantStatus: "verified",
			wantReason: "",
		},
		{
			name: "left side rejects added line",
			req: evidence.QuoteRequest{
				Path:  "pkg/service.go",
				Side:  "LEFT",
				Line:  14,
				Quote: "result := computePrimaryDigest(payload, salt)",
			},
			wantStatus: "unverified",
			wantReason: "text-differs",
		},
		{
			name: "deleted line quoted on right side returns deleted-line",
			req: evidence.QuoteRequest{
				Path:  "pkg/service.go",
				Side:  "RIGHT",
				Line:  14,
				Quote: "oldRemovedSecretCheck(userToken)",
			},
			wantStatus: "unverified",
			wantReason: "deleted-line",
		},
		{
			name: "quote crossing hunks with start_line returns range-crosses-hunks",
			req: evidence.QuoteRequest{
				Path:      "pkg/service.go",
				StartLine: 21,
				Line:      40,
				Quote:     "line21 := iota()\ncloseChannel()",
			},
			wantStatus: "unverified",
			wantReason: "range-crosses-hunks",
		},
		{
			name: "quote crossing hunks without start_line returns range-crosses-hunks",
			req: evidence.QuoteRequest{
				Path:  "pkg/service.go",
				Line:  40,
				Quote: "line21 := iota()\ncloseChannel()",
			},
			wantStatus: "unverified",
			wantReason: "range-crosses-hunks",
		},
		{
			name: "path not in bundle",
			req: evidence.QuoteRequest{
				Path:  "pkg/missing.go",
				Line:  1,
				Quote: "anything",
			},
			wantStatus: "unverified",
			wantReason: "path-not-in-bundle",
		},
		{
			name: "line not in bundle",
			req: evidence.QuoteRequest{
				Path:  "pkg/service.go",
				Line:  200,
				Quote: "result := computePrimaryDigest(payload, salt)",
			},
			wantStatus: "unverified",
			wantReason: "line-not-in-bundle",
		},
		{
			name: "deleted file on right returns file-absent-at-head",
			req: evidence.QuoteRequest{
				Path:  "pkg/gone.go",
				Side:  "RIGHT",
				Line:  2,
				Quote: "func removedForever() {}",
			},
			wantStatus: "unverified",
			wantReason: "file-absent-at-head",
		},
		{
			name: "deleted file on left verifies",
			req: evidence.QuoteRequest{
				Path:  "pkg/gone.go",
				Side:  "LEFT",
				Line:  2,
				Quote: "func removedForever() {}",
			},
			wantStatus: "verified",
			wantReason: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evidence.MatchQuote(idx, tc.req)
			if got.Status != tc.wantStatus || got.Reason != tc.wantReason {
				t.Fatalf("MatchQuote = {Status:%q Reason:%q}, want {Status:%q Reason:%q}",
					got.Status, got.Reason, tc.wantStatus, tc.wantReason)
			}
		})
	}
}

func TestMatchQuoteBounds(t *testing.T) {
	t.Parallel()
	var body strings.Builder
	body.WriteString("diff --git a/big.go b/big.go\n--- a/big.go\n+++ b/big.go\n@@ -0,0 +1,66 @@\n")
	lines64 := make([]string, 64)
	for i := range 64 {
		ln := "lineValue := " + strings.Repeat("x", 10)
		lines64[i] = ln
		body.WriteString("+" + ln + "\n")
	}
	body.WriteString("+" + strings.Repeat("a", 8192) + "\n")
	body.WriteString("+" + strings.Repeat("b", 8193) + "\n")

	idx := evidence.IndexDiff([]byte(body.String()))
	lines65 := append(slices.Clone(lines64), "extraLine")

	assertQuoteOutcome(t, idx, evidence.QuoteRequest{Path: "big.go", Line: 1, Quote: strings.Join(lines64, "\n")}, "verified", "")
	assertQuoteOutcome(t, idx, evidence.QuoteRequest{Path: "big.go", Line: 1, Quote: strings.Join(lines65, "\n")}, "unverified", "quote-too-large")
	assertQuoteOutcome(t, idx, evidence.QuoteRequest{Path: "big.go", Line: 65, Quote: strings.Repeat("a", 8192)}, "verified", "")
	assertQuoteOutcome(t, idx, evidence.QuoteRequest{Path: "big.go", Line: 66, Quote: strings.Repeat("b", 8193)}, "unverified", "quote-too-large")
}

func assertQuoteOutcome(t *testing.T, idx *evidence.DiffIndex, req evidence.QuoteRequest, wantStatus, wantReason string) {
	t.Helper()
	got := evidence.MatchQuote(idx, req)
	if got.Status != wantStatus || got.Reason != wantReason {
		t.Fatalf("MatchQuote = {Status:%q Reason:%q}, want {Status:%q Reason:%q}",
			got.Status, got.Reason, wantStatus, wantReason)
	}
}
