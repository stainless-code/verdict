package incident

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

	appended, err := Upsert(root, &Row{
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
	appended, err = Upsert(root, &Row{
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

// TestValidateRefusesUnstorableRows pins every rule the row has to satisfy:
// each case is one mutation of a storable row, and the message names the field.
func TestValidateRefusesUnstorableRows(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Row)
		want   string
	}{
		{
			name: "schema version", mutate: func(r *Row) { r.SchemaVersion = "2" },
			want: `schema_version "2" is not "1"`,
		},
		{
			name: "empty bundle", mutate: func(r *Row) { r.Bundle = "" },
			want: `bundle "" is not one bundle name`,
		},
		{
			name: "bundle with a separator", mutate: func(r *Row) { r.Bundle = "a/b" },
			want: `bundle "a/b" is not one bundle name`,
		},
		{
			name: "parent bundle", mutate: func(r *Row) { r.Bundle = ".." },
			want: `bundle ".." is not one bundle name`,
		},
		{
			name: "short sha", mutate: func(r *Row) { r.Sha = "abc" },
			want: `sha "abc" is not 7-40 lowercase hex digits`,
		},
		{
			name: "uppercase sha", mutate: func(r *Row) { r.Sha = "AAAAAAA" },
			want: `sha "AAAAAAA" is not 7-40 lowercase hex digits`,
		},
		{
			name: "bare date", mutate: func(r *Row) { r.DetectedAt = "2026-11-14" },
			want: `detected_at "2026-11-14" is not RFC 3339`,
		},
		{
			name: "no paths", mutate: func(r *Row) { r.Paths = nil },
			want: `paths holds no path`,
		},
		{
			name: "empty path", mutate: func(r *Row) { r.Paths = []string{""} },
			want: `paths holds an empty or control-bearing path`,
		},
		{
			name: "control path", mutate: func(r *Row) { r.Paths = []string{"src/a.ts\n"} },
			want: `paths holds an empty or control-bearing path`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := storableRow(tc.mutate)
			err := Validate(&row)
			if err == nil || !strings.HasSuffix(err.Error(), tc.want) {
				t.Fatalf("Validate = %v; want a message ending in %q", err, tc.want)
			}
		})
	}
}

// TestReadRefusesUnstorablePathRow proves the read validates what it decodes,
// so a stored row the join cannot measure refuses as a corrupt store.
func TestReadRefusesUnstorablePathRow(t *testing.T) {
	for _, paths := range []string{`[]`, `["src/a\ts.ts"]`} {
		root := t.TempDir()
		writeStore(t, root, `{"schema_version":"1","bundle":"local-a","sha":"`+shaA+`",`+
			`"paths":`+paths+`,"note":null,"url":null,"detected_at":"2026-11-14T00:00:00Z"}`+"\n")
		if _, err := Read(root); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("Read(paths %s) = %v; want ErrCorrupt", paths, err)
		}
	}
}

// storableRow is one row every rule accepts, mutated by the caller.
func storableRow(mutate func(*Row)) Row {
	row := Row{
		SchemaVersion: "1", Bundle: "local-a", Sha: shaA,
		Paths: []string{"src/a.ts"}, DetectedAt: "2026-11-14T00:00:00Z",
	}
	mutate(&row)
	return row
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

// TestUpsertKeepsEveryParallelAttach proves the attach serializes: sixteen
// attaches started together each keep a row, which the unlocked
// read-modify-write dropped to one or two.
func TestUpsertKeepsEveryParallelAttach(t *testing.T) {
	root := t.TempDir()
	mkdirStore(t, root)
	const attaches = 16
	results, errs := attachInParallel(root, attaches)
	for i := range attaches {
		if errs[i] != nil {
			t.Fatalf("attach %d: %v", i, errs[i])
		}
		if !results[i] {
			t.Fatalf("attach %d reported an existing row; every pair was distinct", i)
		}
	}
	rows, err := Read(root)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(rows) != attaches {
		t.Fatalf("parallel attaches kept %d rows; want %d", len(rows), attaches)
	}
}

// attachInParallel starts every attach at once and returns what each reported.
func attachInParallel(root string, attaches int) ([]bool, []error) {
	results := make([]bool, attaches)
	errs := make([]error, attaches)
	var wg sync.WaitGroup
	for i := range attaches {
		wg.Go(func() {
			results[i], errs[i] = Upsert(root, &Row{
				SchemaVersion: "1",
				Bundle:        fmt.Sprintf("local-%02d", i),
				Sha:           fmt.Sprintf("%040x", i+1),
				Paths:         []string{fmt.Sprintf("src/%02d.go", i)},
				DetectedAt:    "2026-11-14T00:00:00Z",
			})
		})
	}
	wg.Wait()
	return results, errs
}

// TestUpsertBreaksAStaleLock proves a lock left by a killed attach does not
// block the ledger forever.
func TestUpsertBreaksAStaleLock(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, filepath.FromSlash(File)+lockSuffix)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("9999 2020-01-01T00:00:00Z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * lockStale)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	appended, err := Upsert(root, &Row{
		SchemaVersion: "1", Bundle: "local-a", Sha: shaA,
		Paths: []string{"src/a.ts"}, DetectedAt: "2026-11-14T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("Upsert over a stale lock: %v", err)
	}
	if !appended {
		t.Fatal("Upsert over a stale lock appended nothing")
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("the attach left its lock behind (stat err %v)", err)
	}
}

// TestLockRefusesALiveLock proves a waiter gives up typed on a lock another
// attach holds, and never steals the holder's file.
func TestLockRefusesALiveLock(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, filepath.FromSlash(File)+lockSuffix)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("9999 2026-11-14T00:00:00Z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unlock, err := lock(root, 20*time.Millisecond)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("lock = %v; want ErrBusy", err)
	}
	if unlock != nil {
		t.Fatal("a refused lock returned a release")
	}
	if got := readIncidentLock(t, file); got != "9999 2026-11-14T00:00:00Z\n" {
		t.Fatalf("the waiter rewrote the held lock: %q", got)
	}
}

func readIncidentLock(t *testing.T, file string) string {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
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

// mkdirStore lands the ledger's directory, which every attach finds: the
// bundle check proved the review store beside it exists.
func mkdirStore(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(File))), 0o755); err != nil {
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
