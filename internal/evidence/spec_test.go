package evidence_test

import (
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/evidence"
)

func TestEnumerateSpecItems(t *testing.T) {
	t.Parallel()
	block0 := "" +
		"# Spec Title\r\n" +
		"\r\n" +
		"- Must reject empty input\r\n" +
		"  with exit code 1\r\n" +
		"  - Nested bullet requirement\r\n" +
		"Plain paragraph is not a continuation.\r\n" +
		"\r\n" +
		"```markdown\r\n" +
		"- Bullet inside backtick fence must be ignored\r\n" +
		"1. Numbered inside fence must be ignored\r\n" +
		"| Ignored | Row |\r\n" +
		"```\r\n" +
		"~~~text\r\n" +
		"* Bullet inside tilde fence must be ignored\r\n" +
		"~~~\r\n" +
		"1. First numbered\u200b item\r\n" +
		"   continued on next line\r\n" +
		"2) Second numbered item\r\n" +
		"* Star bullet\r\n" +
		"+ Plus bullet\r\n" +
		"| Requirement | Owner |\r\n" +
		"| :--- | ---: |\r\n" +
		"|  Return 401   on expired token  |  auth  |\r\n" +
		"| Log request ID | api |\r\n" +
		"- Must reject empty input with exit code 1\r\n"

	block1 := "" +
		"- Must reject empty input with exit code 1\n" +
		"- Café non-ASCII   item\n" +
		"````md\n" +
		"```\n" +
		"- Still inside 4-backtick fence\n" +
		"```\n" +
		"````\n" +
		"- After 4-backtick fence\n"

	got := evidence.EnumerateSpecItems([]evidence.SpecBlock{
		{Index: 0, Text: block0},
		{Index: 1, Text: block1},
	})

	want := []evidence.SpecItem{
		{ID: "0:3", Text: "- Must reject empty input with exit code 1", BlockIndex: 0, Line: 3},
		{ID: "0:5", Text: "- Nested bullet requirement", BlockIndex: 0, Line: 5},
		{ID: "0:16", Text: "1. First numbered item continued on next line", BlockIndex: 0, Line: 16},
		{ID: "0:18", Text: "2) Second numbered item", BlockIndex: 0, Line: 18},
		{ID: "0:19", Text: "* Star bullet", BlockIndex: 0, Line: 19},
		{ID: "0:20", Text: "+ Plus bullet", BlockIndex: 0, Line: 20},
		{ID: "0:23", Text: "Return 401 on expired token | auth", BlockIndex: 0, Line: 23},
		{ID: "0:24", Text: "Log request ID | api", BlockIndex: 0, Line: 24},
		{ID: "1:2", Text: "- Café non-ASCII item", BlockIndex: 1, Line: 2},
		{ID: "1:8", Text: "- After 4-backtick fence", BlockIndex: 1, Line: 8},
	}

	if !slices.Equal(got, want) {
		t.Fatalf("EnumerateSpecItems:\ngot  = %+v\nwant = %+v", got, want)
	}
}

