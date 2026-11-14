package trendcore

import (
	"fmt"
	"strconv"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/kb"
)

type overlapRow = kb.OverlapRow

type overlapRollup = kb.OverlapRollup

func overlapAudit(st *state, rows []row) *overlapRollup {
	bundles := make([]kb.BundleFindings, len(rows))
	for i := range rows {
		bundles[i] = kb.BundleFindings{
			Bundle:   rows[i].bundle,
			Findings: rows[i].findings,
		}
	}
	return kb.ComputeOverlap(st.req.RepoRootPath(), skipWarn, bundles, st.warn)
}

func (st *state) emitOverlapHuman(out *overlapRollup) {
	st.human(fmt.Sprintf("overlap: %d measured, %d unmeasured — precision=%s coverage=%s (recalled=%d cited=%d overlap=%d)",
		out.MeasuredBundles, out.UnmeasuredBundles, kb.NumOrNA(out.Precision), kb.NumOrNA(out.Coverage),
		len(out.RecalledRules), len(out.CitedRules), len(out.OverlapRules)))
	st.human(fmt.Sprintf("cited outcomes: %d cited findings — addressed=%d false-positive=%d pending=%d wont-fix=%d; "+
		"joins rows=%d (addressed=%d false-positive=%d)",
		out.CitedFindings, out.Addressed, out.FalsePositive, out.Pending, out.WontFix,
		out.JoinsRows, out.JoinsAddressed, out.JoinsFalse))
	for i := range out.PerBundle {
		row := &out.PerBundle[i]
		st.human(fmt.Sprintf("  %s: measured=%s precision=%s coverage=%s (recalled=%d cited=%d overlap=%d) "+
			"outcomes addressed=%d false-positive=%d pending=%d",
			row.Bundle, strconv.FormatBool(row.Measured), kb.NumOrNA(row.Precision), kb.NumOrNA(row.Coverage),
			len(row.Recalled), len(row.Cited), len(row.Overlap), row.Addressed, row.FalsePositive, row.Pending))
	}
}

// overlapDoc renders the overlap envelope fragment in shell key order.
func overlapDoc(out *overlapRollup) *jq.Obj {
	perBundle := make([]any, 0, len(out.PerBundle))
	for i := range out.PerBundle {
		perBundle = append(perBundle, overlapRowDoc(&out.PerBundle[i]))
	}
	return jq.NewObj(
		"measured_bundles", out.MeasuredBundles,
		"unmeasured_bundles", out.UnmeasuredBundles,
		"unmeasured", out.Unmeasured,
		"bundles", out.Bundles,
		"recalled_rules", anyList(out.RecalledRules),
		"cited_rules", anyList(out.CitedRules),
		"overlap_rules", anyList(out.OverlapRules),
		"precision", out.Precision,
		"coverage", out.Coverage,
		"cited_findings", out.CitedFindings,
		"addressed", out.Addressed,
		"false_positive", out.FalsePositive,
		"pending", out.Pending,
		"wont_fix", out.WontFix,
		"joins", jq.NewObj(
			"rows", out.JoinsRows,
			"addressed", out.JoinsAddressed,
			"false_positive", out.JoinsFalse,
		),
		"calibrate_eligible_bundles", out.CalibrateReady,
		"per_bundle", perBundle,
	)
}

func overlapRowDoc(row *overlapRow) *jq.Obj {
	return jq.NewObj(
		"bundle", row.Bundle,
		"measured", row.Measured,
		"recalled", anyList(row.Recalled),
		"cited", anyList(row.Cited),
		"overlap", anyList(row.Overlap),
		"precision", row.Precision,
		"coverage", row.Coverage,
		"cited_findings", row.CitedFindings,
		"addressed", row.Addressed,
		"false_positive", row.FalsePositive,
		"pending", row.Pending,
		"wont_fix", row.WontFix,
		"joins_rows", row.JoinsRows,
		"calibrate_eligible", row.CalibrateReady,
	)
}
