package reportcore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/approval"
	"github.com/stainless-code/verdict/internal/core/risk"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

type evidenceLoadKind int

const (
	evidenceMissing evidenceLoadKind = iota
	evidenceUnreadable
	evidenceStale
	evidencePresent
)

type evidenceState struct {
	receipt      *core.EvidenceReceipt
	recordedHead string
	kind         evidenceLoadKind
	recordExists bool
}

const (
	quoteStatusVerified   = "verified"
	quoteStatusUnverified = "unverified"
	gateFailSuffix        = " — gate fails"
	gateOKSuffix          = " — gate passes"
	evidenceWarnPrefix    = "evidence: "
)

// loadStateEvidence reads the stored evidence receipt — post.json first, then
// record.json, the precedence the coverage dimension reads — and verifies that
// the identity it pinned is the one this bundle still carries: the reviewed
// head, or the worktree diff digest when the bundle has no commit. Missing,
// unreadable, or stale receipts never fall back to a legacy pass path.
func loadStateEvidence(st *state) {
	var stale, unreadable *evidenceState
	for _, suffix := range []string{readcommon.PostSuffix, readcommon.RecordSuffix} {
		state := storedEvidence(st, suffix)
		switch state.kind {
		case evidencePresent:
			st.evidence = state
			return
		case evidenceStale:
			if stale == nil {
				stale = state
			}
		case evidenceUnreadable:
			unreadable = state
		case evidenceMissing:
			// A missing receipt is the default below, once both documents are read.
		}
	}
	switch {
	case stale != nil:
		st.evidence = stale
	case unreadable != nil:
		st.evidence = unreadable
	default:
		st.evidence = &evidenceState{kind: evidenceMissing, recordExists: st.recordExists()}
	}
}

