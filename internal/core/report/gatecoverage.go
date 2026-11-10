package reportcore

import (
	"os"
	"strconv"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/coverage"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// The coverage dimension reads the receipt `record` and `post` already store
// beside the head it was made at — never review.json. A gate that re-evaluated
// the attestation would publish a number the store does not hold, so it could
// disagree with the receipt the merge is audited against.
const (
	postSuffix      = "/post.json"
	recordSuffix    = "/record.json"
	chunksManifest  = "/chunks/manifest.json"
	coverageWarn    = "warn"
	coverageRequire = "require"
	fieldHead       = "head"
	fieldCoverage   = "coverage"
	fieldPercent    = "percent"
	fieldAttested   = "attested"
	fieldTotal      = "total"
	fieldExempt     = "exempt"
	fieldGaps       = "gaps"
	coverageOK      = " — coverage passes"
	coverageFail    = " — coverage fails"
	coverageAdvise  = " — coverage is advisory (--coverage warn)"
	// fullPercent is a fully attested measurement; the evaluator shares the
	// denominator.
	fullPercent = 100
)

// storedCoverage is the receipt found in the bundle's store: the head a
// receipt names, and the measured summary when that receipt carries one.
// head is the last head seen, so an absent or stale receipt can say which head
// it did describe.
type storedCoverage struct {
	sum   *core.CoverageSummary
	head  string
	found bool
}

// readStoredCoverage reads post.json then record.json — the precedence the
// report's lead record already uses — and keeps the receipt that names the
// gated head, with its summary when one is stored.
func readStoredCoverage(dir, head string) storedCoverage {
	var out storedCoverage
	for _, suffix := range []string{postSuffix, recordSuffix} {
		receipt, ok := receiptAt(dir + suffix)
		if !ok {
			continue
		}
		out = mergeReceipt(out, receipt, head)
		if out.sum != nil {
			break
		}
	}
	return out
}

// mergeReceipt folds one receipt into what is kept: a receipt naming another
// head is remembered only for its name, a matching one marks the head found,
// and its summary wins.
func mergeReceipt(kept, next storedCoverage, head string) storedCoverage {
	if head != "" && next.head != head {
		kept.head = next.head
		return kept
	}
	kept.head, kept.found = next.head, true
	if next.sum != nil {
		kept.sum = next.sum
	}
	return kept
}

// receiptAt decodes one stored receipt. A missing, unreadable, or non-object
// document is no receipt at all: the dimension reads it as absent rather than
// guessing at a partial summary.
func receiptAt(path string) (storedCoverage, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return storedCoverage{}, false
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return storedCoverage{}, false
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return storedCoverage{}, false
	}
	out := storedCoverage{head: jq.Str(m[fieldHead])}
	if summary, ok := jq.AsMap(m[fieldCoverage]); ok {
		out.sum = summaryOf(summary)
	}
	return out, true
}

// summaryOf reads the stored receipt's measured fields.
func summaryOf(m map[string]any) *core.CoverageSummary {
	return &core.CoverageSummary{
		Percent:  percentAt(m),
		Attested: intAt(m, fieldAttested),
		Total:    intAt(m, fieldTotal),
		Exempt:   intAt(m, fieldExempt),
		Gaps:     intAt(m, fieldGaps),
	}
}

// percentAt separates an unmeasured run (percent null) from a measured zero.
func percentAt(m map[string]any) *int {
	f, ok := readcommon.NumVal(m[fieldPercent])
	if !ok {
		return nil
	}
	n := int(f)
	return &n
}

func intAt(m map[string]any, key string) int {
	f, ok := readcommon.NumVal(m[key])
	if !ok {
		return 0
	}
	return int(f)
}

