package recordcore_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
)

// nestedRefusalCase is one malformed document extraction must never recover
// by returning an inner object: the bytes pass through untouched for
// CheckReview to refuse invalid-json.
type nestedRefusalCase struct {
	name string
	in   string
}

// nestedRefusalCases pins the trailing-comma review and the wrapped review
// whose inner object once recorded as an empty or nested review.
func nestedRefusalCases() []nestedRefusalCase {
	comment := `{"path":"notes.txt","line":2,"body":"A finding with enough substance."}`
	review := `{"schema_version":"1","body":"` + normalizeBody + `","approved":false,"comments":[]}`
	return []nestedRefusalCase{
		{
			name: "trailing-comma",
			in:   `{"schema_version":"1","body":"` + normalizeBody + `","approved":false,"comments":[` + comment + `,]}`,
		},
		{
			name: "wrapped",
			in:   `{"review":` + review + `,}`,
		},
	}
}

// TestNormalizeReviewNeverExtractsNested proves extraction only considers
// top-level objects: a malformed outer document never normalizes to an inner
// object, so the review refuses invalid-json instead of recording zero
// findings from an empty or nested review.
func TestNormalizeReviewNeverExtractsNested(t *testing.T) {
	t.Parallel()
	for _, tc := range nestedRefusalCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, repairs := recordcore.NormalizeReview([]byte(tc.in))
			if string(got) != tc.in || len(repairs) != 0 {
				t.Fatalf("normalize recovered an inner object: %s -> %s (%v)", tc.in, got, repairs)
			}
			checkInvalidJSONRefusal(t, tc.in, got)
		})
	}
}

// checkInvalidJSONRefusal proves the checker refuses the untouched bytes
// with an invalid-json error.
func checkInvalidJSONRefusal(t *testing.T, in string, got []byte) {
	t.Helper()
	res := recordcore.CheckReview(got, checkDiff)
	if res.Errors == 0 {
		t.Fatalf("problems = %+v, want an invalid-json refusal", res.Problems)
	}
	for _, p := range res.Problems {
		if p.Code == core.ProblemCodeInvalidJSON && p.Level == core.ProblemLevelError {
			return
		}
	}
	t.Fatalf("problems = %+v, want invalid-json as an error for %s", res.Problems, in)
}

// TestNormalizeReviewBracesAreLinear proves extraction is one pass: 1 MB of
// unbalanced braces normalizes well under a second instead of stalling on a
// quadratic rescan.
func TestNormalizeReviewBracesAreLinear(t *testing.T) {
	t.Parallel()
	in := strings.Repeat("{", 1<<20)
	start := time.Now()
	got, repairs := recordcore.NormalizeReview([]byte(in))
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("1 MB of braces took %v, want well under a second", elapsed)
	}
	if string(got) != in || len(repairs) != 0 {
		t.Fatalf("normalize changed unbalanced braces: %d bytes -> %d bytes (%v)", len(in), len(got), repairs)
	}
}
