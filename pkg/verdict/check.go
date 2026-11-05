package verdict

import recordcore "github.com/stainless-code/verdict/internal/core/record"

// Problem is one review-input problem: its JSONPath, code, level, message,
// exact fix, and an optional example of the accepted shape.
type Problem = recordcore.Problem

// CheckResult is CheckReview's output: every problem in document order, the
// overflow past the 50-problem cap, and the clean-run summary.
type CheckResult = recordcore.CheckResult

// CheckReview validates a review.json against a unified diff. It is pure: it
// reads no store, writes nothing, and collects every problem instead of
// stopping at the first.
func CheckReview(review []byte, diff string) CheckResult {
	return recordcore.CheckReview(review, diff)
}

// NormalizeReview repairs the mechanical shapes weak models emit — a
// whole-document code fence, prose around the object, scalars where a string
// array belongs, key aliases, enum synonyms and case, and trivial bytes — and
// returns the normalized document plus one warning per rewrite. It is pure,
// deterministic, and idempotent. It never invents or rewrites prose, and a
// document it cannot recover comes back unchanged for CheckReview to refuse.
func NormalizeReview(review []byte) ([]byte, []Problem) {
	return recordcore.NormalizeReview(review)
}
