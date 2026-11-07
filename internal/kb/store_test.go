package kb_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/kb"
)

// fixtureKB is a store with agent-chosen key order and a history array: the
// stored byte contract is "what was written", never a map re-encode.
const fixtureKB = `{"id":"rec-rule","rule":"Prefer table tests","scope":"internal","provenance":"manual","rigor":"walked","hits":1,"false_hits":0,"judged":1,"status":"active","history":[{"at":"2026-01-01T00:00:00Z","event":"learned","actor":"human"}]}
{"id":"rec-rule-2","rule":"Pin the corpus slug","scope":"internal","provenance":"manual","rigor":"ran","hits":0,"false_hits":0,"judged":0,"status":"retired"}
`

// TestWriteRowsRoundTripsStoreBytes proves the one writer reproduces the
// stored bytes exactly: every verb reads through kb.ReadRows and the rewrite
// path through kb.WriteRows, so key order and the jsonl framing survive.
func TestWriteRowsRoundTripsStoreBytes(t *testing.T) {
	path := seedFixtureStore(t)
	rows := readFixtureRows(t, filepath.Dir(filepath.Dir(path)))
	checkStoredKeyOrder(t, rows[0].MarshalCompact())
	if err := kb.WriteRows(filepath.Dir(filepath.Dir(path)), rows); err != nil {
		t.Fatalf("WriteRows: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != fixtureKB {
		t.Errorf("rewritten store = %q; want byte-identical %q", raw, fixtureKB)
	}
}

// seedFixtureStore writes the fixture into a fresh worktree store and
// returns its path.
func seedFixtureStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".verdict"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".verdict", "kb.jsonl")
	if err := os.WriteFile(path, []byte(fixtureKB), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// readFixtureRows reads the store and checks it parses into two rows.
func readFixtureRows(t *testing.T, root string) []*jq.Obj {
	t.Helper()
	rows, err := kb.ReadRows(root)
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ReadRows = %d rows; want 2", len(rows))
	}
	return rows
}

// checkStoredKeyOrder pins the agent-written key order the fixture carries.
func checkStoredKeyOrder(t *testing.T, got string) {
	t.Helper()
	want := `{"id":"rec-rule","rule":"Prefer table tests","scope":"internal","provenance":"manual",` +
		`"rigor":"walked","hits":1,"false_hits":0,"judged":1,"status":"active",` +
		`"history":[{"at":"2026-01-01T00:00:00Z","event":"learned","actor":"human"}]}`
	if got != want {
		t.Errorf("row 0 = %s; want the stored key order %s", got, want)
	}
}

// TestStoreReadSentinels proves the single reader reports why a store did not
// load, so each caller keeps its own posture: an absent store is ErrMissing
// (learn names the missing store), a malformed line is ErrCorrupt (learn
// refuses corrupt-store, record warns and drops credit).
func TestStoreReadSentinels(t *testing.T) {
	root := t.TempDir()
	if _, err := kb.ReadRows(root); !errors.Is(err, kb.ErrMissing) {
		t.Errorf("absent store = %v; want ErrMissing", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".verdict"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".verdict", "kb.jsonl"), []byte("{broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := kb.ReadRows(root); !errors.Is(err, kb.ErrCorrupt) {
		t.Errorf("malformed store = %v; want ErrCorrupt", err)
	}
	if rows, ok := kb.ReadRowsOK(root); ok || rows != nil {
		t.Errorf("ReadRowsOK on a malformed store = %v, %v; want nil, false", rows, ok)
	}
}

// TestAppendRowKeepsPriorBytes proves the --rule append path never rewrites
// the rows it did not touch.
func TestAppendRowKeepsPriorBytes(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".verdict"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".verdict", "kb.jsonl")
	if err := os.WriteFile(path, []byte(fixtureKB), 0o600); err != nil {
		t.Fatal(err)
	}
	added := `{"id":"rec-rule-3","status":"active"}` + "\n"
	if err := kb.AppendRow(dir, added); err != nil {
		t.Fatalf("AppendRow: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != fixtureKB+added {
		t.Errorf("appended store = %q; want the prior bytes plus the row", raw)
	}
}
