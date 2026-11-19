package recordcore_test

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/core/reviewdoc"
	"github.com/stainless-code/verdict/internal/jq"
)

const checkDiff = "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n" +
	"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"

const checkBody = "> ✅ marker plus enough length to pass the guard ok."

func checkReview(extra string) string {
	findings := `,"findings":[]`
	if strings.Contains(extra, `"findings"`) {
		findings = ""
	}
	summary := `,"summary":"A short summary of the clean review."`
	if strings.Contains(extra, `"summary"`) {
		summary = ""
	}
	return `{"schema_version":"1"` + findings + summary + extra + `}`
}

func checkComment(extra string) string {
	return `{"path":"notes.txt","line":2,"severity":"important",` +
		`"claim":"A finding with enough substance.","quote":"new line","confidence":"high"` + extra + `}`
}

const findingsReview = `{"schema_version":"1","findings":[` +
	`{"path":"notes.txt","line":2,"severity":"important","claim":"A real defect in the changed line.","quote":"new line","confidence":"high","rigor":"pointed"}` +
	`]}`

func TestCheckReviewAcceptsFindingsDocument(t *testing.T) {
	res := recordcore.CheckReview([]byte(findingsReview), checkDiff)
	if res.Errors != 0 || res.Warnings != 0 {
		t.Fatalf("CheckReview = %+v, want a clean findings review", res.Problems)
	}
	if res.Summary.Findings != 1 {
		t.Fatalf("finding count = %d, want 1", res.Summary.Findings)
	}
}

// TestFindingsContractMapping pins the five places the findings-only contract
// differs from the old review document: the closed-schema refusal, claim to
// store body, finder confidence, strict anchors, and the stored spec/receipt
// arrays.

// TestOldShapeFailsTheClosedSchema is the only old-shape test in the tree: an
// approved/body/comments[] document refuses through the ordinary
// closed-schema problems, with no dedicated detector and no repair hint.
func TestOldShapeFailsTheClosedSchema(t *testing.T) {
	review := `{"schema_version":"1","body":"A long enough v1 review body.","approved":false,"comments":[]}`
	res := recordcore.CheckReview([]byte(review), checkDiff)
	for _, path := range []string{"$.approved", "$.body", "$.comments"} {
		if !hasProblem(res, path, core.ProblemCodeUnknownField) {
			t.Fatalf("problems = %+v, want an unknown-field error at %s", res.Problems, path)
		}
	}
	if !hasProblem(res, "$.findings", core.ProblemCodeMissingField) {
		t.Fatalf("problems = %+v, want a missing findings error", res.Problems)
	}
	if res.Errors == 0 {
		t.Fatalf("problems = %+v, want errors so bad-review-input refuses", res.Problems)
	}
}

// TestClaimBecomesStoreBody proves the claim is the stored body byte for byte.
func TestClaimBecomesStoreBody(t *testing.T) {
	finding := storedFinding(t, findingsReview)
	body, _ := finding.Get("body")
	if body != "A real defect in the changed line." {
		t.Fatalf("stored body = %v, want the claim", body)
	}
}

// TestFinderConfidenceLeavesQuoteStanding proves the input confidence is stored
// as finder_confidence and that loading a review invents no quote standing: the
// bundle evidence pass stamps verified or unverified from the diff bytes.
func TestFinderConfidenceLeavesQuoteStanding(t *testing.T) {
	finding := storedFinding(t, findingsReview)
	confidence, _ := finding.Get("finder_confidence")
	if confidence != "high" {
		t.Fatalf("finder_confidence = %v, want high", confidence)
	}
	if verification, has := finding.Get("verification"); has {
		t.Fatalf("loaded finding carries verification = %v, want the evidence pass to stamp it", verification)
	}
}

// TestFindingLineIsStrictInteger proves a quoted line refuses: the anchor is a
// number or it is a problem.
func TestFindingLineIsStrictInteger(t *testing.T) {
	review := strings.Replace(findingsReview, `"line":2`, `"line":"2"`, 1)
	res := recordcore.CheckReview([]byte(review), checkDiff)
	if !hasProblem(res, "$.findings[0].line", core.ProblemCodeWrongType) {
		t.Fatalf("problems = %+v, want a wrong-type line problem", res.Problems)
	}
}

// TestSpecChecksAndReceiptsSurvive proves the two evidence arrays reach the
// store typed, not as loose JSON.
func TestSpecChecksAndReceiptsSurvive(t *testing.T) {
	review := `{"schema_version":"1","findings":[],"spec_checks":[` +
		`{"item":"quoted item","status":"n/a","evidence":{"path":"notes.txt","line":2,"quote":"new line"}}],` +
		`"receipts":[{"hunk":"@@ -1 +1 @@","quote":"new line"}]}`
	obj, err := recordcore.LoadReviewBytes([]byte(review))
	if err != nil {
		t.Fatal(err)
	}
	if got := reviewdoc.SpecChecksTyped(obj); len(got) != 1 || got[0].Status != "n/a" {
		t.Fatalf("spec_checks = %+v, want one stored n/a check", got)
	}
	if got := reviewdoc.ReceiptsTyped(obj); len(got) != 1 || got[0].Quote != "new line" {
		t.Fatalf("receipts = %+v, want one stored quote", got)
	}
}

// storedFinding loads one review document and returns its first finding.
func storedFinding(t *testing.T, review string) *jq.Obj {
	t.Helper()
	obj, err := recordcore.LoadReviewBytes([]byte(review))
	if err != nil {
		t.Fatal(err)
	}
	return mustObj(t, mustFindings(t, obj)[0])
}