// gateCoverageCheck is the coverage dimension: the stored receipt for the
// gated head, complete or not. The posture decides what a shortfall costs —
// `require` (the default) fails the gate, `warn` prints one warning and leaves
// the dimension neutral so it can never fail the gate.
func gateCoverageCheck(st *state) gateCheck {
	status, detail, count := coverageVerdict(st, readStoredCoverage(st.dir, st.headSHA))
	if status == dimFail || status == dimUnknown {
		return coveragePosture(st, status, detail, count)
	}
	return gateCheck{
		dim:  core.GateDimension{ID: dimCoverage, Status: status, Count: count, Detail: detail},
		line: checkPrefix + detail + coverageOK,
	}
}

// coveragePosture applies --coverage to a shortfall: require keeps the failing
// verdict, warn downgrades it to a neutral skip with one warning line naming
// the same detail, so an advisory posture never reads as a clean pass.
func coveragePosture(st *state, status, detail string, count *int) gateCheck {
	if st.flags.Coverage == coverageWarn {
		st.warn("coverage: " + detail + coverageAdvise)
		return gateCheck{
			dim: core.GateDimension{
				ID: dimCoverage, Status: dimSkip, Count: count,
				Detail: detail + coverageAdvise,
			},
			line: checkPrefix + detail + coverageAdvise,
		}
	}
	return gateCheck{
		dim:  core.GateDimension{ID: dimCoverage, Status: status, Count: count, Detail: detail},
		line: checkPrefix + detail + coverageFail,
	}
}

// coverageVerdict maps the stored receipt to one verdict, its human detail,
// and the unattested-range tally. Absent and unprovable receipts read unknown
// (never a pass): the gate fails closed on what the store cannot show.
func coverageVerdict(st *state, cov storedCoverage) (string, string, *int) {
	short := readcommon.ShortSHA(st.headSHA)
	switch {
	case isRegular(st.dir + chunksManifest):
		return dimUnknown, "chunked bundle — chunks[] is the attestation and the store keeps no receipt", nil
	case st.headSHA == "":
		return dimUnknown, "stored coverage absent — the bundle records no HEAD to key it to", nil
	case !cov.found && cov.head != "":
		return dimUnknown, "stored coverage is for HEAD " + readcommon.ShortSHA(cov.head) +
			", not HEAD " + short, nil
	case !cov.found:
		return dimUnknown, "stored coverage absent for HEAD " + short +
			" — no completed post or record", nil
	case cov.sum == nil:
		return dimUnknown, "stored coverage absent for HEAD " + short +
			" — the receipt carries no coverage[] attestation", nil
	}
	return receiptVerdict(cov.sum)
}

// receiptVerdict grades one stored summary: a measured bundle passes only at
// 100%, a bundle with nothing non-exempt to measure passes outright, and a
// partial one fails naming the range count and the basis.
func receiptVerdict(sum *core.CoverageSummary) (string, string, *int) {
	if sum.Total == 0 {
		return dimPass, unmeasuredDetail(sum.Exempt), new(0)
	}
	percent := 0
	if sum.Percent != nil {
		percent = *sum.Percent
	}
	if sum.Gaps > 0 || percent < fullPercent {
		return dimFail, strconv.Itoa(sum.Gaps) + " unattested range(s) at " +
			strconv.Itoa(percent) + "% of " + coverage.Basis + linesDetail(sum), new(sum.Gaps)
	}
	return dimPass, strconv.Itoa(fullPercent) + "% of " + coverage.Basis + " attested" + linesDetail(sum), new(0)
}

// unmeasuredDetail names an exempt-only bundle: nothing was measurable, so
// there is nothing to attest.
func unmeasuredDetail(exempt int) string {
	if exempt > 0 {
		return "nothing changed to measure (" + strconv.Itoa(exempt) + " file(s) exempt)"
	}
	return "nothing changed to measure"
}

// linesDetail renders the attested/total line count beside the percentage.
func linesDetail(sum *core.CoverageSummary) string {
	return " (" + strconv.Itoa(sum.Attested) + "/" + strconv.Itoa(sum.Total) + " lines)"
}
