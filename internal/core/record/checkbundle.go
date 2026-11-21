package recordcore

import (
	"fmt"
	"os"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
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
	return checkPhase(dir, normalized, repairs, false)
}

// CheckDiscovery checks S4 evidence before cross-checking is complete.
func CheckDiscovery(dir string, review []byte) (CheckResult, *core.RefusalError) {
	normalized, repairs := NormalizeReview(review)
	return checkPhase(dir, normalized, repairs, true)
}

func checkPhase(dir string, normalized []byte, repairs []Problem, discovery bool) (CheckResult, *core.RefusalError) {
	st := &state{dir: dir}
	if invalidJSON(normalized) == nil {
		if ref := st.validateSuppressionBytes(normalized); ref != nil {
			return CheckResult{}, ref
		}
	}
	diff, err := os.ReadFile(dir + diffSuffix)
	if err != nil {
		return CheckResult{}, &core.RefusalError{Code: codeMissingDiff, Message: fmt.Sprintf(msgMissingDiff, dir)}
	}
	in := CheckInput{
		Diff:          string(diff),
		ExcludedPaths: st.excludedPaths(),
		HeadSHA:       bundleCheckHead(dir),
	}
	in.ChunkNames, in.ChunkManifest = chunkCheckNames(dir)
	if verification, ok := ReadOptional(dir + "/verification.json"); ok {
		in.Verification, in.HasVerification = verification, true
	}
	return withPhaseEvidence(dir, normalized, MergeRepairs(CheckReviewInput(normalized, in), repairs, normalized), discovery), nil
}

// withPhaseEvidence attaches the evidence receipt the bundle bytes support
// and folds its gaps into the result: the same pass the shared merge runs, so
// the check path and the stored receipt cannot disagree about what the bundle
// proves. Evidence warnings stay out of res.Warnings — that count is the shape
// checker's, and a stale-head or normalized-fence warning refusing under
// --strict must not depend on how many quotes failed.
func withPhaseEvidence(dir string, normalized []byte, res CheckResult, discovery bool) CheckResult {
	receipt, problems := phaseEvidenceOf(dir, normalized, discovery)
	if receipt == nil {
		return res
	}
	check := &EvidenceCheck{Receipt: receipt, Problems: problems}
	for i := range problems {
		if problems[i].Level == core.ProblemLevelError {
			check.Errors++
		}
	}
	res.Evidence = check
	res.Errors += check.Errors
	return res
}

func bundleCheckHead(dir string) string {
	store := readcommon.OpenBundle(dir)
	if head := store.Head(); head != "" {
		return head
	}
	return readcommon.JSONStr(dir+sourceSuffix, "head_sha")
}
