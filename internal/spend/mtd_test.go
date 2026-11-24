package spend_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/spend"
)

func stateDirOf(root string) string {
	return filepath.Join(root, ".verdict", "state")
}

func writeReceipt(t *testing.T, root, body string) {
	t.Helper()
	dir := filepath.Join(root, ".verdict", "state", "42")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "budget.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMTDAggregatesCurrentMonth(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	writeReceipt(t, root, `[{"at":"2026-05-01T12:00:00Z","projected_usd":1.5},{"at":"2026-05-10T12:00:00Z","projected_usd":0.5}]`)
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.Month != "2026-05" {
		t.Fatalf("month = %q, want 2026-05", got.Month)
	}
	if got.KnownUSD != "2" {
		t.Fatalf("KnownUSD = %q, want 2", got.KnownUSD)
	}
	if got.KnownCount != 2 || got.UnknownCount != 0 {
		t.Fatalf("counts = %d/%d, want 2/0", got.KnownCount, got.UnknownCount)
	}
}

func TestMTDExcludesPriorMonth(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	writeReceipt(t, root, `[{"at":"2026-05-01T12:00:00Z","projected_usd":1},{"at":"2026-04-28T12:00:00Z","projected_usd":99}]`)
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.KnownUSD != "1" {
		t.Fatalf("KnownUSD = %q, want 1 (prior month excluded)", got.KnownUSD)
	}
}

func TestMTDUnknownStaysUnknown(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	writeReceipt(t, root, `[{"at":"2026-05-02T12:00:00Z","projected_usd":null},{"at":"2026-05-03T12:00:00Z","projected_usd":0.5}]`)
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.KnownUSD != "0.5" {
		t.Fatalf("KnownUSD = %q, want 0.5", got.KnownUSD)
	}
	if got.UnknownCount != 1 {
		t.Fatalf("UnknownCount = %d, want 1", got.UnknownCount)
	}
}

func TestMTDExactDecimal(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	writeReceipt(t, root, `[{"at":"2026-05-01T00:00:00Z","projected_usd":0.1},{"at":"2026-05-02T00:00:00Z","projected_usd":0.2}]`)
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.KnownUSD != "0.3" {
		t.Fatalf("KnownUSD = %q, want exact 0.3", got.KnownUSD)
	}
}

func TestMTDNoReceipts(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.KnownUSD != "" || got.KnownCount != 0 || got.UnknownCount != 0 {
		t.Fatalf("empty = %+v, want zero", got)
	}
}

