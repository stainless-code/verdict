package drain

import (
	"encoding/json"
	"errors"
	"io/fs"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/storelock"
)

// AppendLedger records one successful reply under the store lock. A crash
// after a host write but before this call is the only unrecorded window; once
// the entry lands, the next drain reads it and makes no second host write.
func AppendLedger(path string, entry *LedgerEntry) error {
	unlock, err := storelock.Acquire(path, storelock.DefaultWait)
	if err != nil {
		return jq.NewErrf("could not lock %s: %v", path, err)
	}
	defer unlock()
	entries, err := readLedger(path)
	if err != nil {
		return err
	}
	entries = append(entries, *entry)
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return jq.NewErrf("could not encode %s: %v", path, err)
	}
	if err := atomic.WriteFile(path, append(raw, '\n')); err != nil {
		return jq.NewErrf("could not write %s: %v", path, err)
	}
	return nil
}

// readLedger reads the shared address-reviews ledger. An absent file is an
// empty one; a corrupt file refuses instead of being read as nobody replied.
func readLedger(path string) ([]LedgerEntry, error) {
	raw, err := jq.CappedFile(path, jq.MaxPRBytes)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []LedgerEntry{}, nil
		}
		return nil, jq.NewErrf("unreadable %s: %v", path, err)
	}
	var entries []LedgerEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, jq.NewErrf("unreadable %s: %v", path, err)
	}
	return entries, nil
}
