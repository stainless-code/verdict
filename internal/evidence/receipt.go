package evidence

// Receipt verification failure reasons.
const (
	ReasonReceiptHunkUnknown = "receipt-hunk-unknown"
	ReasonReceiptMismatch    = "receipt-mismatch"
)

// ReceiptInput is one agent-supplied receipt claiming a changed hunk was read.
type ReceiptInput struct {
	Hunk  string
	Quote string
}

// ReceiptCheck is the verification outcome for one receipt entry.
type ReceiptCheck struct {
	Hunk              string
	Reason            string
	Side              string
	Index             int
	Matched           bool
	DuplicateMismatch bool
}

// ReceiptCoverage summarizes receipt verification and coverage over all
// non-exempt changed hunks.
type ReceiptCoverage struct {
	Checks   []ReceiptCheck
	Missing  []string
	Required []string
	Total    int
	Covered  int
	Exempt   int
	Complete bool
}

// VerifyReceipts checks receipts against idx under A3 rules: each hunk is
// keyed by its NEW hunks.tsv row when its NEW side is non-empty, else its OLD
// row, and a receipt quote matches any line of that hunk on either side via
// the A2 matcher. Coverage requires 100% of non-exempt hunks.
func VerifyReceipts(idx *DiffIndex, receipts []ReceiptInput, exempt func(string) bool) ReceiptCoverage {
	required, exemptCount := idx.partitionReceiptKeys(exempt)
	checks := make([]ReceiptCheck, len(receipts))
	coveredKeys := make(map[string]struct{}, len(required))

	for i, r := range receipts {
		chk := verifyOneReceipt(idx, i, r)
		checks[i] = chk
		if chk.Matched {
			coveredKeys[chk.Hunk] = struct{}{}
		}
	}

	markDuplicateMismatches(checks, coveredKeys)
	missing := missingReceiptKeys(required, coveredKeys)
	total := len(required)
	covered := total - len(missing)

	return ReceiptCoverage{
		Checks:   checks,
		Missing:  missing,
		Required: required,
		Total:    total,
		Covered:  covered,
		Exempt:   exemptCount,
		Complete: len(missing) == 0,
	}
}

// verifyOneReceipt validates a single receipt against its canonical hunk.
func verifyOneReceipt(idx *DiffIndex, index int, r ReceiptInput) ReceiptCheck {
	h, ok := idx.HunkByKey(r.Hunk)
	if !ok {
		return ReceiptCheck{
			Index:  index,
			Hunk:   r.Hunk,
			Reason: ReasonReceiptHunkUnknown,
		}
	}
	normText, quoteLines := NormalizeQuote(r.Quote)
	if len(quoteLines) > MaxQuoteLines || len(normText) > MaxQuoteBytes {
		return ReceiptCheck{
			Index:  index,
			Hunk:   r.Hunk,
			Reason: ReasonQuoteTooLarge,
		}
	}
	if side, matched := matchReceiptInHunk(h, quoteLines); matched {
		return ReceiptCheck{
			Index:   index,
			Hunk:    r.Hunk,
			Side:    side,
			Matched: true,
		}
	}
	return ReceiptCheck{
		Index:  index,
		Hunk:   r.Hunk,
		Reason: ReasonReceiptMismatch,
	}
}

// matchReceiptInHunk tries the A2 matcher over the entire hunk on NEW first,
// then OLD.
func matchReceiptInHunk(h *DiffHunk, quoteLines []string) (string, bool) {
	if len(quoteLines) == 0 {
		return "", false
	}
	if len(h.NewLines) > 0 {
		winEnd := addClampedMax(h.NewStart, len(h.NewLines)-1)
		if _, _, ok := matchLinesInHunk(h.NewLines, h.NewStart, quoteLines, h.NewStart, winEnd); ok {
			return SideRight, true
		}
	}
	if len(h.OldLines) > 0 {
		winEnd := addClampedMax(h.OldStart, len(h.OldLines)-1)
		if _, _, ok := matchLinesInHunk(h.OldLines, h.OldStart, quoteLines, h.OldStart, winEnd); ok {
			return SideLeft, true
		}
	}
	return "", false
}

// markDuplicateMismatches flags non-matching receipts for hunks that already
// have another matched receipt, so callers can report them as warnings instead
// of errors (§3.2).
func markDuplicateMismatches(checks []ReceiptCheck, covered map[string]struct{}) {
	for i := range checks {
		if !checks[i].Matched && checks[i].Reason == ReasonReceiptMismatch {
			if _, ok := covered[checks[i].Hunk]; ok {
				checks[i].DuplicateMismatch = true
			}
		}
	}
}

// missingReceiptKeys returns the required keys not present in covered,
// preserving required's sorted order.
func missingReceiptKeys(required []string, covered map[string]struct{}) []string {
	var missing []string
	for _, key := range required {
		if _, ok := covered[key]; !ok {
			missing = append(missing, key)
		}
	}
	return missing
}
