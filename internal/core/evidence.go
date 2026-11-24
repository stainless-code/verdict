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
type EvidenceReceipt struct { //nolint:govet // field order is the published property order
	Head         string           `json:"head"`
	DiffSHA256   string           `json:"diff_sha256"`
	Quotes       []EvidenceQuote  `json:"quotes,omitempty"`
	Gaps         []string         `json:"gaps,omitempty"`
	Receipts     EvidenceReceipts `json:"receipts"`
	Spec         EvidenceSpec     `json:"spec"`
	Diagram      *EvidenceDiagram `anyOf:"null"            json:"diagram,omitempty" optional:"true"`
	Insufficient bool             `json:"insufficient"`
}

// EvidenceDiagram is the settled disposition of the review's behavioral
// diagram: how many of the supplied nodes and edges the diff anchored, how many
// therefore stayed out of the block, and why the whole diagram was left out
// when it was. A diagram never withholds approval — the counts are published so
// a reader can tell a complete picture from a partial or absent one.
type EvidenceDiagram struct { //nolint:govet // field order is the published property order
	Type     string `enum:"sequence|state|flow" json:"type,omitempty"`
	Title    string `json:"title,omitempty"`
	Supplied int    `json:"supplied"            min:"0"`
	Verified int    `json:"verified"            min:"0"`
	Dropped  int    `json:"dropped"             min:"0"`
	Omitted  string `json:"omitted,omitempty"`
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

// SpecFailure is one spec check that withholds approval: the status that
// failed (`violated` or `unmet`) and the item it graded. The pair travels
// together because the decision names which kind failed, and the list's width
// is the tally.
type SpecFailure struct {
	Status string
	Item   string
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

// SpecCheckUnmetProblem builds the published problem for a spec check that
// reports a requirement nothing implements. It is an error because the review
// is withholding on it, and it names the item rather than the row's note: the
// note is agent prose, and a problem message is rendered into a log.
func SpecCheckUnmetProblem(index int, item string) Problem {
	return Problem{
		Path:    fmt.Sprintf("$.spec_checks[%d].status", index),
		Code:    ProblemCodeSpecCheckUnmet,
		Level:   ProblemLevelError,
		Message: fmt.Sprintf("spec check %d (%q) is unmet", index, item),
		Fix:     "implement the requirement, or record n/a with a note saying why it does not apply",
	}
}

// SpecNoteMissingProblem builds the published problem for an `unmet` row that
// carries no note: a requirement declared missing has to say what is missing.
func SpecNoteMissingProblem(index int) Problem {
	return Problem{
		Path:    fmt.Sprintf("$.spec_checks[%d].note", index),
		Code:    ProblemCodeSpecNoteMissing,
		Level:   ProblemLevelError,
		Message: fmt.Sprintf("spec check %d is unmet and carries no note", index),
		Fix:     `add "note" saying what is missing`,
	}
}

// SpecNAUnjustifiedProblem builds the published problem for an `n/a` row that
// carries no note. It is a warning, promoted to an error by --strict and
// strict_input: rows already in the wild carry this shape, and a warning is the
// honest level for text the review can still be read without.
func SpecNAUnjustifiedProblem(index int) Problem {
	return Problem{
		Path:    fmt.Sprintf("$.spec_checks[%d].note", index),
		Code:    ProblemCodeSpecNAUnjustified,
		Level:   ProblemLevelWarning,
		Message: fmt.Sprintf("spec check %d is n/a and carries no note", index),
		Fix:     `add "note" saying why the item does not apply`,
	}
}