func TestIsTestOutputPathA8(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path string
		want bool
	}{
		// Denied basenames.
		{path: "internal/evidence/quote_test.go", want: true},
		{path: "src/components/button.test.ts", want: true},
		{path: "src/components/button.spec.tsx", want: true},
		{path: "pkg/UPPER_TEST.PY", want: true},
		// Denied path segments.
		{path: "test/helper.go", want: true},
		{path: "pkg/tests/runner.py", want: true},
		{path: "web/__tests__/util.js", want: true},
		{path: "internal/core/report/testdata/out.txt", want: true},
		{path: "evals/fixtures/sample.json", want: true},
		{path: "ui/snapshots/modal.txt", want: true},
		{path: "ui/__snapshots__/modal.txt", want: true},
		{path: `pkg\testdata\win.json`, want: true},
		// Denied extensions.
		{path: "internal/core/report/card.golden", want: true},
		{path: "ui/button.snap", want: true},
		{path: "build/output.log", want: true},
		{path: "build/RUN.LOG", want: true},
		// Allowed source, docs, and examples.
		{path: "internal/evidence/quote.go", want: false},
		{path: "internal/contest/runner.go", want: false},
		{path: "pkg/test.go", want: false},
		{path: "docs/manual.md", want: false},
		{path: "README.md", want: false},
		{path: "examples/basic/main.go", want: false},
		{path: "examples/walkthrough.md", want: false},
		{path: "bench/quality/fx1/spec.md", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			if got := evidence.IsTestOutputPath(tc.path); got != tc.want {
				t.Fatalf("IsTestOutputPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestVerifySpecChecksAndCoverage(t *testing.T) {
	t.Parallel()
	diff := "" +
		"diff --git a/auth.go b/auth.go\n" +
		"--- a/auth.go\n" +
		"+++ b/auth.go\n" +
		"@@ -1,2 +1,3 @@\n" +
		" package auth\n" +
		"+func CheckToken() error { return ErrExpired }\n" +
		" // end\n" +
		"diff --git a/auth_test.go b/auth_test.go\n" +
		"--- /dev/null\n" +
		"+++ b/auth_test.go\n" +
		"@@ -0,0 +1,2 @@\n" +
		"+package auth\n" +
		"+func TestCheckToken() {}\n" +
		"diff --git a/docs/manual.md b/docs/manual.md\n" +
		"--- a/docs/manual.md\n" +
		"+++ b/docs/manual.md\n" +
		"@@ -10,1 +10,2 @@\n" +
		" # Auth\n" +
		"+Tokens expire after 15 minutes.\n"

	idx := evidence.IndexDiff([]byte(diff))
	items := evidence.EnumerateSpecItems([]evidence.SpecBlock{
		{
			Index: 0,
			Text: "- Reject expired tokens\n" +
				"- Document token expiry\n" +
				"- Item 3\n" +
				"- Item 4\n" +
				"- Item 5\n",
		},
	})

	t.Run("met with code and doc change meets 80 percent threshold", func(t *testing.T) {
		t.Parallel()
		checks := []evidence.SpecCheckInput{
			{
				Item:         "- Reject expired tokens",
				Status:       "met",
				EvidencePath: "auth.go",
				Line:         2,
				Quote:        "func CheckToken() error { return ErrExpired }",
			},
			{
				Item:         "- Document token expiry",
				Status:       "met",
				EvidencePath: "docs/manual.md",
				Line:         11,
				Quote:        "Tokens expire after 15 minutes.",
			},
			{Item: "- Item 3", Status: "n/a"},
			{Item: "- Item 4", Status: "n/a"},
		}
		got := evidence.VerifySpecChecks(idx, items, checks, 80)
		assertSpecCoverage(t, &got, 5, 4, true, true)
		if len(got.Missing) != 1 || got.Missing[0].ID != "0:5" {
			t.Fatalf("Missing = %+v, want [0:5]", got.Missing)
		}
	})

	t.Run("under 80 percent fails coverage", func(t *testing.T) {
		t.Parallel()
		checks := []evidence.SpecCheckInput{
			{Item: "- Reject expired tokens", Status: "n/a"},
			{Item: "- Document token expiry", Status: "n/a"},
			{Item: "- Item 3", Status: "n/a"},
		}
		got := evidence.VerifySpecChecks(idx, items, checks, 80)
		assertSpecCoverage(t, &got, 5, 3, false, false)
	})

	t.Run("test file citation unknown item and quote mismatch refused", func(t *testing.T) {
		t.Parallel()
		checks := []evidence.SpecCheckInput{
			{
				Item:         "- Reject expired tokens",
				Status:       "met",
				EvidencePath: "auth_test.go",
				Line:         2,
				Quote:        "func TestCheckToken() {}",
			},
			{
				Item:         "- Document token expiry",
				Status:       "met",
				EvidencePath: "auth.go",
				Line:         2,
				Quote:        "wrong quote text",
			},
			{
				Item:   "- Reject expired",
				Status: "n/a",
			},
		}
		got := evidence.VerifySpecChecks(idx, items, checks, 80)
		assertSpecCoverage(t, &got, 5, 0, false, false)
		assertSpecCheckReason(t, &got.Checks[0], "spec-evidence-not-code", "")
		assertSpecCheckReason(t, &got.Checks[1], "spec-evidence-mismatch", "text-differs")
		assertSpecCheckReason(t, &got.Checks[2], "spec-item-unknown", "")
	})

	t.Run("zero items passes vacuously", func(t *testing.T) {
		t.Parallel()
		got := evidence.VerifySpecChecks(idx, nil, nil, 80)
		assertSpecCoverage(t, &got, 0, 0, true, true)
	})
}

func assertSpecCoverage(t *testing.T, got *evidence.SpecCoverage, wantTotal, wantCovered int, wantCovMet, wantSatisfied bool) {
	t.Helper()
	if got.Total != wantTotal || got.Covered != wantCovered || got.CoverageMet != wantCovMet || got.Satisfied != wantSatisfied {
		t.Fatalf("SpecCoverage = {Total:%d Covered:%d CoverageMet:%v Satisfied:%v}, want {Total:%d Covered:%d CoverageMet:%v Satisfied:%v}",
			got.Total, got.Covered, got.CoverageMet, got.Satisfied, wantTotal, wantCovered, wantCovMet, wantSatisfied)
	}
}

func assertSpecCheckReason(t *testing.T, got *evidence.SpecCheckResult, wantReason, wantQuoteReason string) {
	t.Helper()
	if got.Valid || got.Reason != wantReason || got.QuoteReason != wantQuoteReason {
		t.Fatalf("SpecCheckResult = %+v, want Valid=false Reason=%q QuoteReason=%q", got, wantReason, wantQuoteReason)
	}
}
