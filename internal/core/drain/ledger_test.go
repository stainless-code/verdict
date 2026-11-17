package drain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestAppendLedgerKeepsLegacyEntries pins the shared format: one drain entry
// appended under the lock without rewriting the address-reviews entries.
func TestAppendLedgerKeepsLegacyEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), KeyLedgerJSON)
	legacy := `[{"thread":"PRRT_old","reply_id":"900","at":"2026-11-04T00:00:00Z"}]` + "\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	entries := []LedgerEntry{
		{Thread: "PRRT_1", ReplyID: "77", At: "2026-11-05T00:00:00Z", InReplyTo: "102", By: ReplyBy},
		{Thread: "PRRT_2", ReplyID: "88", At: "2026-11-05T00:01:00Z", InReplyTo: "202", By: ReplyBy},
	}
	for _, entry := range entries {
		if err := AppendLedger(path, entry); err != nil {
			t.Fatalf("AppendLedger: %v", err)
		}
	}
	got, err := readLedgerForTest(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Thread != "PRRT_old" || got[0].InReplyTo != "" ||
		got[1] != entries[0] || got[2] != entries[1] {
		t.Fatalf("ledger = %+v", got)
	}
}

func readLedgerForTest(path string) ([]LedgerEntry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entries []LedgerEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}
