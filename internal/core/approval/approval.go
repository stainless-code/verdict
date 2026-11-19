// Package approval derives one verdict-owned approval decision from stored
// findings. No model writes the result.
package approval

import (
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// DefaultApprovalTier withholds approval from any open finding at important
// or above when no stricter policy or --check tier is configured.
const DefaultApprovalTier = "important"

const (
	quoteUnverified = "unverified"
	quoteUnchecked  = "unchecked"
)

// EligibleFindings returns the stored findings whose quote standing is not
// unverified, preserving store order. Both compliance evaluators (record and
// report) filter through this helper so an unverified finding never gates,
// even when named by a block clause.
func EligibleFindings(entries []any) []any {
	out := make([]any, 0, len(entries))
	for _, entry := range entries {
		if QuoteStatus(entry) == quoteUnverified {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// Evaluate derives approval from the visible, open findings, the configured
// block clauses, any violated spec check, and the deterministic evidence
// receipt. Unverified findings never enter GatingSigs; insufficient evidence
// or a dropped block-clause finding that would otherwise flip approval to
// true yields Approved=false with Insufficient=true.
func Evaluate(
	entries []any,
	tier, category string,
	comp *config.ReadCompliance,
	specViolated bool,
	ev *core.EvidenceReceipt,
) *core.ApprovalReceipt {
	threshold := tier
	if threshold == "" {
		threshold = DefaultApprovalTier
	}
	limit := core.TierRank(threshold)
	// Both lists stay allocated: record.json publishes them as arrays, and a
	// nil slice renders as null, which the schema refuses.
	gating := gatingSigs(entries, limit, category)
	clauses, droppedClause := clauseSigs(comp, entries, &gating)
	approved, insufficient, reason := evaluateOutcome(threshold, gating, specViolated, droppedClause, ev)
	return &core.ApprovalReceipt{
		Approved:     approved,
		Insufficient: insufficient,
		Reason:       reason,
		Tier:         threshold,
		GatingSigs:   gating,
		Clauses:      clauses,
	}
}

func evaluateOutcome(
	threshold string,
	gating []string,
	specViolated, droppedClause bool,
	ev *core.EvidenceReceipt,
) (approved, insufficient bool, reason string) {
	if ev != nil && ev.Insufficient {
		return false, true, insufficientReason(ev)
	}
	if len(gating) > 0 {
		return false, false, "open findings at-or-above " + threshold
	}
	if specViolated {
		return false, false, "violated spec check"
	}
	if droppedClause {
		return false, true, core.CodeReviewInsufficient + ": unverified finding named by block clause"
	}
	return true, false, "no gating findings"
}

func insufficientReason(ev *core.EvidenceReceipt) string {
	for _, gap := range ev.Gaps {
		if trimmed := strings.TrimSpace(gap); trimmed != "" {
			return core.CodeReviewInsufficient + ": " + trimmed
		}
	}
	return core.CodeReviewInsufficient + ": evidence checks insufficient"
}

// gatingSigs collects the sigs of the visible, open, checked findings at or
// above the tier, in store order.
func gatingSigs(entries []any, limit int, category string) []string {
	gating := []string{}
	for _, item := range readcommon.VisibleFindings(entries) {
		obj, ok := item.(*jq.Obj)
		if !ok || !withholds(obj, limit, category) {
			continue
		}
		sig, _ := obj.Get("sig")
		gating = append(gating, jq.R(sig))
	}
	return gating
}

// withholds reports whether one finding blocks approval on its own.
func withholds(obj *jq.Obj, limit int, category string) bool {
	if readcommon.StatusOf(obj) != "open" || QuoteStatus(obj) == quoteUnverified {
		return false
	}
	if category != "" && core.CategoryOf(obj) != category {
		return false
	}
	severity, _ := obj.Get("severity")
	return core.TierRank(severity) <= limit
}

// clauseSigs folds verified block-clause findings into the gating sigs and
// returns the clauses in policy order plus whether any blocker was skipped
// because its finding is unverified (defense in depth behind EligibleFindings).
func clauseSigs(comp *config.ReadCompliance, entries []any, gating *[]string) ([]string, bool) {
	if comp == nil {
		return []string{}, false
	}
	unverified := unverifiedSigs(entries)
	clauses := []string{}
	dropped := false
	for i := range comp.Blockers {
		blocker := &comp.Blockers[i]
		if _, skip := unverified[blocker.Finding.Sig]; skip {
			dropped = true
			continue
		}
		clauses = appendUnique(clauses, blocker.Clause)
		if sig := blocker.Finding.Sig; sig != "" {
			*gating = appendUnique(*gating, sig)
		}
	}
	return clauses, dropped
}

func unverifiedSigs(entries []any) map[string]struct{} {
	out := make(map[string]struct{})
	for _, entry := range entries {
		if QuoteStatus(entry) != quoteUnverified {
			continue
		}
		if sig := jq.R(readcommon.FieldOf(entry, "sig")); sig != "" {
			out[sig] = struct{}{}
		}
	}
	return out
}

func appendUnique(dst []string, val string) []string {
	if slices.Contains(dst, val) {
		return dst
	}
	return append(dst, val)
}

// Views adapts stored entries to the compliance evaluator's finding shape.
func Views(entries []any) []config.FindingView {
	out := make([]config.FindingView, 0, len(entries))
	for _, entry := range entries {
		obj, ok := entry.(*jq.Obj)
		if !ok {
			continue
		}
		out = append(out, config.FindingView{
			Sig:      jq.R(readcommon.FieldOf(obj, "sig")),
			Path:     jq.R(readcommon.FieldOf(obj, "path")),
			Category: jq.R(readcommon.FieldOf(obj, "category")),
			Severity: jq.R(readcommon.FieldOf(obj, "severity")),
			Status:   jq.R(readcommon.FieldOf(obj, "status")),
		})
	}
	return out
}

// QuoteStatus reads the evidence standing stored on one finding. A legacy
// store without the field reads unchecked, not unverified.
func QuoteStatus(entry any) string {
	value := readcommon.FieldOf(entry, "verification")
	record, ok := value.(*jq.Obj)
	if !ok {
		return quoteUnchecked
	}
	quote, _ := record.Get("quote")
	if status, ok := quote.(string); ok {
		return status
	}
	return quoteUnchecked
}
