package reverts

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// plantLedger writes body at `.verdict/reverts.jsonl` under a fresh temp root.
func plantLedger(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".verdict"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(File)), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestReadKeepsRowBytes proves the reader validates each row's values and hands
// back the row's own bytes, so a writer that keeps a row untouched keeps its
// formatting too.
func TestReadKeepsRowBytes(t *testing.T) {
	t.Parallel()
	first := `{"schema_version":"1","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",` +
		`"subject":"Revert \"feat: add a guard\"","reverts":["bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"],` +
		`"date":"2024-01-03T00:00:00Z","paths":["main.go","pkg/a.go"]}`
	second := `{ "sha":"cccccccccccccccccccccccccccccccccccccccc", "paths":["b.go"], "date":"2024-01-04T00:00:00Z",` +
		` "subject":"Revert \"second\"", "reverts":[], "schema_version":"1"}`
	root := plantLedger(t, first+"\n"+second+"\n")
	entries, err := Read(root)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("rows = %d; want 2", len(entries))
	}
	if entries[0].Text != first || entries[1].Text != second {
		t.Errorf("row bytes = %q / %q; want the stored bytes", entries[0].Text, entries[1].Text)
	}
	want := Row{
		SchemaVersion: "1",
		SHA:           "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Subject:       `Revert "feat: add a guard"`,
		Reverts:       []string{"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		Date:          "2024-01-03T00:00:00Z",
		Paths:         []string{"main.go", "pkg/a.go"},
	}
	if got := entries[0].Row; got.SchemaVersion != want.SchemaVersion || got.SHA != want.SHA ||
		got.Subject != want.Subject || got.Date != want.Date ||
		strings.Join(got.Reverts, ",") != strings.Join(want.Reverts, ",") ||
		strings.Join(got.Paths, ",") != strings.Join(want.Paths, ",") {
		t.Errorf("first row = %+v; want %+v", got, want)
	}
}

// TestReadReportsAbsentLedger proves the reader distinguishes no memory from
// unreadable memory: a caller that treats both the same would warn on every
// repository that never scanned a revert.
func TestReadReportsAbsentLedger(t *testing.T) {
	t.Parallel()
	if _, err := Read(t.TempDir()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Read(absent) = %v; want fs.ErrNotExist", err)
	}
}

// TestReadRefusesUnknownSchemaVersion proves a row written by another schema
// build is a named defect, never an empty row a reader would quote.
func TestReadRefusesUnknownSchemaVersion(t *testing.T) {
	t.Parallel()
	root := plantLedger(t, `{"schema_version":"2","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
		`"subject":"Revert \"x\"","reverts":[],"date":"2024-01-01T00:00:00Z","paths":["main.go"]}`+"\n")
	_, err := Read(root)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Read(unknown schema) = %v; want ErrCorrupt", err)
	}
	const want = `field "schema_version" "2" is not 1`
	if err.Error() != want {
		t.Errorf("defect = %q; want %q", err.Error(), want)
	}
}

// TestReadRefusesOverCapLedger proves the reader's cap is the shared one, so a
// ledger no reader can load is an error rather than a partial read.
func TestReadRefusesOverCapLedger(t *testing.T) {
	t.Parallel()
	root := plantLedger(t, strings.Repeat("\n", int(jq.MaxKBBytes)+1))
	if _, err := Read(root); !errors.Is(err, jq.ErrTooLarge) {
		t.Fatalf("Read(over cap) = %v; want jq.ErrTooLarge", err)
	}
}

// TestEncodeRendersPublishedOrder pins the stored key order: the ledger is
// tracked and diffed, so a reordered row is repository churn.
func TestEncodeRendersPublishedOrder(t *testing.T) {
	t.Parallel()
	row := Row{
		SchemaVersion: "1",
		SHA:           "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Subject:       `Revert "feat: add a guard"`,
		Reverts:       []string{"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		Date:          "2024-01-03T00:00:00Z",
		Paths:         []string{"main.go"},
	}
	const want = `{"schema_version":"1","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",` +
		`"subject":"Revert \"feat: add a guard\"",` +
		`"reverts":["bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"],` +
		`"date":"2024-01-03T00:00:00Z","paths":["main.go"]}`
	if got := Encode(&row); got != want {
		t.Errorf("Encode:\n%s\nwant:\n%s", got, want)
	}
}