func TestCheckReviewRequiredFindingFields(t *testing.T) {
	for _, field := range []string{"line", "severity", "claim", "quote", "confidence"} {
		t.Run(field, func(t *testing.T) {
			review := strings.Replace(findingsReview, `,"`+field+`":`, `,"removed":`, 1)
			res := recordcore.CheckReview([]byte(review), checkDiff)
			if !hasProblem(res, "$.findings[0]."+field, core.ProblemCodeMissingField) {
				t.Fatalf("problems = %+v, want missing %s", res.Problems, field)
			}
		})
	}
}

func TestCheckReviewRefusesBadSpecStatus(t *testing.T) {
	review := `{"schema_version":"1","findings":[],"spec_checks":[` +
		`{"item":"item","status":"na","evidence":{"path":"notes.txt","line":2,"quote":"new line"}}]}`
	res := recordcore.CheckReview([]byte(review), checkDiff)
	if !hasProblem(res, "$.spec_checks[0].status", core.ProblemCodeBadEnum) {
		t.Fatalf("problems = %+v, want the n/a enum refusal", res.Problems)
	}
}

// TestCheckReviewCleanReviewNeedsSummary pins the shared rule record, run, and
// pack test all use: an empty findings array needs a non-empty summary.
func TestCheckReviewCleanReviewNeedsSummary(t *testing.T) {
	t.Parallel()
	res := recordcore.CheckReview([]byte(`{"schema_version":"1","findings":[]}`), checkDiff)
	if !hasProblem(res, "$.summary", core.ProblemCodeEmptyFindingsNeedsSummary) {
		t.Fatalf("problems = %+v, want the clean-review summary problem", res.Problems)
	}
	withSummary := recordcore.CheckReview([]byte(`{"schema_version":"1","findings":[],"summary":"Reviewed the diff; no defects found."}`), checkDiff)
	if len(withSummary.Problems) != 0 {
		t.Fatalf("problems = %+v, want a valid clean review", withSummary.Problems)
	}
	withFinding := recordcore.CheckReview([]byte(findingsReview), checkDiff)
	if hasProblem(withFinding, "$.summary", core.ProblemCodeEmptyFindingsNeedsSummary) {
		t.Fatalf("a review with findings does not need a summary: %+v", withFinding.Problems)
	}
}

func TestCheckReviewSummaryCap(t *testing.T) {
	review := `{"schema_version":"1","findings":[],"summary":"` + strings.Repeat("x", 2001) + `"}`
	res := recordcore.CheckReview([]byte(review), checkDiff)
	if !hasProblem(res, "$.summary", core.ProblemCodeOutOfRange) {
		t.Fatalf("problems = %+v, want a summary cap refusal", res.Problems)
	}
}

// TestCheckReviewSummaryCapCountsRunes proves the cap matches the schema's
// maxLength, which counts characters: 2000 two-byte runes are 4000 bytes and
// must pass.
func TestCheckReviewSummaryCapCountsRunes(t *testing.T) {
	runes := strings.Repeat("é", 2000)
	res := recordcore.CheckReview([]byte(checkReview(`,"summary":"`+runes+`"`)), checkDiff)
	if hasProblem(res, "$.summary", core.ProblemCodeOutOfRange) {
		t.Fatalf("problems = %+v, want 2000 characters accepted", res.Problems)
	}
	long := recordcore.CheckReview([]byte(checkReview(`,"summary":"`+runes+"é"+`"`)), checkDiff)
	if !hasProblem(long, "$.summary", core.ProblemCodeOutOfRange) {
		t.Fatalf("problems = %+v, want the 2001st character refused", long.Problems)
	}
}

// TestCheckReviewRefusesEmptyStrings proves the minLength: 1 fields refuse an
// empty value, not only a missing one.
func TestCheckReviewRefusesEmptyStrings(t *testing.T) {
	finding := func(claim, quote string) string {
		return `{"path":"notes.txt","line":2,"severity":"important","confidence":"high","rigor":"pointed",` +
			`"claim":"` + claim + `","quote":"` + quote + `"}`
	}
	cases := []struct {
		name   string
		review string
		path   string
	}{
		{"empty claim", checkReview(`,"findings":[` + finding("", "new line") + `]`), "$.findings[0].claim"},
		{"empty quote", checkReview(`,"findings":[` + finding("a real defect", "") + `]`), "$.findings[0].quote"},
		{
			"empty spec item and quote",
			checkReview(`,"findings":[],"spec_checks":[{"item":"","status":"met","evidence":{"path":"notes.txt","line":2,"quote":""}}]`),
			"$.spec_checks[0].item",
		},
		{
			"empty receipt hunk and quote",
			checkReview(`,"findings":[],"receipts":[{"hunk":"","quote":""}]`),
			"$.receipts[0].hunk",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := recordcore.CheckReview([]byte(tc.review), checkDiff)
			if !hasProblem(res, tc.path, core.ProblemCodeOutOfRange) {
				t.Fatalf("problems = %+v, want an empty-value refusal at %s", res.Problems, tc.path)
			}
		})
	}
}

func hasProblem(res recordcore.CheckResult, path, code string) bool {
	for _, p := range res.Problems {
		if p.Path == path && p.Code == code {
			return true
		}
	}
	return false
}

func mustFindings(t *testing.T, obj *jq.Obj) []any {
	t.Helper()
	value, _ := obj.Get("findings")
	items := jq.Elements(value)
	if len(items) == 0 {
		t.Fatal("review has no findings")
	}
	return items
}

func mustObj(t *testing.T, value any) *jq.Obj {
	t.Helper()
	obj, ok := value.(*jq.Obj)
	if !ok {
		t.Fatalf("value %T is not an object", value)
	}
	return obj
}

func checkProblems(t *testing.T, res recordcore.CheckResult, want int) {
	t.Helper()
	if len(res.Problems) != want {
		t.Fatalf("problems = %+v, want %d", res.Problems, want)
	}
}
