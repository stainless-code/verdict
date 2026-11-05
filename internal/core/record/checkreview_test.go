package recordcore_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
)

const checkDiff = "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n" +
	"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"

const checkBody = "> ✅ marker plus enough length to pass the guard ok."

func checkReview(extra string) string {
	return `{"schema_version":"1","body":"` + checkBody + `","approved":false` + extra + `}`
}

func checkComment(extra string) string {
	return `{"path":"notes.txt","line":2,"side":"RIGHT","body":"A finding with enough substance."` + extra + `}`
}

func checkProblems(t *testing.T, result recordcore.CheckResult, want int) {
	t.Helper()
	if len(result.Problems) != want {
		t.Fatalf("problems = %d, want %d: %+v", len(result.Problems), want, result.Problems)
	}
	for _, p := range result.Problems {
		if p.Path == "" || p.Code == "" || p.Level == "" || p.Message == "" || p.Fix == "" {
			t.Errorf("problem is missing a published field: %+v", p)
		}
	}
}

// TestCheckReviewCodeTable pins every published code, its path and level.
//
//nolint:revive // the code table reads as one block
func TestCheckReviewCodeTable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		code  string
		level string
		path  string
		json  string
	}{
		{
			name: "invalid-json", code: core.ProblemCodeInvalidJSON, level: core.ProblemLevelError, path: "$",
			json: "```json\n{\"body\":\"x\"}\n```",
		},
		{
			name: "wrong-type", code: core.ProblemCodeWrongType, level: core.ProblemLevelError,
			path: "$.comments[0].brief.questions",
			json: checkReview(`,"comments":[` + checkComment(`,"rigor":"walked","brief":{"outcome":"Reject duplicate deliveries before anything is enqueued.","questions":"why"}`) + `]`),
		},
		{
			name: "missing-field", code: core.ProblemCodeMissingField, level: core.ProblemLevelError,
			path: "$.comments[0].path",
			json: checkReview(`,"comments":[{"line":2,"body":"A finding with enough substance.","rigor":"walked"}]`),
		},
		{
			name: "bad-enum", code: core.ProblemCodeBadEnum, level: core.ProblemLevelError,
			path: "$.comments[0].rigor",
			json: checkReview(`,"comments":[` + checkComment(`,"rigor":"swam"`) + `]`),
		},
		{
			name: "path-not-in-diff", code: core.ProblemCodePathNotInDiff, level: core.ProblemLevelError,
			path: "$.comments[0].path",
			json: checkReview(`,"comments":[{"path":"gone.txt","line":2,"body":"A finding with enough substance.","rigor":"walked"}]`),
		},
		{
			name: "unknown-field", code: core.ProblemCodeUnknownField, level: core.ProblemLevelWarning,
			path: "$.fyel",
			json: checkReview(`,"fyel":true`),
		},
		{
			name: "anchor-outside-hunk", code: core.ProblemCodeAnchorOutsideHunk, level: core.ProblemLevelWarning,
			path: "$.comments[0].line",
			json: checkReview(`,"comments":[{"path":"notes.txt","line":99,"body":"A finding with enough substance.","rigor":"walked"}]`),
		},
		{
			name: "rigor-unset", code: core.ProblemCodeRigorUnset, level: core.ProblemLevelWarning,
			path: "$.comments[0].rigor",
			json: checkReview(`,"comments":[` + checkComment("") + `]`),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := recordcore.CheckReview([]byte(tc.json), checkDiff)
			checkProblems(t, result, 1)
			p := result.Problems[0]
			if p.Code != tc.code || p.Level != tc.level || p.Path != tc.path {
				t.Errorf("problem = %+v, want code %s level %s path %s", p, tc.code, tc.level, tc.path)
			}
			if tc.code == core.ProblemCodeInvalidJSON && !strings.Contains(p.Message, "invalid JSON at 1:") {
				t.Errorf("invalid-json message lacks line:col: %q", p.Message)
			}
		})
	}
}

// TestCheckReviewBriefQuestionsString is the acceptance fixture: a string
// where questions[] belongs reports exactly one problem with a fix and an
// example at that JSONPath.
func TestCheckReviewBriefQuestionsString(t *testing.T) {
	t.Parallel()
	review := checkReview(`,"comments":[` + checkComment(`,"rigor":"walked","brief":{"outcome":"Reject duplicate deliveries before anything is enqueued.","questions":"why"}`) + `]`)
	result := recordcore.CheckReview([]byte(review), checkDiff)
	checkProblems(t, result, 1)
	p := result.Problems[0]
	if p.Path != "$.comments[0].brief.questions" || p.Code != core.ProblemCodeWrongType {
		t.Errorf("problem = %+v, want wrong-type at the questions path", p)
	}
	if p.Fix == "" || p.Example == nil {
		t.Errorf("problem needs a fix and an example: %+v", p)
	}
}

// TestCheckReviewCollectsEveryProblem proves independent errors all arrive in
// one pass, in document order.
func TestCheckReviewCollectsEveryProblem(t *testing.T) {
	t.Parallel()
	review := `{"schema_version":"1","body":"` + checkBody + `","approved":false,` +
		`"comments":[{"path":7,"line":2,"body":"A finding.","rigor":"walked"},` +
		`{"path":"notes.txt","line":2,"rigor":"walked"}],` +
		`"resolved":[3],"sections":[5]}`
	result := recordcore.CheckReview([]byte(review), checkDiff)
	checkProblems(t, result, 4)
	want := []string{"$.comments[0].path", "$.comments[1].body", "$.resolved[0]", "$.sections[0]"}
	for i, path := range want {
		if result.Problems[i].Path != path {
			t.Errorf("problem %d path = %s, want %s", i, result.Problems[i].Path, path)
		}
	}
}

// TestCheckReviewCapsProblems proves 50 problems survive and the overflow is
// counted, not silently dropped.
func TestCheckReviewCapsProblems(t *testing.T) {
	t.Parallel()
	comments := make([]string, 0, 60)
	for i := range 60 {
		comments = append(comments, fmt.Sprintf(
			`{"path":"notes.txt","line":2,"body":"Finding %d.","rigor":"walked","zzz":%d}`, i, i))
	}
	result := recordcore.CheckReview([]byte(checkReview(`,"comments":[`+strings.Join(comments, ",")+`]`)), checkDiff)
	if len(result.Problems) != 50 {
		t.Fatalf("problems = %d, want 50", len(result.Problems))
	}
	if result.Truncated != 10 {
		t.Errorf("truncated = %d, want 10", result.Truncated)
	}
}

// TestCheckReviewSummary proves a clean input reports what a dry run would do.
func TestCheckReviewSummary(t *testing.T) {
	t.Parallel()
	review := checkReview(`,"comments":[` + checkComment(`,"rigor":"walked"`) +
		`,{"path":"notes.txt","line":99,"body":"Moves to the body.","rigor":"walked"}],` +
		`"sections":[{"heading":"### Security","severity":"important"}]`)
	result := recordcore.CheckReview([]byte(review), checkDiff)
	checkProblems(t, result, 1)
	if result.Problems[0].Code != core.ProblemCodeAnchorOutsideHunk {
		t.Fatalf("problem = %+v", result.Problems[0])
	}
	if result.Summary.Comments != 2 || result.Summary.Sections != 1 || result.Summary.AnchorsMovedToBody != 1 {
		t.Errorf("summary = %+v", result.Summary)
	}
}
