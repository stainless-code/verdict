package evidence_test

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

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

func TestIsTestOutputPathA8b(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path string
		want bool
	}{
		// Denied path segments.
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
		// Test source is code: a review may cite the regression test it added.
		{path: "internal/evidence/quote_test.go", want: false},
		{path: "src/components/button.test.ts", want: false},
		{path: "src/components/button.spec.tsx", want: false},
		{path: "pkg/UPPER_TEST.PY", want: false},
		{path: "test/helper.go", want: false},
		{path: "pkg/tests/runner.py", want: false},
		{path: "web/__tests__/util.js", want: false},
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
		"+Tokens expire after 15 minutes.\n" +
		"diff --git a/testdata/fixture.json b/testdata/fixture.json\n" +
		"--- /dev/null\n" +
		"+++ b/testdata/fixture.json\n" +
		"@@ -0,0 +1,2 @@\n" +
		"+{\n" +
		"+  \"token\": \"expired\"\n" +
		"diff --git a/ui/out.golden b/ui/out.golden\n" +
		"--- a/ui/out.golden\n" +
		"+++ b/ui/out.golden\n" +
		"@@ -1,1 +1,2 @@\n" +
		" modal open\n" +
		"+modal closed\n"

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

	t.Run("test source citation is code evidence", func(t *testing.T) {
		t.Parallel()
		oneItem := evidence.EnumerateSpecItems([]evidence.SpecBlock{
			{Index: 0, Text: "- Reject expired tokens\n"},
		})
		checks := []evidence.SpecCheckInput{{
			Item:         "- Reject expired tokens",
			Status:       "met",
			EvidencePath: "auth_test.go",
			Line:         2,
			Quote:        "func TestCheckToken() {}",
		}}
		got := evidence.VerifySpecChecks(idx, oneItem, checks, 80)
		assertSpecCoverage(t, &got, 1, 1, true, true)
		if !got.Checks[0].Valid || got.Checks[0].Reason != "" || got.Checks[0].QuoteReason != "" {
			t.Fatalf("SpecCheckResult = %+v, want the test source accepted as code", got.Checks[0])
		}
	})

	t.Run("test output citation is refused", func(t *testing.T) {
		t.Parallel()
		checks := []evidence.SpecCheckInput{
			{
				Item:         "- Reject expired tokens",
				Status:       "met",
				EvidencePath: "testdata/fixture.json",
				Line:         2,
				Quote:        `  "token": "expired"`,
			},
			{
				Item:         "- Document token expiry",
				Status:       "met",
				EvidencePath: "ui/out.golden",
				Line:         2,
				Quote:        "modal closed",
			},
		}
		got := evidence.VerifySpecChecks(idx, items, checks, 80)
		assertSpecCoverage(t, &got, 5, 0, false, false)
		assertSpecCheckReason(t, &got.Checks[0], "spec-evidence-not-code", "spec-evidence-not-code")
		assertSpecCheckReason(t, &got.Checks[1], "spec-evidence-not-code", "spec-evidence-not-code")
	})

	t.Run("quote mismatch and unknown item refused", func(t *testing.T) {
		t.Parallel()
		checks := []evidence.SpecCheckInput{
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
		assertSpecCheckReason(t, &got.Checks[0], "spec-evidence-mismatch", "text-differs")
		assertSpecCheckReason(t, &got.Checks[1], "spec-item-unknown", "")
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

// TestEnumerateSpecItemsTableAndQuoteRules proves R6: a GFM table needs its
// header plus separator row, pipes in prose and bullets are text, and quoted
// lines are someone else's specification rather than this one's.
func TestEnumerateSpecItemsTableAndQuoteRules(t *testing.T) {
	t.Parallel()
	text := "" +
		"Prose naming a|b and c||d stays prose.\n" +
		"| Requirement | Owner |\n" +
		"| --- | --- |\n" +
		"|  Return 401  on expired token  |  auth  |\n" +
		"| Log request ID | api |\n" +
		"\n" +
		"> - Quoted bullet is not an item\n" +
		"> | Quoted | Table |\n" +
		"> | --- | --- |\n" +
		"> | Quoted | Row |\n" +
		"> ```md\n" +
		"> - Quoted fence content is not an item\n" +
		"- The real item | with a pipe\n" +
		"  continues | here\n" +
		"- After the quote and the table\n"

	got := evidence.EnumerateSpecItems([]evidence.SpecBlock{{Index: 0, Text: text}})
	want := []evidence.SpecItem{
		{ID: "0:4", Text: "Return 401 on expired token | auth", BlockIndex: 0, Line: 4},
		{ID: "0:5", Text: "Log request ID | api", BlockIndex: 0, Line: 5},
		{ID: "0:13", Text: "- The real item | with a pipe continues | here", BlockIndex: 0, Line: 13},
		{ID: "0:15", Text: "- After the quote and the table", BlockIndex: 0, Line: 15},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("EnumerateSpecItems:\ngot  = %+v\nwant = %+v", got, want)
	}
}

// renamedToTestOutputDiff renames source to test output: the bundle resolves
// the NEW path, the OLD one still reading as production source.
const renamedToTestOutputDiff = "" +
	"diff --git a/pkg/helper.go b/pkg/testdata/helper.json\n" +
	"similarity index 80%\n" +
	"rename from pkg/helper.go\n" +
	"rename to pkg/testdata/helper.json\n" +
	"--- a/pkg/helper.go\n" +
	"+++ b/pkg/testdata/helper.json\n" +
	"@@ -1,2 +1,2 @@\n" +
	" package helper\n" +
	"-\tfunc Probe() {}\n" +
	"+\tfunc Probe() {}\n"

// TestVerifySpecChecksRenamedOntoTestOutputIsNotCode proves R5: a citation of
// the OLD name of a rename onto test output is refused, because the bundle
// resolves the path to testdata.
func TestVerifySpecChecksRenamedOntoTestOutputIsNotCode(t *testing.T) {
	t.Parallel()
	idx := evidence.IndexDiff([]byte(renamedToTestOutputDiff))
	items := evidence.EnumerateSpecItems([]evidence.SpecBlock{
		{Index: 0, Text: "- Prove the probe\n"},
	})
	checks := []evidence.SpecCheckInput{{
		Item:         "- Prove the probe",
		Status:       "met",
		EvidencePath: "pkg/helper.go",
		Line:         2,
		Quote:        "func Probe() {}",
	}}
	got := evidence.VerifySpecChecks(idx, items, checks, 80)
	if len(got.Checks) != 1 {
		t.Fatalf("checks = %+v, want one", got.Checks)
	}
	assertSpecCheckReason(t, &got.Checks[0], "spec-evidence-not-code", "spec-evidence-not-code")
}

// TestVerifySpecChecksAmbiguousPathReason proves R12: a citation that matches
// more than one diff section reports the ambiguous path rather than claiming
// the path is absent.
func TestVerifySpecChecksAmbiguousPathReason(t *testing.T) {
	t.Parallel()
	// Two sections land on the same NEW path: a bundle that repeated a file.
	diff := "" +
		"diff --git a/docs/spec.md b/docs/spec.md\n" +
		"--- a/docs/spec.md\n" +
		"+++ b/docs/spec.md\n" +
		"@@ -1,1 +1,1 @@\n" +
		"-first\n" +
		"+first addition\n" +
		"diff --git a/docs/other.md b/docs/spec.md\n" +
		"--- a/docs/other.md\n" +
		"+++ b/docs/spec.md\n" +
		"@@ -5,1 +5,1 @@\n" +
		"-second\n" +
		"+second addition\n"
	idx := evidence.IndexDiff([]byte(diff))
	items := evidence.EnumerateSpecItems([]evidence.SpecBlock{
		{Index: 0, Text: "- Document the addition\n"},
	})
	checks := []evidence.SpecCheckInput{{
		Item:         "- Document the addition",
		Status:       "met",
		EvidencePath: "docs/spec.md",
		Line:         1,
		Quote:        "first addition",
	}}
	got := evidence.VerifySpecChecks(idx, items, checks, 80)
	if len(got.Checks) != 1 {
		t.Fatalf("checks = %+v, want one", got.Checks)
	}
	assertSpecCheckReason(t, &got.Checks[0], "spec-evidence-not-code", "ambiguous-path")
}

// goalCase is one prose block and the one item it is supposed to state.
type goalCase struct {
	want *evidence.SpecItem
	name string
	text string
}

// assertGoal proves the block yields exactly the expected goal item, or no item
// at all when want is nil.
func assertGoal(t *testing.T, text string, want *evidence.SpecItem) {
	t.Helper()
	got := evidence.EnumerateSpecItems([]evidence.SpecBlock{{Index: 0, Text: text}})
	if want == nil {
		if len(got) != 0 {
			t.Fatalf("items = %+v, want none", got)
		}
		return
	}
	if len(got) != 1 {
		t.Fatalf("items = %+v, want one goal item", got)
	}
	if got[0] != *want {
		t.Fatalf("item = %+v, want %+v", got[0], *want)
	}
}

// runGoalCases runs one table of prose blocks through assertGoal.
func runGoalCases(t *testing.T, cases []goalCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assertGoal(t, c.text, c.want)
		})
	}
}

// TestEnumerateSpecItemsGoalFromHeading proves a titled source states its goal
// in the title: without the synthesis an ordinary prose specification is a
// requirement-free source that satisfies every gate while asking for nothing.
func TestEnumerateSpecItemsGoalFromHeading(t *testing.T) {
	t.Parallel()
	runGoalCases(t, []goalCase{
		{
			name: "heading beats prose",
			text: "Some introduction.\n\n## Retry budget\n\nA 429 must reset it.\n",
			want: &evidence.SpecItem{ID: "0:3", Text: "Retry budget", BlockIndex: 0, Line: 3},
		},
		{
			name: "level one heading closes its hashes",
			text: "# Bounded retries ###\n\nProse.\n",
			want: &evidence.SpecItem{ID: "0:1", Text: "Bounded retries", BlockIndex: 0, Line: 1},
		},
	})
}

// TestEnumerateSpecItemsGoalFromProse proves an untitled source falls back to
// its first line that carries prose, and that quoted text, a fence body, and
// table scaffolding are never prose.
func TestEnumerateSpecItemsGoalFromProse(t *testing.T) {
	t.Parallel()
	runGoalCases(t, []goalCase{
		{
			name: "first prose line",
			text: "Tokens must stop working 15 minutes after issue.\n\nNothing enumerates here.\n",
			want: &evidence.SpecItem{ID: "0:1", Text: "Tokens must stop working 15 minutes after issue.", BlockIndex: 0, Line: 1},
		},
		{
			name: "blockquote is not prose",
			text: "> quoted specification\n> more quote\n\nReset the budget on 429.\n",
			want: &evidence.SpecItem{ID: "0:4", Text: "Reset the budget on 429.", BlockIndex: 0, Line: 4},
		},
		{
			name: "fence body is not prose",
			text: "```sh\nexpire tokens after 15m\n```\n\nExpire tokens after 15 minutes.\n",
			want: &evidence.SpecItem{ID: "0:5", Text: "Expire tokens after 15 minutes.", BlockIndex: 0, Line: 5},
		},
		{
			name: "table scaffolding is not prose",
			text: "| Requirement | Owner |\n| --- | --- |\n\nThe real goal.\n",
			want: &evidence.SpecItem{ID: "0:4", Text: "The real goal.", BlockIndex: 0, Line: 4},
		},
		{
			name: "no prose at all",
			text: "> only a quote\n\n```sh\nrm -rf /\n```\n",
			want: nil,
		},
	})
}

// TestEnumerateSpecItemsGoalIsCapped proves one long prose line cannot become a
// whole document of requirement text, and that the cut lands on a rune
// boundary so the quoted item still matches what the agent is told to check.
func TestEnumerateSpecItemsGoalIsCapped(t *testing.T) {
	t.Parallel()
	const capped = 300
	text := strings.Repeat("a", capped-1) + "é" + strings.Repeat("tail", 50)
	got := evidence.EnumerateSpecItems([]evidence.SpecBlock{{Index: 0, Text: text}})
	if len(got) != 1 {
		t.Fatalf("items = %+v, want one goal item", len(got))
	}
	want := strings.Repeat("a", capped-1) + "é"
	if got[0].Text != want {
		t.Fatalf("item = %q, want %q", got[0].Text, want)
	}
	if n := utf8.RuneCountInString(got[0].Text); n != capped {
		t.Errorf("item = %d runes, want %d", n, capped)
	}
}

// unmetNAFixture is the three-item checklist and the one-hunk diff the
// unmet/n-a cases grade: a met row quotes auth.go:2.
func unmetNAFixture() (*evidence.DiffIndex, []evidence.SpecItem) {
	diff := "" +
		"diff --git a/auth.go b/auth.go\n" +
		"--- a/auth.go\n" +
		"+++ b/auth.go\n" +
		"@@ -1,2 +1,3 @@\n" +
		" package auth\n" +
		"+func CheckToken() error { return ErrExpired }\n" +
		" // end\n"
	items := evidence.EnumerateSpecItems([]evidence.SpecBlock{
		{Index: 0, Text: "- Reject expired tokens\n- Reset the retry budget\n- Rotate the signing key\n"},
	})
	return evidence.IndexDiff([]byte(diff)), items
}

// TestVerifySpecChecksUnmetAndNAVerify proves an unmet row and an n/a row — the
// two statuses with no diff line to cite — verify as covered items carrying no
// evidence at all.
func TestVerifySpecChecksUnmetAndNAVerify(t *testing.T) {
	t.Parallel()
	idx, items := unmetNAFixture()
	got := evidence.VerifySpecChecks(idx, items, []evidence.SpecCheckInput{
		{
			Item: "- Reject expired tokens", Status: "met", EvidencePath: "auth.go", Line: 2,
			Quote: "func CheckToken() error { return ErrExpired }",
		},
		{Item: "- Reset the retry budget", Status: "unmet"},
		{Item: "- Rotate the signing key", Status: "n/a"},
	}, 0)
	assertSpecCoverage(t, &got, 3, 3, true, true)
	for _, chk := range got.Checks[1:] {
		if !chk.Valid || chk.Reason != "" {
			t.Errorf("check %+v, want valid with no reason", chk)
		}
	}
}

// TestVerifySpecChecksUnmetAndNARejectEvidence proves either status refuses a
// path, a quote, or a line: each is a claim about a diff line that cannot exist
// for a requirement nothing implements or one that does not apply.
func TestVerifySpecChecksUnmetAndNARejectEvidence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, status string
		chk          evidence.SpecCheckInput
	}{
		{"unmet path", "unmet", evidence.SpecCheckInput{EvidencePath: "auth.go"}},
		{"unmet quote", "unmet", evidence.SpecCheckInput{Quote: "func CheckToken() error { return ErrExpired }"}},
		{"unmet line", "unmet", evidence.SpecCheckInput{Line: 2}},
		{"n/a path", "n/a", evidence.SpecCheckInput{EvidencePath: "auth.go"}},
		{"n/a quote", "n/a", evidence.SpecCheckInput{Quote: "func CheckToken() error { return ErrExpired }"}},
		{"n/a line", "n/a", evidence.SpecCheckInput{Line: 2}},
	}
	for _, c := range cases {
		t.Run(c.name+" is refused", func(t *testing.T) {
			t.Parallel()
			idx, items := unmetNAFixture()
			chk := c.chk
			chk.Item, chk.Status = "- Reset the retry budget", c.status
			got := evidence.VerifySpecChecks(idx, items, []evidence.SpecCheckInput{chk}, 0)
			if len(got.Checks) != 1 {
				t.Fatalf("checks = %+v, want one", got.Checks)
			}
			assertSpecCheckReason(t, &got.Checks[0],
				"spec-evidence-unexpected", "spec-evidence-unexpected")
		})
	}
}
