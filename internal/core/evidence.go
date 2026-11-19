package core

import (
	"fmt"
	"strings"
)

// CodeReviewInsufficient names the refusal emitted when deterministic
// evidence checks leave open gaps in quotes, receipts, or spec coverage.
const CodeReviewInsufficient = "review-insufficient"

// Quote-mismatch failure reasons mapped to ProblemCodeQuoteMismatch; every
// other non-empty quote failure reason maps to ProblemCodeQuoteNotCheckable.
const (
	quoteReasonTextDiffers       = "text-differs"
	quoteReasonRangeInvalid      = "range-invalid"
	quoteReasonDeletedLine       = "deleted-line"
	quoteReasonRangeCrossesHunks = "range-crosses-hunks"
)

// EvidenceReceipt is the deterministic evidence verification summary stored
// on record.json and record --json data. Head and DiffSHA256 pin the bundle
// bytes the check read so report --check can detect stale receipts.
type EvidenceReceipt struct {
	Head         string           `json:"head"`
	DiffSHA256   string           `json:"diff_sha256"`
	Quotes       []EvidenceQuote  `json:"quotes,omitempty"`
	Gaps         []string         `json:"gaps,omitempty"`
	Receipts     EvidenceReceipts `json:"receipts"`
	Spec         EvidenceSpec     `json:"spec"`
	Insufficient bool             `json:"insufficient"`
}

// EvidenceQuote records one finding's deterministic quote-check outcome.
type EvidenceQuote struct {
	Path   string `json:"path"`
	Side   string `enum:"RIGHT|LEFT"          json:"side"`
	Status string `enum:"verified|unverified" json:"status"`
	Reason string `json:"reason,omitempty"`
	Line   int    `json:"line"                min:"0"`
}

// EvidenceReceipts summarizes changed-hunk reading receipt coverage.
type EvidenceReceipts struct {
	Total    int  `json:"total"    min:"0"`
	Covered  int  `json:"covered"  min:"0"`
	Exempt   int  `json:"exempt"   min:"0"`
	Complete bool `json:"complete"`
}

// EvidenceSpec summarizes specification checklist verification coverage.
type EvidenceSpec struct {
	Total   int  `json:"total"   min:"0"`
	Covered int  `json:"covered" min:"0"`
	Min     int  `json:"min"     min:"0"`
	Met     bool `json:"met"`
}

// InsufficientRefusal returns a typed review-insufficient refusal carrying
// the open evidence problems.
func InsufficientRefusal(reason string, problems []Problem) *RefusalError {
	msg := strings.TrimSpace(reason)
	if msg == "" {
		msg = "evidence checks insufficient"
	}
	return &RefusalError{
		Code:     CodeReviewInsufficient,
		Message:  msg,
		Problems: problems,
	}
}

// QuoteProblemCode maps a quote-verification failure reason to its published
// problem code: text mismatches map to quote-mismatch and bundle
// non-checkable reasons map to quote-not-checkable.
func QuoteProblemCode(reason string) string {
	switch reason {
	case "":
		return ""
	case quoteReasonTextDiffers, quoteReasonDeletedLine, quoteReasonRangeCrossesHunks, quoteReasonRangeInvalid:
		return ProblemCodeQuoteMismatch
	default:
		return ProblemCodeQuoteNotCheckable
	}
}

// ReceiptMissingProblem builds the published problem for a non-exempt
// changed hunk that carries no matched reading receipt.
func ReceiptMissingProblem(hunk string) Problem {
	return Problem{
		Path:    "$.receipts",
		Code:    ProblemCodeReceiptMissing,
		Level:   ProblemLevelError,
		Message: fmt.Sprintf("hunk %s has no matched receipt", hunk),
		Fix:     fmt.Sprintf(`add {"hunk":%q,"quote":"<exact line from that hunk>"}`, hunk),
	}
}

// SpecCheckMissingProblem builds the published problem for an enumerated
// specification item that has no matching spec_checks entry.
func SpecCheckMissingProblem(index int, item string) Problem {
	return Problem{
		Path:    "$.spec_checks",
		Code:    ProblemCodeSpecCheckMissing,
		Level:   ProblemLevelError,
		Message: fmt.Sprintf("spec item %d (%q) has no check", index, item),
		Fix:     "add a spec_checks[] entry quoting that item",
	}
}
