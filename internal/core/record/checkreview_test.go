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
	comments := `,"comments":[]`
	if strings.Contains(extra, `"comments"`) {
		comments = ""
	}
	return `{"schema_version":"1","body":"` + checkBody + `","approved":false` + comments + extra + `}`
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

// TestCheckReviewCoversBundleInputs pins one problem for every input family
// record would otherwise validate only while merging: brief parts, anchors and
// sides, kb ids, chunk attestation, confidence, claims, triage, sections, and
// verification claims.
//
//nolint:revive // the bundle-input matrix reads as one block
func TestCheckReviewCoversBundleInputs(t *testing.T) {
	t.Parallel()
	comment := func(extra string) string {
		return checkReview(`,"comments":[` + checkComment(`,"rigor":"walked"`+extra) + `]`)
	}
	briefOutcome := `"outcome":"Reject duplicate deliveries before anything is enqueued."`
	//nolint:govet // table grouping keeps the case fields readable
	tests := []struct {
		name  string
		json  string
		input recordcore.CheckInput
		code  string
		level string
		path  string
	}{
		{
			name: "schema-missing", json: `{"body":"` + checkBody + `","approved":false}`,
			code: core.ProblemCodeMissingField, level: core.ProblemLevelWarning, path: "$.schema_version",
		},
		{
			name: "brief-outcome-missing", json: comment(`,"brief":{"sites":[]}`),
			code: core.ProblemCodeMissingField, level: core.ProblemLevelError, path: "$.comments[0].brief.outcome",
		},
		{
			name: "brief-site-outside-diff", json: comment(`,"brief":{` + briefOutcome + `,"sites":[{"path":"gone.txt","line":2}]}`),
			code: core.ProblemCodePathNotInDiff, level: core.ProblemLevelWarning, path: "$.comments[0].brief.sites[0].path",
		},
		{
			name: "brief-approach-short", json: comment(`,"brief":{` + briefOutcome + `,"approach":"tiny"}`),
			code: core.ProblemCodeOutOfRange, level: core.ProblemLevelError, path: "$.comments[0].brief.approach",
		},
		{
			name: "brief-question-type", json: comment(`,"brief":{` + briefOutcome + `,"questions":[42]}`),
			code: core.ProblemCodeWrongType, level: core.ProblemLevelError, path: "$.comments[0].brief.questions[0]",
		},
		{
			name: "non-numeric-anchor", json: checkReview(`,"comments":[{"path":"notes.txt","line":"two","body":"A finding with enough substance.","rigor":"walked"}]`),
			code: core.ProblemCodeWrongType, level: core.ProblemLevelWarning, path: "$.comments[0].line",
		},
		{
			name: "side-enum", json: checkReview(`,"comments":[{"path":"notes.txt","line":2,"side":"MIDDLE","body":"A finding with enough substance.","rigor":"walked"}]`),
			code: core.ProblemCodeBadEnum, level: core.ProblemLevelError, path: "$.comments[0].side",
		},
		{
			name: "kb-id-shape", json: comment(`,"kb":["BAD ID!"]`),
			code: core.ProblemCodeBadEnum, level: core.ProblemLevelWarning, path: "$.comments[0].kb[0]",
		},
		{
			name: "kb-id-type", json: comment(`,"kb":[42]`),
			code: core.ProblemCodeWrongType, level: core.ProblemLevelError, path: "$.comments[0].kb[0]",
		},
		{
			name: "suggestion-fence", json: comment(",\"suggestion\":\"```suggestion\\nx\\n```\""),
			code: core.ProblemCodeWrongType, level: core.ProblemLevelWarning, path: "$.comments[0].suggestion",
		},
		{
			name: "confidence-range", json: checkReview(`,"confidence":6`),
			code: core.ProblemCodeOutOfRange, level: core.ProblemLevelError, path: "$.confidence",
		},
		{
			name: "security-summary-type", json: checkReview(`,"security_summary":5`),
			code: core.ProblemCodeWrongType, level: core.ProblemLevelError, path: "$.security_summary",
		},
		{
			name: "resolved-type", json: checkReview(`,"resolved":[3]`),
			code: core.ProblemCodeWrongType, level: core.ProblemLevelError, path: "$.resolved[0]",
		},
		{
			name: "triage-status", json: checkReview(`,"triage":[{"sig":"...","status":"bogus"}]`),
			code: core.ProblemCodeBadEnum, level: core.ProblemLevelError, path: "$.triage[0].status",
		},
		{
			name: "sections-type", json: checkReview(`,"sections":[5]`),
			code: core.ProblemCodeWrongType, level: core.ProblemLevelError, path: "$.sections[0]",
		},
		{
			name: "chunks-type", json: checkReview(`,"chunks":"a"`),
			code: core.ProblemCodeWrongType, level: core.ProblemLevelError, path: "$.chunks",
		},
		{
			name: "chunks-unknown", json: checkReview(`,"chunks":["a","zzz"]`),
			input: recordcore.CheckInput{ChunkNames: []string{"a"}, ChunkManifest: "manifest.json"},
			code:  core.ProblemCodeBadEnum, level: core.ProblemLevelWarning, path: "$.chunks[1]",
		},
		{
			name: "verification-shape", json: checkReview(""),
			input: recordcore.CheckInput{Verification: []byte(`{"sig":"x"}`), HasVerification: true},
			code:  core.ProblemCodeWrongType, level: core.ProblemLevelError, path: "verification.json",
		},
		{
			name: "verification-item-shape", json: checkReview(""),
			input: recordcore.CheckInput{Verification: []byte(`[42]`), HasVerification: true},
			code:  core.ProblemCodeWrongType, level: core.ProblemLevelError, path: "verification.json[0]",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := tc.input
			in.Diff = checkDiff
			result := recordcore.CheckReviewInput([]byte(tc.json), in)
			var found *recordcore.Problem
			for i := range result.Problems {
				if result.Problems[i].Path == tc.path && result.Problems[i].Code == tc.code {
					found = &result.Problems[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("no %s at %s in %+v", tc.code, tc.path, result.Problems)
			}
			if found.Level != tc.level {
				t.Errorf("level = %s, want %s: %+v", found.Level, tc.level, found)
			}
		})
	}
}

// TestCheckReviewCountsTruncatedLevels proves the level tally covers problems
// past the 50-problem cap, so a truncated error can never be accepted.
func TestCheckReviewCountsTruncatedLevels(t *testing.T) {
	t.Parallel()
	var sb strings.Builder
	sb.WriteString(`{"schema_version":"1","body":"` + checkBody + `","approved":false`)
	for i := range 50 {
		fmt.Fprintf(&sb, `,"zz%02d":%d`, i, i)
	}
	sb.WriteString(`,"comments":[{"path":7,"line":2,"body":"A finding with enough substance.","rigor":"walked"}]}`)
	res := recordcore.CheckReview([]byte(sb.String()), checkDiff)
	if len(res.Problems) != 50 || res.Truncated != 1 {
		t.Fatalf("problems=%d truncated=%d, want 50/1", len(res.Problems), res.Truncated)
	}
	if res.Warnings != 50 || res.Errors != 1 {
		t.Errorf("levels errors=%d warnings=%d, want 1/50", res.Errors, res.Warnings)
	}
}

// TestCheckReviewMissingComments proves the schema-required comments key is
// reported instead of silently accepted.
func TestCheckReviewMissingComments(t *testing.T) {
	t.Parallel()
	res := recordcore.CheckReview([]byte(`{"schema_version":"1","body":"`+checkBody+`","approved":false}`), checkDiff)
	checkProblems(t, res, 1)
	if p := res.Problems[0]; p.Path != "$.comments" || p.Code != core.ProblemCodeMissingField || p.Level != core.ProblemLevelWarning {
		t.Errorf("problem = %+v, want missing-field warning at $.comments", p)
	}
}

// TestCheckReviewMissingAnchor proves a comment with no line or start_line is
// reported and counted as moved, matching the merge's body placement.
func TestCheckReviewMissingAnchor(t *testing.T) {
	t.Parallel()
	res := recordcore.CheckReview([]byte(checkReview(`,"comments":[{"path":"notes.txt","body":"A finding with enough substance.","rigor":"walked"}]`)), checkDiff)
	checkProblems(t, res, 1)
	if p := res.Problems[0]; p.Path != "$.comments[0].line" || p.Code != core.ProblemCodeMissingField || p.Level != core.ProblemLevelWarning {
		t.Errorf("problem = %+v, want missing-field warning at $.comments[0].line", p)
	}
	if res.Summary.AnchorsMovedToBody != 1 {
		t.Errorf("moved = %d, want 1", res.Summary.AnchorsMovedToBody)
	}
}

// TestCheckReviewDegenerateBody proves the body-quality bar is a review-input
// problem, not a merge-only refusal.
func TestCheckReviewDegenerateBody(t *testing.T) {
	t.Parallel()
	for _, body := range []string{"short", "this is a placeholder sample text here ok"} {
		res := recordcore.CheckReview([]byte(`{"schema_version":"1","body":`+fmt.Sprintf("%q", body)+`,"approved":false,"comments":[]}`), checkDiff)
		checkProblems(t, res, 1)
		if p := res.Problems[0]; p.Path != "$.body" || p.Code != core.ProblemCodeMissingField || p.Level != core.ProblemLevelError {
			t.Errorf("body %q problem = %+v, want missing-field error at $.body", body, p)
		}
	}
}

// TestCheckReviewPartialChunks proves chunk coverage completeness is a
// review-input problem before the merge.
func TestCheckReviewPartialChunks(t *testing.T) {
	t.Parallel()
	in := recordcore.CheckInput{Diff: checkDiff, ChunkNames: []string{"a", "b"}, ChunkManifest: "manifest.json"}
	res := recordcore.CheckReviewInput([]byte(checkReview(`,"chunks":["a"]`)), in)
	var found *recordcore.Problem
	for i := range res.Problems {
		if res.Problems[i].Path == "$.chunks" && res.Problems[i].Code == core.ProblemCodeMissingField {
			found = &res.Problems[i]
		}
	}
	if found == nil || found.Level != core.ProblemLevelError || !strings.Contains(found.Message, "b") {
		t.Fatalf("partial chunks problems = %+v, want missing-field error naming b", res.Problems)
	}
}

// TestCheckReviewLongUnknownKey proves hostile key names skip the nearest-key
// suggestion instead of paying edit distance over the whole string.
func TestCheckReviewLongUnknownKey(t *testing.T) {
	t.Parallel()
	key := strings.Repeat("z", 4096)
	res := recordcore.CheckReview([]byte(`{"schema_version":"1","body":"`+checkBody+`","approved":false,"comments":[],`+fmt.Sprintf("%q", key)+`:true}`), checkDiff)
	checkProblems(t, res, 1)
	if got := res.Problems[0].Fix; got != "remove it" {
		t.Errorf("fix = %q, want remove it for the long key", got)
	}
}

// TestCheckReviewFenceCountsAsMoved proves a fenced suggestion contributes to
// the same moved count the merge reports.
func TestCheckReviewFenceCountsAsMoved(t *testing.T) {
	t.Parallel()
	suggestion := ",\"suggestion\":\"```\\nx\\n```\""
	res := recordcore.CheckReview([]byte(checkReview(`,"comments":[`+checkComment(`,"rigor":"walked"`+suggestion)+`]`)), checkDiff)
	checkProblems(t, res, 1)
	if res.Summary.AnchorsMovedToBody != 1 {
		t.Errorf("moved = %d, want 1 for the demoted suggestion", res.Summary.AnchorsMovedToBody)
	}
}
