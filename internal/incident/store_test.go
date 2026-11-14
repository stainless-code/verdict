package incident

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

const (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// TestUpsertIsIdempotentAndSorted pins the ledger's bytes: a second attach of
// the same (bundle, sha) pair writes nothing, and a new pair lands in bundle
// order while an existing line keeps its own fields verbatim.
func TestUpsertIsIdempotentAndSorted(t *testing.T) {
	root := t.TempDir()
	seeded := `{"schema_version":"1","bundle":"local-b","sha":"` + shaB + `","paths":["src/b.ts"],` +
		`"note":null,"url":null,"detected_at":"2026-11-14T00:00:00Z","run":"kept"}`
	writeStore(t, root, seeded+"\n")

	appended, err := Upsert(root, Row{
		SchemaVersion: "1", Bundle: "local-a", Sha: shaA, Paths: []string{"src/a.ts"},
		Note: new("reverted in the same hour"), URL: new("https://example.test/inc/1"),
		DetectedAt: "2026-11-14T00:00:01Z",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if !appended {
		t.Fatal("first attach reported an existing row")
	}
	want := `{"schema_version":"1","bundle":"local-a","sha":"` + shaA + `","paths":["src/a.ts"],` +
		`"note":"reverted in the same hour","url":"https://example.test/inc/1",` +
		`"detected_at":"2026-11-14T00:00:01Z"}` + "\n" + seeded + "\n"
	if got := readStore(t, root); got != want {
		t.Fatalf("ledger = %q; want %q", got, want)
	}

	before := readStore(t, root)
	appended, err = Upsert(root, Row{
		SchemaVersion: "1", Bundle: "local-a", Sha: shaA, Paths: []string{"src/other.ts"},
		DetectedAt: "2027-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	if appended {
		t.Fatal("second attach of the same pair reported an append")
	}
	if got := readStore(t, root); got != before {
		t.Fatalf("second attach moved the ledger bytes: %q", got)
	}
}

// TestReadRefusesUnknownSchemaVersion proves a row this build does not
// understand refuses the read instead of being skipped (the corrupt-store
// posture internal/core/learn and internal/core/trend hold).
func TestReadRefusesUnknownSchemaVersion(t *testing.T) {
	root := t.TempDir()
	writeStore(t, root, `{"schema_version":"2","bundle":"local-a","sha":"`+shaA+`",`+
		`"paths":["src/a.ts"],"note":null,"url":null,"detected_at":"2026-11-14T00:00:00Z"}`+"\n")
	if _, err := Read(root); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Read = %v; want ErrCorrupt", err)
	}
}

// TestReadRefusesOverCapStore proves the size cap refuses an oversized ledger
// rather than decoding it.
func TestReadRefusesOverCapStore(t *testing.T) {
	root := t.TempDir()
	writeStore(t, root, strings.Repeat(" ", int(jq.MaxKBBytes)+1))
	if _, err := Read(root); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Read = %v; want ErrCorrupt", err)
	}
}

// TestReadMissingLedgerIsMissing proves an absent ledger is its own answer, so
// a first attach starts from empty and `trend --incidents` reads zeros.
func TestReadMissingLedgerIsMissing(t *testing.T) {
	if _, err := Read(t.TempDir()); !errors.Is(err, ErrMissing) {
		t.Fatalf("Read = %v; want ErrMissing", err)
	}
}

func writeStore(t *testing.T, root, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(File))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readStore(t *testing.T, root string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(File)))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