// storedEvidence reads one stored receipt's evidence block. A file that exists
// without one has nothing to say about this bundle's evidence, so the caller
// keeps reading: post.json written before evidence existed must not hide the
// record.json beside it.
func storedEvidence(st *state, suffix string) *evidenceState {
	raw, err := jq.CappedFile(st.dir+suffix, jq.MaxPRBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &evidenceState{kind: evidenceMissing}
		}
		return &evidenceState{kind: evidenceUnreadable, recordExists: true}
	}
	var doc struct {
		Evidence *core.EvidenceReceipt `json:"evidence"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return &evidenceState{kind: evidenceUnreadable, recordExists: true}
	}
	if doc.Evidence == nil {
		return &evidenceState{kind: evidenceMissing, recordExists: true}
	}
	state := &evidenceState{receipt: doc.Evidence, recordedHead: doc.Evidence.Head, recordExists: true}
	if evidenceCurrent(st, doc.Evidence) {
		state.kind = evidencePresent
	} else {
		state.kind = evidenceStale
	}
	return state
}

// recordExists reports whether the bundle stores either receipt at all, which
// separates "never recorded" from "recorded without evidence".
func (st *state) recordExists() bool {
	for _, suffix := range []string{readcommon.PostSuffix, readcommon.RecordSuffix} {
		if _, err := os.Stat(st.dir + suffix); err == nil {
			return true
		}
	}
	return false
}

// evidenceCurrent reports whether the receipt pinned the bytes this bundle
// still carries. A bundle with a commit is identified by its head; a dirty
// worktree bundle has no head, so its verified diff digest is the identity the
// receipt has to match, exactly as the coverage receipt does.
func evidenceCurrent(st *state, ev *core.EvidenceReceipt) bool {
	current := risk.CoverageIdentity(st.dir, st.headSHA)
	return current != "" && receiptIdentity(ev) == current
}

// receiptIdentity renders the receipt's pinned identity the way every verdict
// receipt states it: the head, or the diff digest when there is no commit.
func receiptIdentity(ev *core.EvidenceReceipt) string {
	return readcommon.ReceiptIdentity(map[string]any{"head": ev.Head, "diff_sha256": ev.DiffSHA256})
}

// currentEvidenceReceipt returns the evidence receipt only when it is
// present and matches the current bundle head.
func (st *state) currentEvidenceReceipt() *core.EvidenceReceipt {
	if st.evidence == nil || st.evidence.kind != evidencePresent {
		return nil
	}
	return st.evidence.receipt
}

// evidenceInput is the approval decision's evidence input: the current receipt
// when the bundle carries one, and otherwise the sentence naming what is
// missing. A missing, stale, or unreadable receipt never approves a review.
func (st *state) evidenceInput() approval.Evidence {
	if receipt := st.currentEvidenceReceipt(); receipt != nil {
		return approval.Evidence{Receipt: receipt}
	}
	return approval.Evidence{Missing: st.evidenceAbsence()}
}

// evidenceAbsence names why no current receipt backs the gated head.
func (st *state) evidenceAbsence() string {
	if st.evidence == nil {
		return evidenceMissingSentence(st.headSHA)
	}
	switch st.evidence.kind {
	case evidenceStale:
		return evidenceStaleSentence(st.headSHA, st.evidence.recordedHead)
	case evidenceUnreadable:
		return evidenceUnreadableSentence(st.headSHA)
	default:
		return evidenceMissingSentence(st.headSHA)
	}
}

// warnEvidence prints one advisory warning when an un-checked report sees an
// insufficient, stale, or unreadable evidence receipt, without failing.
func warnEvidence(st *state) {
	if st.flags.Check != "" || st.evidence == nil {
		return
	}
	switch st.evidence.kind {
	case evidencePresent:
		if st.evidence.receipt.Insufficient {
			st.warn(evidenceWarnPrefix + evidenceInsufficientDetail(st.evidence.receipt))
		}
	case evidenceStale:
		st.warn(evidenceWarnPrefix + evidenceStaleDetail(st.headSHA, st.evidence.recordedHead))
	case evidenceUnreadable:
		st.warn(evidenceWarnPrefix + evidenceUnreadableDetail(st.headSHA))
	case evidenceMissing:
		if st.evidence.recordExists {
			st.warn(evidenceWarnPrefix + evidenceMissingDetail(st.headSHA))
		}
	}
}

// gateEvidenceCheck evaluates the always-on evidence dimension under --check.
// Pass requires a current-head receipt with insufficient=false; insufficient
// fails; missing, stale, or unreadable reads unknown (which fails the gate).
func gateEvidenceCheck(st *state) gateCheck {
	kind := evidenceMissing
	if st.evidence != nil {
		kind = st.evidence.kind
	}
	switch kind {
	case evidencePresent:
		return gatePresentEvidence(st.evidence.receipt)
	case evidenceStale:
		detail := evidenceStaleDetail(st.headSHA, st.evidence.recordedHead)
		return gateCheck{
			dim:  core.GateDimension{ID: dimEvidence, Status: dimUnknown, Detail: detail},
			line: checkPrefix + detail + gateFailSuffix,
		}
	case evidenceUnreadable:
		detail := evidenceUnreadableDetail(st.headSHA)
		return gateCheck{
			dim:  core.GateDimension{ID: dimEvidence, Status: dimUnknown, Detail: detail},
			line: checkPrefix + detail + gateFailSuffix,
		}
	default:
		detail := evidenceMissingDetail(st.headSHA)
		return gateCheck{
			dim:  core.GateDimension{ID: dimEvidence, Status: dimUnknown, Detail: detail},
			line: checkPrefix + detail + gateFailSuffix,
		}
	}
}

func gatePresentEvidence(ev *core.EvidenceReceipt) gateCheck {
	if ev.Insufficient {
		detail := evidenceInsufficientDetail(ev)
		count := max(len(ev.Gaps), 1)
		return gateCheck{
			dim:  core.GateDimension{ID: dimEvidence, Status: dimFail, Count: new(count), Detail: detail},
			line: checkPrefix + detail + gateFailSuffix,
		}
	}
	detail := evidenceSatisfiedDetail(ev)
	return gateCheck{
		dim:  core.GateDimension{ID: dimEvidence, Status: dimPass, Count: new(0), Detail: detail},
		line: checkPrefix + detail + gateOKSuffix,
	}
}

func evidenceSatisfiedDetail(ev *core.EvidenceReceipt) string {
	verified := countQuoteStatus(ev.Quotes, quoteStatusVerified)
	return fmt.Sprintf(
		"evidence checks satisfied (%d quotes verified, %d/%d hunks receipted, %d/%d spec items checked)",
		verified, ev.Receipts.Covered, ev.Receipts.Total, ev.Spec.Covered, ev.Spec.Total,
	)
}

func evidenceInsufficientDetail(ev *core.EvidenceReceipt) string {
	parts := evidenceGapParts(ev)
	if len(parts) == 0 {
		if len(ev.Gaps) > 0 {
			parts = append(parts, fmt.Sprintf("%d evidence gap(s)", len(ev.Gaps)))
		} else {
			parts = append(parts, core.CodeReviewInsufficient)
		}
	}
	return fmt.Sprintf("evidence checks insufficient (%s) [%s]", strings.Join(parts, ", "), core.CodeReviewInsufficient)
}

func evidenceGapParts(ev *core.EvidenceReceipt) []string {
	var parts []string
	if unv := countQuoteStatus(ev.Quotes, quoteStatusUnverified); unv > 0 {
		parts = append(parts, fmt.Sprintf("%d quote gap(s)", unv))
	}
	if missing := ev.Receipts.Total - ev.Receipts.Covered; missing > 0 {
		parts = append(parts, formatMissingHunks(missing))
	}
	if !ev.Spec.Met && ev.Spec.Total > 0 {
		parts = append(parts, fmt.Sprintf("spec coverage %d/%d below %d%%", ev.Spec.Covered, ev.Spec.Total, ev.Spec.Min))
	}
	return parts
}

func formatMissingHunks(n int) string {
	if n == 1 {
		return "1 hunk without receipt"
	}
	return fmt.Sprintf("%d hunks without receipt", n)
}

func countQuoteStatus(quotes []core.EvidenceQuote, want string) int {
	n := 0
	for i := range quotes {
		if quotes[i].Status == want {
			n++
		}
	}
	return n
}

// The sentence helpers name one absence without the review-insufficient code:
// the check line adds the code in brackets and the approval reason prefixes it.
func evidenceMissingSentence(head string) string {
	return "evidence receipt missing for head " + shortHeadLabel(head)
}

func evidenceStaleSentence(head, recorded string) string {
	return fmt.Sprintf("evidence receipt stale for head %s (recorded %s)",
		shortHeadLabel(head), shortHeadLabel(recorded))
}

func evidenceUnreadableSentence(head string) string {
	return "evidence receipt unreadable for head " + shortHeadLabel(head)
}

func evidenceMissingDetail(head string) string {
	return evidenceMissingSentence(head) + codedInsufficient()
}

func evidenceStaleDetail(head, recorded string) string {
	return evidenceStaleSentence(head, recorded) + codedInsufficient()
}

func evidenceUnreadableDetail(head string) string {
	return evidenceUnreadableSentence(head) + codedInsufficient()
}

// codedInsufficient renders the review-insufficient code the detail sentences
// carry in brackets.
func codedInsufficient() string {
	return " [" + core.CodeReviewInsufficient + "]"
}

func shortHeadLabel(sha string) string {
	if sha == "" {
		return "unknown"
	}
	return readcommon.ShortSHA(sha)
}
