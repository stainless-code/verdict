package recordcore_test

import (
	"bytes"
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
)

// FuzzCheckReview keeps the pure checker total over arbitrary agent bytes:
// it never panics, never returns more than the published cap, and only emits
// codes from the published enum.
//
//nolint:revive // the fuzz body is one assertion block
func FuzzCheckReview(f *testing.F) {
	f.Add([]byte(`{"schema_version":"1","body":"> ✅ body","comments":[]}`), checkDiff)
	f.Add([]byte("```json\n{}\n```"), checkDiff)
	f.Add([]byte(`{"comments":[{"path":"notes.txt","line":2,"brief":{"questions":"why"}}]}`), checkDiff)
	f.Add([]byte(`[1,2]`), "")
	f.Fuzz(func(t *testing.T, review []byte, diff string) {
		result := recordcore.CheckReview(review, diff)
		if len(result.Problems) > 50 {
			t.Fatalf("problems = %d, want at most 50", len(result.Problems))
		}
		if result.Truncated < 0 {
			t.Fatalf("truncated = %d", result.Truncated)
		}
		for _, problem := range result.Problems {
			if !slices.Contains(core.ProblemCodes(), problem.Code) {
				t.Fatalf("unpublished problem code %q", problem.Code)
			}
		}
	})
}

// FuzzNormalizeReview keeps the normalizer total and idempotent over arbitrary
// agent bytes: it never panics, only emits published repair codes, and a second
// pass changes nothing.
func FuzzNormalizeReview(f *testing.F) {
	f.Add([]byte(`{"schema_version":"1","body":"> ✅ body","comments":[]}`))
	f.Add([]byte("```json\n{\"body\":\"> ✅ body\"}\n```"))
	f.Add([]byte("Here is the review:\n{\"body\":\"> ✅ body\"}\nLet me know"))
	f.Add([]byte("\ufeff{\"comments\":[{\"file\":\"a.go\",\"rung\":\"traced\",\"severity\":\"Critical\"}]}"))
	f.Add([]byte(`{broken`))
	f.Fuzz(func(t *testing.T, review []byte) {
		checkNormalizedIdempotent(t, review)
	})
}

// checkNormalizedIdempotent proves one normalize pass is total and the
// second pass changes nothing.
func checkNormalizedIdempotent(t *testing.T, review []byte) {
	t.Helper()
	once, repairs := recordcore.NormalizeReview(review)
	for _, repair := range repairs {
		if !slices.Contains(core.ProblemCodes(), repair.Code) {
			t.Fatalf("unpublished repair code %q", repair.Code)
		}
		if repair.Level != core.ProblemLevelWarning {
			t.Fatalf("repair %+v is not a warning", repair)
		}
	}
	twice, again := recordcore.NormalizeReview(once)
	if !bytes.Equal(twice, once) || len(again) != 0 {
		t.Fatalf("not idempotent: %q -> %q (%v)", once, twice, again)
	}
}
