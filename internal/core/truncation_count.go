package core

// ResultCut reports a capped list. A nil total means the scan stopped before
// counting the full domain; next is nil for producers without offset support.
func ResultCut(reason string, returned int, total, next *int) *Truncation {
	return &Truncation{Truncated: true, TruncationReason: reason, Returned: returned, Total: total, NextOffset: next}
}

// ScanCut reports an incomplete scan with an unknown total.
func ScanCut(cut bool, returned int) *Truncation {
	if !cut {
		return nil
	}
	return ResultCut("scan_cap", returned, nil, nil)
}
