package incident

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

	request := &Row{
		SchemaVersion: "1", Bundle: "local-a", Sha: shaA, Paths: []string{"src/a.ts"},
		Note: new("reverted in the same hour"), URL: new("https://example.test/inc/1"),
		DetectedAt: "2026-11-14T00:00:01Z",
	}
	kept, appended, err := Upsert(root, request)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if !appended {
		t.Fatal("first attach reported an existing row")
	}
	assertRow(t, "the appended row", kept, request)
	want := `{"schema_version":"1","bundle":"local-a","sha":"` + shaA + `","paths":["src/a.ts"],` +
		`"note":"reverted in the same hour","url":"https://example.test/inc/1",` +
		`"detected_at":"2026-11-14T00:00:01Z"}` + "\n" + seeded + "\n"
	if got := readStore(t, root); got != want {
		t.Fatalf("ledger = %q; want %q", got, want)
	}

	before := readStore(t, root)
	kept, appended, err = Upsert(root, &Row{
		SchemaVersion: "1", Bundle: "local-a", Sha: shaA, Paths: []string{"src/other.ts"},
		DetectedAt: "2027-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	if appended {
		t.Fatal("second attach of the same pair reported an append")
	}
	// The existing path answers with the stored row, never the request's:
	// every field below came from the first attach, not this one.
	assertRow(t, "the existing row", kept, request)
	if got := readStore(t, root); got != before {
		t.Fatalf("second attach moved the ledger bytes: %q", got)
	}
}

// assertRow pins kept to want field by field: the row a caller reports is the
// row the ledger holds.
func assertRow(t *testing.T, label string, kept, want *Row) {
	t.Helper()
	if kept == nil {
		t.Fatalf("%s: Upsert returned no row", label)
	}
	if !slices.Equal(kept.Paths, want.Paths) {
		t.Fatalf("%s paths = %q; want %q", label, kept.Paths, want.Paths)
	}
	if got, wantNote := deref(kept.Note), deref(want.Note); got != wantNote {
		t.Fatalf("%s note = %q; want %q", label, got, wantNote)
	}
	if got, wantURL := deref(kept.URL), deref(want.URL); got != wantURL {
		t.Fatalf("%s url = %q; want %q", label, got, wantURL)
	}
	if kept.SchemaVersion != want.SchemaVersion || kept.Bundle != want.Bundle ||
		kept.Sha != want.Sha || kept.DetectedAt != want.DetectedAt {
		t.Fatalf("%s identity = %+v; want %+v", label, *kept, *want)
	}
}

// deref renders one optional row field, with "" for absent.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
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

// TestUpsertRefusesAnAppendPastTheReadCap proves the write honors the reader's
// cap: an append that would leave a ledger no Read can open refuses typed and
// moves no byte, so the rows already attached stay readable.
func TestUpsertRefusesAnAppendPastTheReadCap(t *testing.T) {
	root := t.TempDir()
	empty := `{"schema_version":"1","bundle":"local-a","sha":"` + shaA + `","paths":["src/a.ts"],` +
		`"note":"","url":null,"detected_at":"2026-11-14T00:00:00Z"}` + "\n"
	note := strings.Repeat("n", int(jq.MaxKBBytes)-len(empty)-64)
	seeded := strings.Replace(empty, `"note":""`, `"note":"`+note+`"`, 1)
	writeStore(t, root, seeded)
	if int64(len(seeded)) > jq.MaxKBBytes {
		t.Fatalf("the seeded ledger is already past the cap: %d bytes", len(seeded))
	}

	_, appended, err := Upsert(root, &Row{
		SchemaVersion: "1", Bundle: "local-a", Sha: shaB,
		Paths: []string{"src/b.ts"}, DetectedAt: "2026-11-14T00:00:01Z",
	})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Upsert past the read cap = (%v, %v); want ErrTooLarge", appended, err)
	}
	if got := readStore(t, root); got != seeded {
		t.Fatal("a refused append moved the ledger")
	}
	if _, err := Read(root); err != nil {
		t.Fatalf("Read after the refused append = %v; want the seeded ledger", err)
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
			_, results[i], errs[i] = Upsert(root, &Row{
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

// TestHeldLockRefusesAWait proves the lock is exclusive and typed: while one
// attach holds it, a second waits its bounded window and refuses [ErrBusy]
// instead of reading rows the holder is about to replace.
func TestHeldLockRefusesAWait(t *testing.T) {
	root := t.TempDir()
	mkdirStore(t, root)
	unlock, err := lock(root, lockWait)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	defer unlock()

	release, err := lock(root, 20*time.Millisecond)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("second lock = %v; want ErrBusy", err)
	}
	if release != nil {
		t.Fatal("a refused lock returned a release")
	}
}

// TestUpsertRefusesAHeldLock proves a refused attach writes nothing: the
// ledger keeps its rows while another attach holds the lock, and the retry
// appends beside them once the holder releases.
func TestUpsertRefusesAHeldLock(t *testing.T) {
	root := t.TempDir()
	seeded := ledgerLine(shaA, "src/a.ts", "2026-11-14T00:00:00Z") + "\n"
	writeStore(t, root, seeded)
	unlock, err := lock(root, lockWait)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	row := &Row{
		SchemaVersion: "1", Bundle: "local-a", Sha: shaB,
		Paths: []string{"src/b.ts"}, DetectedAt: "2026-11-14T00:00:01Z",
	}
	if _, appended, err := Upsert(root, row); !errors.Is(err, ErrBusy) || appended {
		t.Fatalf("Upsert under a held lock = (%v, %v); want ErrBusy", appended, err)
	}
	if got := readStore(t, root); got != seeded {
		t.Fatalf("a refused attach moved the ledger: %q", got)
	}

	unlock()
	_, appended, err := Upsert(root, row)
	if err != nil || !appended {
		t.Fatalf("Upsert after the holder released = (%v, %v); want an append", appended, err)
	}
	want := seeded + ledgerLine(shaB, "src/b.ts", "2026-11-14T00:00:01Z") + "\n"
	if got := readStore(t, root); got != want {
		t.Fatalf("ledger = %q; want %q", got, want)
	}
}

// ledgerLine is one stored row in the ledger's published key order.
func ledgerLine(sha, path, at string) string {
	return `{"schema_version":"1","bundle":"local-a","sha":"` + sha +
		`","paths":["` + path + `"],"note":null,"url":null,"detected_at":"` + at + `"}`
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
