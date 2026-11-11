package recordcore

import (
	"fmt"
	"os"

	"github.com/stainless-code/verdict/internal/core"
)

// CheckBundle runs the same bundle-level shape check record's dry run uses:
// normalize the review bytes, then check them against full.diff with the
// chunk manifest's names and the optional verification claims. A missing or
// unreadable full.diff refuses missing-diff instead of checking an empty
// diff.
func CheckBundle(dir string, review []byte) (CheckResult, *core.RefusalError) {
	diff, err := os.ReadFile(dir + diffSuffix)
	if err != nil {
		return CheckResult{}, &core.RefusalError{Code: codeMissingDiff, Message: fmt.Sprintf(msgMissingDiff, dir)}
	}
	normalized, repairs := NormalizeReview(review)
	in := CheckInput{Diff: string(diff)}
	in.ChunkNames, in.ChunkManifest = chunkCheckNames(dir)
	if verification, ok := ReadOptional(dir + "/verification.json"); ok {
		in.Verification, in.HasVerification = verification, true
	}
	return MergeRepairs(CheckReviewInput(normalized, in), repairs, normalized), nil
}
