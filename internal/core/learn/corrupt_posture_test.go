package learncore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/kb"
)

// TestCorruptStorePostures proves one malformed kb.jsonl drives one reader
// and two deliberate postures: record's commit path (kb.CommitStaged) warns
// and drops staged credit, while learn refuses corrupt-store and changes
// nothing. The decision point is kb.ReadRows; the messages stay per-verb.
func TestCorruptStorePostures(t *testing.T) {
	findings := seedCorruptStore(t)
	checkRecordWarnsAndDrops(t, findings)
	checkLearnRefuses(t, findings)
}

func seedCorruptStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(filepath.Join(".review", "7"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kb.File, []byte("{broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	findings := filepath.Join(".review", "7", "findings.json")
	before := `[{"sig":"a1","status":"open","kb_credit":[{"rule":"r1","to":"hits"}]}]` + "\n"
	if err := os.WriteFile(findings, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	return findings
}

func checkRecordWarnsAndDrops(t *testing.T, findings string) {
	t.Helper()
	var warns []string
	c := kb.New(func(msg string) { warns = append(warns, msg) })
	c.Staged = []map[string]any{{"sig": "a1", "rule": "r1", "outcome": "hits"}}
	c.CommitStaged(findings, "2026-01-01T00:00:00Z")
	if c.Staged != nil {
		t.Error("CommitStaged kept staged credit on a corrupt store")
	}
	if len(warns) == 0 || !strings.Contains(strings.Join(warns, "\n"), "unreadable at commit") {
		t.Errorf("warnings = %v; want the unreadable-at-commit warning", warns)
	}
}

func checkLearnRefuses(t *testing.T, _ string) {
	t.Helper()
	st := &state{req: &Request{}}
	_, ref := st.readRowsStrict("r1")
	if ref == nil || ref.Code != codeCorruptStore {
		t.Fatalf("readRowsStrict on a corrupt store = %v; want corrupt-store", ref)
	}
	if _, err := os.Stat(kb.File); err != nil {
		t.Errorf("learn read removed the store: %v", err)
	}
}
