package recordcore

import (
	"fmt"
	"os"

	"github.com/stainless-code/verdict/internal/core"
)

// CheckBundle normalizes the review bytes, then runs the bundle-level shape
// check record applies on every path: the normalized review against full.diff
// with the chunk manifest's names and the optional verification claims. A
// caller that already normalized for its own use calls [checkNormalized]
// instead, so the review is normalized once. A missing or unreadable
// full.diff refuses missing-diff instead of checking an empty diff.
func CheckBundle(dir string, review []byte) (CheckResult, *core.RefusalError) {
	normalized, repairs := NormalizeReview(review)
	return checkNormalized(dir, normalized, repairs)
}

// checkNormalized checks already normalized review bytes, folding the
// normalizer's repairs into the result: the caller normalized once and keeps
// the normalized bytes for its own output.
func checkNormalized(dir string, normalized []byte, repairs []Problem) (CheckResult, *core.RefusalError) {
	if invalidJSON(normalized) == nil {
		if ref := validateSuppressionBytes(dir, normalized); ref != nil {
			return CheckResult{}, ref
		}
	}
	diff, err := os.ReadFile(dir + diffSuffix)
	if err != nil {
		return CheckResult{}, &core.RefusalError{Code: codeMissingDiff, Message: fmt.Sprintf(msgMissingDiff, dir)}
	}
	in := CheckInput{Diff: string(diff), ExcludedPaths: excludedPaths(dir)}
	in.ChunkNames, in.ChunkManifest = chunkCheckNames(dir)
	if verification, ok := ReadOptional(dir + "/verification.json"); ok {
		in.Verification, in.HasVerification = verification, true
	}
	return MergeRepairs(CheckReviewInput(normalized, in), repairs, normalized), nil
}
