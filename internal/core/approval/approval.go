// Package approval derives one verdict-owned approval decision from stored
// findings. No model writes the result.
package approval

import (
	"slices"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// DefaultApprovalTier withholds approval from any open finding at important
// or above when no stricter policy or --check tier is configured.
const DefaultApprovalTier = "important"

// Evaluate derives approval from the visible, open findings, the configured
// block clauses, and any violated spec check. Unverified findings never
// withhold approval; S1 writes every finding as unchecked and S4 fills the
// field.
func Evaluate(entries []any, tier, category string, comp *config.ReadCompliance, specViolated bool) *core.ApprovalReceipt {
	threshold := tier
	if threshold == "" {
		threshold = DefaultApprovalTier
	}
	limit := core.TierRank(threshold)
	// Both lists stay allocated: record.json publishes them as arrays, and a
	// nil slice renders as null, which the schema refuses.
	gating := gatingSigs(entries, limit, category)
	clauses := clauseSigs(comp, &gating)
	approved := len(gating) == 0 && !specViolated
	reason := "no gating findings"
	if specViolated {
		reason = "violated spec check"
	}
	if len(gating) > 0 {
		reason = "open findings at-or-above " + threshold
	}
	return &core.ApprovalReceipt{
		Approved:   approved,
		Reason:     reason,
		Tier:       threshold,
		GatingSigs: gating,
		Clauses:    clauses,
	}
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
	if readcommon.StatusOf(obj) != "open" || QuoteStatus(obj) == "unverified" {
		return false
	}
	if category != "" && core.CategoryOf(obj) != category {
		return false
	}
	severity, _ := obj.Get("severity")
	return core.TierRank(severity) <= limit
}

// clauseSigs folds the configured block clauses into the gating sigs and
// returns the clauses in policy order.
func clauseSigs(comp *config.ReadCompliance, gating *[]string) []string {
	if comp == nil {
		return []string{}
	}
	clauses := []string{}
	for i := range comp.Blockers {
		blocker := &comp.Blockers[i]
		if !slices.Contains(clauses, blocker.Clause) {
			clauses = append(clauses, blocker.Clause)
		}
		if sig := blocker.Finding.Sig; sig != "" && !slices.Contains(*gating, sig) {
			*gating = append(*gating, sig)
		}
	}
	return clauses
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
		return "unchecked"
	}
	quote, _ := record.Get("quote")
	if status, ok := quote.(string); ok {
		return status
	}
	return "unchecked"
}
