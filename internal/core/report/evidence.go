package reportcore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
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

// loadStateEvidence reads record.json's evidence receipt and verifies that
// its pinned head matches the bundle's current head. Missing, unreadable, or
// stale receipts never fall back to a legacy pass path.
func loadStateEvidence(st *state) {
	raw, err := jq.CappedFile(st.dir+readcommon.RecordSuffix, jq.MaxPRBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			st.evidence = &evidenceState{kind: evidenceMissing}
			return
		}
		st.evidence = &evidenceState{kind: evidenceUnreadable, recordExists: true}
		return
	}
	var rec core.Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		st.evidence = &evidenceState{kind: evidenceUnreadable, recordExists: true}
		return
	}
	if rec.Evidence == nil {
		st.evidence = &evidenceState{kind: evidenceMissing, recordExists: true}
		return
	}
	if st.headSHA == "" || rec.Evidence.Head != st.headSHA {
		st.evidence = &evidenceState{
			receipt:      rec.Evidence,
			recordedHead: rec.Evidence.Head,
			kind:         evidenceStale,
			recordExists: true,
		}
		return
	}
	st.evidence = &evidenceState{
		receipt:      rec.Evidence,
		recordedHead: rec.Evidence.Head,
		kind:         evidencePresent,
		recordExists: true,
	}
}

// currentEvidenceReceipt returns the evidence receipt only when it is
// present and matches the current bundle head.
func (st *state) currentEvidenceReceipt() *core.EvidenceReceipt {
	if st.evidence == nil || st.evidence.kind != evidencePresent {
		return nil
	}
	return st.evidence.receipt
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

func evidenceMissingDetail(head string) string {
	return fmt.Sprintf("evidence receipt missing for head %s [%s]", shortHeadLabel(head), core.CodeReviewInsufficient)
}

func evidenceStaleDetail(head, recorded string) string {
	return fmt.Sprintf("evidence receipt stale for head %s (recorded %s) [%s]",
		shortHeadLabel(head), shortHeadLabel(recorded), core.CodeReviewInsufficient)
}

func evidenceUnreadableDetail(head string) string {
	return fmt.Sprintf("evidence receipt unreadable for head %s [%s]", shortHeadLabel(head), core.CodeReviewInsufficient)
}

func shortHeadLabel(sha string) string {
	if sha == "" {
		return "unknown"
	}
	return readcommon.ShortSHA(sha)
}
