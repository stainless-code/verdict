package reportcore

import (
	"fmt"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// loadRunPolicy verifies the persisted run bundle for the display
// floor: a deleted or tampered bundle refuses controls-stale instead
// of silently rendering everything, while legacy bundles without typed
// evidence render everything with the gap warned.
func loadRunPolicy(st *state) *core.RefusalError {
	rs, ref := config.VerifyRunState(st.dir)
	if ref != nil {
		return ref
	}
	if rs == nil {
		st.warn("run bundle has no typed evidence (pre-typed bundle?) — rendering without policy floors")
		return nil
	}
	if !rs.Verified() {
		st.warn("typed evidence provenance unavailable (pre-integrity bundle) — stored policy enforced without verified evidence")
	}
	st.policy = rs
	return nil
}

// evaluateCompliance evaluates the captured policy against the FULL stored
// finding set before any display filter, so a display floor never hides a
// blocking finding and a filtered view never greens a gate. Reads never
// append an outcome or execute a command.
func evaluateCompliance(st *state) {
	if st.policy == nil || st.policy.Resolved == nil {
		return
	}
	comp := config.EvaluateReadCompliance(st.policy, st.headSHA, readFindingViews(st.entries))
	if !comp.HasPolicy {
		return
	}
	st.compliance = &comp
	st.requiredGaps = requiredProfileGaps(st, &comp)
	warnCompliance(st, &comp)
}

// warnCompliance surfaces the captured compliance shortfall beside the
// rendered review; gate and typed data carry the same verdict.
func warnCompliance(st *state, comp *config.ReadCompliance) {
	if n := len(comp.Blockers); n > 0 {
		st.warn(fmt.Sprintf("compliance: %d open finding(s) violate a captured requirement — approval is blocked", n))
	}
	if !comp.Unverified && len(comp.Pending) > 0 {
		st.warn(fmt.Sprintf("compliance: %d required check(s) pending for saved head %s",
			len(comp.Pending), readcommon.ShortSHA(st.headSHA)))
	}
	if comp.Unverified {
		st.warn("compliance: captured provenance unavailable — required checks stay unknown, never passing")
	}
	for _, gap := range st.requiredGaps {
		st.warn("compliance: " + gap)
	}
}

// requiredProfileGaps reads the captured profile work report and gate must
// evaluate: a deep group without a current validation receipt, and a capped
// group without usable lens attestation. Reads never execute or append.
func requiredProfileGaps(st *state, comp *config.ReadCompliance) []string {
	if comp == nil || !comp.HasPolicy || st.policy.Resolved == nil {
		return nil
	}
	var gaps []string
	for _, gap := range verifycore.DeepGaps(st.policy, st.dir, st.headSHA, st.req.RepoRootPath()) {
		gaps = append(gaps, "required work: "+gap)
	}
	usage := config.ReadLensUsage(st.policy, st.dir)
	for _, violation := range usage.Violations() {
		gaps = append(gaps, "required lens evidence: "+violation.Detail)
	}
	return gaps
}

// readFindingViews adapts the unfiltered stored entries to the compliance
// view; entries without the finding shape never invent a blocker.
func readFindingViews(entries []any) []config.FindingView {
	out := make([]config.FindingView, 0, len(entries))
	for _, entry := range entries {
		out = append(out, config.FindingView{
			Path:     jqStrOf(entry, "path"),
			Category: jqStrOf(entry, "category"),
			Severity: jqStrOf(entry, "severity"),
			Status:   jqStrOf(entry, "status"),
		})
	}
	return out
}

// jqStrOf reads one string field from a stored finding entry.
func jqStrOf(entry any, key string) string {
	return jq.StrOf(readcommon.FieldOf(entry, key))
}

// warnLensUsage surfaces incomplete or over-cap lens evidence beside the
// rendered review: report is a read, so the shortfall is a warning and
// never a pass, and an absent record reads as unknown.
func warnLensUsage(st *state) {
	if st.policy == nil || st.policy.Resolved == nil {
		return
	}
	usage := config.ReadLensUsage(st.policy, st.dir)
	for _, violation := range usage.Violations() {
		st.warn("profile lens evidence: " + violation.Detail)
	}
}

// warnDeepValidation surfaces each deep group's receipt standing beside the
// rendered review. Report is a read: it never runs a command and never turns
// a missing or stale receipt into a pass.
func warnDeepValidation(st *state) {
	if st.policy == nil || st.policy.Resolved == nil {
		return
	}
	head := ""
	if b := readcommon.OpenBundle(st.dir); b != nil {
		head = b.Head()
	}
	for _, gap := range verifycore.DeepGaps(st.policy, st.dir, head, st.req.RepoRootPath()) {
		st.warn(gap)
	}
}

// applyDisplayFloor derives the filtered view after the gates load: the
// effective display_min_severity withholds below-floor findings from
// the rendered view while every gate keeps reading the full ranked
// set. Unknown severities stay visible; the unset floor views
// everything.
func applyDisplayFloor(st *state) {
	rs := st.policy
	kept := make([]readcommon.Ranked, 0, len(st.ranked))
	for _, item := range st.ranked {
		if config.BelowDisplayFloor(rs, item.Entry) {
			continue
		}
		kept = append(kept, item)
	}
	st.view = kept
}
