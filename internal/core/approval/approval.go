// Package approval derives one verdict-owned approval decision from stored
// findings. No model writes the result.
package approval

import (
	"fmt"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	panelcore "github.com/stainless-code/verdict/internal/core/panel"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// DefaultApprovalTier withholds approval from any open finding at important
// or above when no stricter policy or gate tier is configured.
const DefaultApprovalTier = "important"

// Evidence statuses the decision reads. Only an explicit verified stamp
// counts: a finding the evidence pass never saw, or one whose quote it could
// not check, carries no approval weight.
const (
	quoteVerified  = "verified"
	quoteUnchecked = "unchecked"

	// missingEvidence is the reason an absent receipt carries when the caller
	// names none: an unreadable or never-recorded bundle is the same fact here.
	missingEvidence = "evidence receipt missing"

	// droppedClauseReason names a block clause whose finding carries no
	// verified quote: the clause may not gate, so the review is insufficient.
	droppedClauseReason = ": unverified finding named by block clause"

	// specStatusUnmet is the spec-check status that reports a requirement
	// nothing in the diff implements. It withholds approval without making the
	// review unverifiable.
	specStatusUnmet = "unmet"
)

// Evidence is the evidence input to one approval decision: the receipt that
// backs the reviewed head, or why the bundle carries none. A nil receipt is
// never sufficient — a decision without evidence is an insufficient decision,
// not a clean one.
type Evidence struct {
	Receipt *core.EvidenceReceipt
	// Missing names why no current receipt backs the head, without the
	// `review-insufficient` code: "evidence receipt missing for head a1b2c3d".
	// It is read only when Receipt is nil.
	Missing string
}

// EligibleFindings returns the stored findings whose quote standing is
// verified, preserving store order. The compliance gates that refuse a post or
// hold readiness read through this helper, so an unverified finding never
// blocks there.
func EligibleFindings(entries []any) []any {
	out := make([]any, 0, len(entries))
	for _, entry := range entries {
		if Verified(entry) {
			out = append(out, entry)
		}
	}
	return out
}

// Verified reports whether one stored finding carries the verified quote
// standing the evidence pass stamps. A finding without the record, or with a
// standing this build does not know, is not verified.
func Verified(entry any) bool {
	if QuoteStatus(entry) != quoteVerified {
		return false
	}
	return CrossEligible(entry)
}

// CrossEligible reports whether one finding's cross standing lets it gate. A
// manual capture carries no stamp and keeps quote-only eligibility; a stamp the
// panel derived from a captured execution must be complete and confirmed.
//
// This predicate cannot see the capture mode, by design: the panel rewrites
// verification.cross from the captured execution before any reader of the
// store runs, so an absent stamp here means a manual capture and never a
// stripped one. A two-finder capture always holds a present stamp —
// confirmed, contested, or pending — which is why the panel is the one place
// that decides eligibility.
func CrossEligible(entry any) bool {
	verification := readcommon.FieldOf(entry, "verification")
	cross := readcommon.FieldOf(verification, "cross")
	return cross == nil || (QuoteStatus(entry) == quoteVerified && panelcore.EligibleCross(cross))
}

// Evaluate derives approval from the visible, open findings, the configured
// block clauses, the spec checks that failed, and the deterministic evidence
// receipt. Gating requires a verified quote: an unverified finding never enters
// GatingSigs, and a gating finding without one makes the decision insufficient
// instead of clean. comp must be evaluated over the unfiltered stored findings
// so clauseSigs can see, and refuse, an unverified blocker.
func Evaluate(
	entries []any,
	tier, category string,
	comp *config.ReadCompliance,
	specFailures []core.SpecFailure,
	ev Evidence,
) *core.ApprovalReceipt {
	threshold := tier
	if threshold == "" {
		threshold = DefaultApprovalTier
	}
	limit := core.TierRank(threshold)
	// Both lists stay allocated: record.json publishes them as arrays, and a
	// nil slice renders as null, which the schema refuses.
	gating, unbacked := gatingSigs(entries, limit, category)
	clauses, droppedClause := clauseSigs(comp, entries, &gating, &unbacked)
	got := evaluateOutcome(&decision{
		threshold:     threshold,
		gating:        gating,
		unbacked:      unbacked,
		specFailures:  specFailures,
		droppedClause: droppedClause,
		ev:            ev,
	})
	return &core.ApprovalReceipt{
		Approved:     got.approved,
		Insufficient: got.insufficient,
		Reason:       got.reason,
		Tier:         threshold,
		GatingSigs:   gating,
		Clauses:      clauses,
	}
}

// decision carries the inputs the outcome ladder reads once the findings and
// the policy have been evaluated.
type decision struct {
	ev            Evidence
	threshold     string
	gating        []string
	unbacked      []string
	specFailures  []core.SpecFailure
	droppedClause bool
}

// outcome is one decision: whether approval is granted, whether it was
// withheld because the evidence could not back it, and why.
type outcome struct {
	reason       string
	approved     bool
	insufficient bool
}

// evaluateOutcome applies the decision ladder: evidence first, then gating
// findings, then the spec checks that failed, then insufficiency the evidence
// pass could not see, and only then a clean approval — which a receipt has to
// complete before it can be granted.
func evaluateOutcome(in *decision) outcome {
	if got, settled := evidenceOutcome(in); settled {
		return got
	}
	if len(in.gating) > 0 {
		return outcome{reason: "open findings at-or-above " + in.threshold}
	}
	if reason := specFailureReason(in.specFailures); reason != "" {
		return outcome{reason: reason}
	}
	if in.droppedClause {
		return outcome{approved: false, insufficient: true, reason: core.CodeReviewInsufficient + droppedClauseReason}
	}
	if len(in.unbacked) > 0 {
		return outcome{
			approved:     false,
			insufficient: true,
			reason:       core.CodeReviewInsufficient + ": finding " + in.unbacked[0] + " has no verified quote",
		}
	}
	// A3's last rule, and the one only this decision can enforce: with nothing
	// gating, approval needs a matched receipt for every non-exempt changed
	// hunk. The evidence pass already reports a shortfall as an error when it
	// agrees nothing gates, but it reads the review bytes while this decision
	// reads the visible store — a finding withheld by an exclusion, a
	// suppression receipt, or a policy floor is invisible here and was visible
	// there, so the two can disagree. Reading the receipt's own coverage closes
	// that gap for good: a review nothing checked may not read as clean.
	if !in.ev.Receipt.Receipts.Complete {
		return outcome{approved: false, insufficient: true, reason: incompleteReceiptsReason(in.ev.Receipt)}
	}
	return outcome{approved: true, reason: "no gating findings"}
}

// incompleteReceiptsReason names the coverage shortfall: how many changed hunks
// the receipt leaves unmatched, or the pass's own sentence when the shortfall
// is a diff with no keyable hunk at all.
func incompleteReceiptsReason(ev *core.EvidenceReceipt) string {
	if missing := ev.Receipts.Total - ev.Receipts.Covered; missing > 0 {
		return fmt.Sprintf("%s: receipts missing for %d of %d hunks",
			core.CodeReviewInsufficient, missing, ev.Receipts.Total)
	}
	return insufficientReason(ev)
}

// specFailureReason words the spec checks that withheld approval. A review
// whose only failing rows are `violated` keeps the sentence it has always had,
// so the stored decision and its goldens do not move; an `unmet` row names
// itself, because "nothing implements this" is a different instruction to the
// author than "this code is wrong".
func specFailureReason(failures []core.SpecFailure) string {
	unmet, violated := "", false
	for _, failure := range failures {
		if failure.Status != specStatusUnmet {
			violated = true
			continue
		}
		if unmet == "" {
			unmet = failure.Item
		}
	}
	switch {
	case violated && unmet != "":
		return "violated spec check; unmet spec check: " + unmet
	case violated:
		return "violated spec check"
	case unmet != "":
		return "unmet spec check: " + unmet
	default:
		return ""
	}
}

// evidenceOutcome settles the decision on the evidence alone, before any
// finding is read: no receipt and an insufficient receipt are both refusals,
// and neither can be answered by a clean finding set. A verified gating finding
// is the more decisive fact for the operator, so the reason leads with it while
// the decision stays insufficient: the receipt still cannot back the head.
func evidenceOutcome(in *decision) (outcome, bool) {
	got, settled := receiptOutcome(in.ev)
	if !settled {
		return outcome{}, false
	}
	if len(in.gating) > 0 {
		got.reason = "open findings at-or-above " + in.threshold + "; " + got.reason
	}
	return got, true
}

// receiptOutcome is evidenceOutcome's evidence half: the refusal an absent or
// insufficient receipt earns, without the finding precedence above it.
func receiptOutcome(ev Evidence) (outcome, bool) {
	if ev.Receipt == nil {
		reason := ev.Missing
		if reason == "" {
			reason = missingEvidence
		}
		return outcome{approved: false, insufficient: true, reason: core.CodeReviewInsufficient + ": " + reason}, true
	}
	if ev.Receipt.Insufficient {
		return outcome{approved: false, insufficient: true, reason: insufficientReason(ev.Receipt)}, true
	}
	return outcome{}, false
}

// insufficientReason names the first gap the evidence pass recorded.
func insufficientReason(ev *core.EvidenceReceipt) string {
	for _, gap := range ev.Gaps {
		if trimmed := strings.TrimSpace(gap); trimmed != "" {
			return core.CodeReviewInsufficient + ": " + trimmed
		}
	}
	return core.CodeReviewInsufficient + ": evidence checks insufficient"
}

// gatingSigs splits the visible, open, at-or-above-tier findings of the
// requested category into the sigs that withhold approval and the sigs that
// would withhold but carry no verified quote.
func gatingSigs(entries []any, limit int, category string) (gating, unbacked []string) {
	gating, unbacked = []string{}, []string{}
	for _, item := range readcommon.VisibleFindings(entries) {
		obj, ok := item.(*jq.Obj)
		if !ok || !blocks(obj, limit, category) {
			continue
		}
		sig := jq.R(readcommon.FieldOf(obj, "sig"))
		if Verified(obj) {
			gating = append(gating, sig)
			continue
		}
		unbacked = appendUnique(unbacked, sig)
	}
	return gating, unbacked
}

// blocks reports whether one finding would withhold approval were its quote
// verified: open, in the requested category, at or above the tier.
func blocks(obj *jq.Obj, limit int, category string) bool {
	if readcommon.StatusOf(obj) != "open" {
		return false
	}
	if category != "" && core.CategoryOf(obj) != category {
		return false
	}
	severity, _ := obj.Get("severity")
	return core.TierRank(severity) <= limit
}

// clauseSigs folds the block-clause findings whose quote is verified into the
// gating sigs, and returns the clauses in policy order plus whether any
// blocker was skipped because its finding is unverified.
func clauseSigs(comp *config.ReadCompliance, entries []any, gating, unbacked *[]string) ([]string, bool) {
	if comp == nil {
		return []string{}, false
	}
	backed := verifiedSigs(entries)
	clauses := []string{}
	dropped := false
	for i := range comp.Blockers {
		blocker := &comp.Blockers[i]
		if !backed[blocker.Finding.Sig] {
			dropped = true
			*unbacked = appendUnique(*unbacked, blocker.Finding.Sig)
			continue
		}
		clauses = appendUnique(clauses, blocker.Clause)
		if sig := blocker.Finding.Sig; sig != "" {
			*gating = appendUnique(*gating, sig)
		}
	}
	return clauses, dropped
}

// verifiedSigs collects the sigs of the stored findings whose quote verifies.
func verifiedSigs(entries []any) map[string]bool {
	out := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if !Verified(entry) {
			continue
		}
		if sig := jq.R(readcommon.FieldOf(entry, "sig")); sig != "" {
			out[sig] = true
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

// UnbackedClauses retains insufficiency when compliance filters a candidate.
func UnbackedClauses(comp *config.ReadCompliance, entries []any) bool {
	backed := verifiedSigs(entries)
	for i := range comp.Blockers {
		if !backed[comp.Blockers[i].Finding.Sig] {
			return true
		}
	}
	return false
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

// QuoteStatus reads the evidence standing stored on one finding. A store
// without the field, or with a value this build does not know, reads
// unchecked: never verified.
func QuoteStatus(entry any) string {
	value := readcommon.FieldOf(entry, "verification")
	record, ok := value.(*jq.Obj)
	if !ok {
		return quoteUnchecked
	}
	quote, _ := record.Get("quote")
	if status, ok := quote.(string); ok && status != "" {
		return status
	}
	return quoteUnchecked
}