func TestMTDCostJSON(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	dir := filepath.Join(root, ".verdict", "state", "42")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `[{"at":"2026-05-02T12:00:00Z","est_usd":2.5},{"at":"2026-05-03T12:00:00Z","est_usd":null}]`
	if err := os.WriteFile(filepath.Join(dir, "cost.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.KnownUSD != "2.5" || got.UnknownCount != 1 {
		t.Fatalf("cost MTD = %+v, want 2.5/1 unknown", got)
	}
}

// TestMTDSingleSourcePerBundle proves budget.json and the recorded cost
// files are one authoritative source per bundle, not three additive
// ones: a bundle carrying budget.json ($5), cost.json ($5), and a
// non-chunk decoy file (chunks/a.cost.json, which the report rollup's
// chunk selection does not read) must aggregate to $5, not $15.
func TestMTDSingleSourcePerBundle(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	dir := filepath.Join(root, ".verdict", "state", "42")
	if err := os.MkdirAll(filepath.Join(dir, "chunks"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("budget.json", `[{"at":"2026-05-02T12:00:00Z","projected_usd":5,"execution_id":"exec-1"}]`)
	writeFile("cost.json", `[{"at":"2026-05-02T12:00:00Z","est_usd":5}]`)
	writeFile(filepath.Join("chunks", "a.cost.json"), `[{"at":"2026-05-02T12:00:00Z","est_usd":5}]`)
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.KnownUSD != "5" || got.KnownCount != 1 {
		t.Fatalf("single-source MTD = %+v, want known 5 count 1 (not 15)", got)
	}
}

func TestMTDCostAndChunkMergeAsOneSource(t *testing.T) {
	// Valid chunk files mirror the report rollup: cost.json plus the
	// chunk-*.cost.json records form the recorded source, and the
	// ledger stays out of it.
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	dir := filepath.Join(root, ".verdict", "state", "42")
	if err := os.MkdirAll(filepath.Join(dir, "chunks"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("budget.json", `[{"at":"2026-05-02T12:00:00Z","projected_usd":50}]`)
	writeFile("cost.json", `[{"at":"2026-05-02T12:00:00Z","est_usd":1}]`)
	writeFile(filepath.Join("chunks", "chunk-a.cost.json"), `[{"at":"2026-05-02T12:00:00Z","est_usd":2}]`)
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.KnownUSD != "3" || got.KnownCount != 2 {
		t.Fatalf("recorded MTD = %+v, want known 3 count 2 (ledger excluded)", got)
	}
}

func TestMTDBudgetDedupeByExecutionID(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	writeReceipt(t, root, `[{"at":"2026-05-02T12:00:00Z","projected_usd":5,"execution_id":"exec-1"},{"at":"2026-05-02T12:00:01Z","projected_usd":5,"execution_id":"exec-1"}]`)
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.KnownUSD != "5" || got.KnownCount != 1 {
		t.Fatalf("deduped MTD = %+v, want known 5 count 1", got)
	}
}

func TestMTDBudgetDedupeByProjectionSHA(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	writeReceipt(t, root, `[{"at":"2026-05-02T12:00:00Z","projected_usd":5,"projection_sha256":"sha-a"},{"at":"2026-05-02T12:00:01Z","projected_usd":5,"projection_sha256":"sha-a"},{"at":"2026-05-02T12:00:02Z","projected_usd":2,"projection_sha256":"sha-b"}]`)
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.KnownUSD != "7" || got.KnownCount != 2 {
		t.Fatalf("deduped MTD = %+v, want known 7 count 2", got)
	}
}

func TestMTDOffsetTimestampsBucketByUTC(t *testing.T) {
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	// 2026-06-01T02:00:00+05:00 is 2026-05-31T21:00:00Z — in May.
	root := t.TempDir()
	writeReceipt(t, root, `[{"at":"2026-06-01T02:00:00+05:00","projected_usd":7}]`)
	if got := spend.AggregateMTD(stateDirOf(root), now); got.KnownUSD != "7" {
		t.Fatalf("offset-in-May MTD = %+v, want known 7", got)
	}
	// 2026-05-31T23:30:00-05:00 is 2026-06-01T04:30:00Z — not in May.
	root = t.TempDir()
	writeReceipt(t, root, `[{"at":"2026-05-31T23:30:00-05:00","projected_usd":7}]`)
	if got := spend.AggregateMTD(stateDirOf(root), now); got.KnownUSD != "" || got.KnownCount != 0 {
		t.Fatalf("offset-in-June MTD = %+v, want empty", got)
	}
}

func TestMTDRejectsStringEncodedNumbers(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	writeReceipt(t, root, `[{"at":"2026-05-02T12:00:00Z","projected_usd":"7"}]`)
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.KnownUSD != "" || got.UnknownCount != 1 {
		t.Fatalf("string-number MTD = %+v, want unknown 1 (strict JSON number)", got)
	}
}

func TestMTDRejectsNegativeNumbers(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	writeReceipt(t, root, `[{"at":"2026-05-02T12:00:00Z","projected_usd":-5}]`)
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.KnownUSD != "" || got.UnknownCount != 1 {
		t.Fatalf("negative MTD = %+v, want unknown 1 (negative is unknown)", got)
	}
}

func TestMTDBoundsHugeExponents(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	writeReceipt(t, root, `[{"at":"2026-05-02T12:00:00Z","projected_usd":1e100000},{"at":"2026-05-03T12:00:00Z","projected_usd":1}]`)
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.KnownUSD != "1" || got.UnknownCount != 1 {
		t.Fatalf("huge-exponent MTD = %+v, want known 1 with the 1e100000 number unknown", got)
	}
}

func TestMTDUnreadableStateDirWarns(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	if err := os.WriteFile(state, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := spend.AggregateMTD(state, time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC))
	if got.Warning == "" {
		t.Fatalf("unreadable state dir = %+v, want a warning (not silent zero)", got)
	}
}

func TestMTDMonthBoundaryUTC(t *testing.T) {
	// Just after UTC midnight at month boundary belongs to the new month.
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	root := t.TempDir()
	writeReceipt(t, root, `[{"at":"2026-05-31T23:59:59Z","projected_usd":10},{"at":"2026-06-01T00:00:00Z","projected_usd":1}]`)
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.Month != "2026-06" || got.KnownUSD != "1" {
		t.Fatalf("boundary = %+v, want month 2026-06 known 1", got)
	}
}

func TestMonthlyExceedsExact(t *testing.T) {
	// The groupbudget exact case: 0.300000000000000001 > 0.3.
	if !spend.Exceeds("0.300000000000000001", "0.3") {
		t.Fatal("exact decimal above 0.3 not detected")
	}
	if spend.Exceeds("0.3", "0.3") {
		t.Fatal("equal is not exceeds")
	}
	if spend.Exceeds("0.3", "0.300000000000000001") {
		t.Fatal("smaller exceeds larger")
	}
}
