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

// VerifyReceipts checks receipts against idx under A3 rules. A receipt's hunk
// key is one of the rows hunks.tsv prints: the canonical key — the NEW row when
// that side is non-empty, else the OLD row of a pure deletion — or the OLD row
// of a modified hunk, which names the same change. A receipt quote matches any
// line of the resolved hunk on either side via the A2 matcher. Coverage counts
// canonical keys only, so supplying both rows for one hunk counts it once, and
// it requires 100% of non-exempt hunks.
func VerifyReceipts(idx *DiffIndex, receipts []ReceiptInput, exempt func(string) bool) ReceiptCoverage {
	required, exemptCount := idx.partitionReceiptKeys(exempt)
	checks := make([]ReceiptCheck, len(receipts))
	coveredKeys := make(map[string]struct{}, len(required))

	for i, r := range receipts {
		chk, h := verifyOneReceipt(idx, i, r)
		checks[i] = chk
		if chk.Matched && h != nil {
			coveredKeys[h.Key()] = struct{}{}
		}
	}

	markDuplicateMismatches(idx, checks, coveredKeys)
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

// verifyOneReceipt validates a single receipt against the hunk its key names,
// returning that hunk alongside the check so coverage can count its canonical
// key. The check always echoes the key the receipt supplied.
func verifyOneReceipt(idx *DiffIndex, index int, r ReceiptInput) (ReceiptCheck, *DiffHunk) {
	h, ok := idx.hunkForReceipt(r.Hunk)
	if !ok {
		return ReceiptCheck{
			Index:  index,
			Hunk:   r.Hunk,
			Reason: ReasonReceiptHunkUnknown,
		}, nil
	}
	normText, quoteLines := NormalizeQuote(r.Quote)
	if len(quoteLines) > MaxQuoteLines || len(normText) > MaxQuoteBytes {
		return ReceiptCheck{
			Index:  index,
			Hunk:   r.Hunk,
			Reason: ReasonQuoteTooLarge,
		}, h
	}
	if side, matched := matchReceiptInHunk(h, quoteLines); matched {
		return ReceiptCheck{
			Index:   index,
			Hunk:    r.Hunk,
			Side:    side,
			Matched: true,
		}, h
	}
	return ReceiptCheck{
		Index:  index,
		Hunk:   r.Hunk,
		Reason: ReasonReceiptMismatch,
	}, h
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
// of errors (§3.2). The covered set holds canonical keys, so an OLD row is
// resolved to its hunk before the lookup.
func markDuplicateMismatches(idx *DiffIndex, checks []ReceiptCheck, covered map[string]struct{}) {
	for i := range checks {
		if duplicateMismatch(idx, &checks[i], covered) {
			checks[i].DuplicateMismatch = true
		}
	}
}

// duplicateMismatch reports whether check is a quote mismatch whose canonical
// hunk another receipt already covered.
func duplicateMismatch(idx *DiffIndex, check *ReceiptCheck, covered map[string]struct{}) bool {
	if check.Matched || check.Reason != ReasonReceiptMismatch {
		return false
	}
	h, ok := idx.hunkForReceipt(check.Hunk)
	if !ok {
		return false
	}
	_, alreadyCovered := covered[h.Key()]
	return alreadyCovered
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
