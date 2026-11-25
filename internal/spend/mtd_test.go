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

func TestMTDMissingStateDirIsSilent(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "state")
	got := spend.AggregateMTD(state, time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC))
	if got.Warning != "" || got.KnownUSD != "" || got.KnownCount != 0 || got.UnknownCount != 0 {
		t.Fatalf("missing state dir = %+v, want silent empty result (no warning)", got)
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

func writeBundleFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, ".verdict", "state", "42")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// Prior-month recorded cost must not suppress current-month ledger rows.
func TestMTDPriorMonthCostDoesNotSuppressCurrentLedger(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	writeBundleFiles(t, root, map[string]string{
		"cost.json":   `[{"at":"2026-04-15T12:00:00Z","est_usd":99}]`,
		"budget.json": `[{"at":"2026-05-02T12:00:00Z","projected_usd":5}]`,
	})
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.KnownUSD != "5" || got.KnownCount != 1 {
		t.Fatalf("prior-month suppression = %+v, want known 5 count 1 from current-month ledger", got)
	}
}

// Same-month recorded cost with exec-1 suppresses only the matching ledger
// row; a distinct exec-2 row is counted once.
func TestMTDSameMonthExecLevelSuppression(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	writeBundleFiles(t, root, map[string]string{
		"cost.json":   `[{"at":"2026-05-02T12:00:00Z","est_usd":5,"execution_id":"exec-1"}]`,
		"budget.json": `[{"at":"2026-05-02T12:00:00Z","projected_usd":5,"execution_id":"exec-1"},{"at":"2026-05-03T12:00:00Z","projected_usd":3,"execution_id":"exec-2"}]`,
	})
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.KnownUSD != "8" || got.KnownCount != 2 {
		t.Fatalf("exec-level MTD = %+v, want known 8 count 2 (exec-1 once from cost, exec-2 once from ledger)", got)
	}
}

// Legacy recorded cost without execution_id uses the timestamp gate: an
// earlier ledger row is the pre-run projection (not double-counted), a
// later consented row is counted.
func TestMTDLegacyCostUsesTimestampGate(t *testing.T) {
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	writeBundleFiles(t, root, map[string]string{
		"cost.json":   `[{"at":"2026-05-02T12:00:00Z","est_usd":5}]`,
		"budget.json": `[{"at":"2026-05-02T11:55:00Z","projected_usd":5}]`,
	})
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.KnownUSD != "5" || got.KnownCount != 1 {
		t.Fatalf("legacy earlier-gate = %+v, want known 5 (no double count)", got)
	}
	root = t.TempDir()
	writeBundleFiles(t, root, map[string]string{
		"cost.json":   `[{"at":"2026-05-02T12:00:00Z","est_usd":5}]`,
		"budget.json": `[{"at":"2026-05-02T11:55:00Z","projected_usd":5},{"at":"2026-05-02T14:00:00Z","projected_usd":3}]`,
	})
	got = spend.AggregateMTD(stateDirOf(root), now)
	if got.KnownUSD != "8" || got.KnownCount != 2 {
		t.Fatalf("legacy later-gate = %+v, want known 8 (earlier suppressed, later counted)", got)
	}
}

// TestMTDIdentifiedCostDoesNotCoverUnidentifiedRow proves a later identified
// recorded cost never moves the gate that an unidentified receipt sets: $5
// unidentified at 12:00, a distinct consented $3 row at 14:00, and another
// execution's identified $2 at 16:00 is $10 — the 16:00 receipt belongs to
// exec-3, so it must not suppress the 14:00 row, while the 12:00 row stays
// covered and is not double-counted.
func TestMTDIdentifiedCostDoesNotCoverUnidentifiedRow(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	writeBundleFiles(t, root, map[string]string{
		"cost.json": `[{"at":"2026-05-02T12:00:00Z","est_usd":5},` +
			`{"at":"2026-05-02T16:00:00Z","est_usd":2,"execution_id":"exec-3"}]`,
		"budget.json": `[{"at":"2026-05-02T12:00:00Z","projected_usd":5},` +
			`{"at":"2026-05-02T14:00:00Z","projected_usd":3}]`,
	})
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.KnownUSD != "10" || got.KnownCount != 3 {
		t.Fatalf("mixed MTD = %+v, want known 10 count 3 (5 recorded + 3 consented + 2 recorded)", got)
	}
}

// TestMTDResumedProjectionAfterRecordedCost proves a ledger row strictly
// later than its own execution's recorded cost is counted: a resumed run
// authorizes the remaining work after recording the earlier part, so a scan
// that suppressed it would hide pending spend from every concurrent bundle.
// The earlier row, at or before the recorded timestamp, stays its pre-run
// projection and is not double-counted.
func TestMTDResumedProjectionAfterRecordedCost(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	writeBundleFiles(t, root, map[string]string{
		"cost.json": `[{"at":"2026-05-02T12:00:00Z","est_usd":2,"execution_id":"exec-1"}]`,
		"budget.json": `[{"at":"2026-05-02T11:55:00Z","projected_usd":2,"execution_id":"exec-1"},` +
			`{"at":"2026-05-02T12:05:00Z","projected_usd":2,"execution_id":"exec-1"}]`,
	})
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.KnownUSD != "4" || got.KnownCount != 2 {
		t.Fatalf("resumed MTD = %+v, want known 4 count 2 (recorded 2 + resumed projection 2)", got)
	}
}

// TestMTDUnidentifiedCostCoversIdentifiedRow proves the unidentified gate
// applies to identified ledger rows: cost.json is agent-written, so a legacy
// record can omit the execution_id the budget.json row it projects carries.
// The row at 11:55 under exec-1 is that projection of the 12:00 $5 record and
// must not count again.
func TestMTDUnidentifiedCostCoversIdentifiedRow(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	writeBundleFiles(t, root, map[string]string{
		"cost.json":   `[{"at":"2026-05-02T12:00:00Z","est_usd":5}]`,
		"budget.json": `[{"at":"2026-05-02T11:55:00Z","projected_usd":5,"execution_id":"exec-1"}]`,
	})
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.KnownUSD != "5" || got.KnownCount != 1 {
		t.Fatalf("identified-row gate = %+v, want known 5 count 1 (one authorization)", got)
	}
}

// TestMTDRecordedCostAbsorbsEarlierProjections proves the resumed projection
// is not a permanent second charge: once exec-1 measures the resumed work
// again at 14:00, that receipt covers every earlier row of the execution, so
// the bundle counts its two measured $2 records and neither projection.
func TestMTDRecordedCostAbsorbsEarlierProjections(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	writeBundleFiles(t, root, map[string]string{
		"cost.json": `[{"at":"2026-05-02T12:00:00Z","est_usd":2,"execution_id":"exec-1"},` +
			`{"at":"2026-05-02T14:00:00Z","est_usd":2,"execution_id":"exec-1"}]`,
		"budget.json": `[{"at":"2026-05-02T11:55:00Z","projected_usd":2,"execution_id":"exec-1"},` +
			`{"at":"2026-05-02T12:05:00Z","projected_usd":2,"execution_id":"exec-1"}]`,
	})
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.KnownUSD != "4" || got.KnownCount != 2 {
		t.Fatalf("measured MTD = %+v, want known 4 count 2 (two measured $2 records, no projection)", got)
	}
}

// TestMTDPartialProjectionCountsKnownSubtotal proves a partially priced
// receipt — projected_usd null with the priced phases' lower bound in
// projection.known_subtotal_usd — keeps that known spend. Dropping it let the
// monthly ceiling read a stale lower month-to-date: $1 fully priced plus a $2
// known subtotal is $3, not $1, and the partial row is still unknown.
func TestMTDPartialProjectionCountsKnownSubtotal(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	writeReceipt(t, root, `[{"at":"2026-05-02T12:00:00Z","projected_usd":null,`+
		`"projection":{"total_usd":null,"known_subtotal_usd":2,"unknown_phases":["finder-2-discovery-1"]}},`+
		`{"at":"2026-05-03T12:00:00Z","projected_usd":1}]`)
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.KnownUSD != "3" || got.KnownCount != 1 || got.UnknownCount != 1 {
		t.Fatalf("partial-projection MTD = %+v, want known 3, 1 priced, 1 unknown (2 + 1)", got)
	}
}

// TestMTDPartialProjectionAloneReportsSubtotal proves one partially priced
// receipt with no fully priced sibling still reports its known lower bound:
// the amount is committed spend even though no row is fully priced.
func TestMTDPartialProjectionAloneReportsSubtotal(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	writeReceipt(t, root, `[{"at":"2026-05-02T12:00:00Z","projected_usd":null,`+
		`"projection":{"known_subtotal_usd":2,"unknown_phases":["finder-2-discovery-1"]}}]`)
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.KnownUSD != "2" || got.KnownCount != 0 || got.UnknownCount != 1 {
		t.Fatalf("partial-only MTD = %+v, want known 2, 0 priced, 1 unknown", got)
	}
}

// TestMTDNullProjectionWithoutSubtotalStaysUnknown proves a row with neither
// projected_usd nor a projection subtotal contributes nothing and stays
// unknown: no amount is invented for an unpriced authorization.
func TestMTDNullProjectionWithoutSubtotalStaysUnknown(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	writeReceipt(t, root, `[{"at":"2026-05-02T12:00:00Z","projected_usd":null,`+
		`"projection":{"total_usd":null,"known_subtotal_usd":null,"unknown_phases":["finder-1-discovery-1"]}},`+
		`{"at":"2026-05-03T12:00:00Z","projected_usd":"7"}]`)
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.KnownUSD != "" || got.KnownCount != 0 || got.UnknownCount != 2 {
		t.Fatalf("unpriced MTD = %+v, want no known amount and 2 unknown rows", got)
	}
}

// TestBundleTotalReadsOneBundleAcrossMonths proves the per-bundle sum: the
// bundle's own records count whatever their month (a round that crosses a
// UTC month boundary is not cut in two), a neighbor bundle's do not, and an
// unpriced record stays unknown rather than free.
func TestBundleTotalReadsOneBundleAcrossMonths(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 6, 1, 0, 0, 5, 0, time.UTC)
	mine := writeCost(t, root, "local-loop-1",
		`[{"at":"2026-05-31T23:59:59Z","est_usd":0.25},{"at":"2026-06-01T00:00:02Z","est_usd":0.5},{"at":"2026-06-01T00:00:03Z"}]`)
	writeCost(t, root, "local-loop-2", `{"at":"2026-06-01T00:00:01Z","est_usd":9}`)
	got := spend.BundleTotal(mine)
	if got.KnownUSD != "0.75" || got.KnownCount != 2 || got.UnknownCount != 1 {
		t.Fatalf("BundleTotal = %+v, want 0.75 known across the boundary and one unknown", got)
	}
	if all := spend.AggregateMTD(stateDirOf(root), now); all.KnownUSD != "9.5" {
		t.Fatalf("AggregateMTD = %+v, want June only (9.5)", all)
	}
}

// TestSpawnRowsAreNotCoveredByUnidentifiedCost proves the single-source rule
// treats a loop spawn row as its own receipt: an unidentified recorded cost
// in the same second still covers a projection row, but not a spawn row.
func TestSpawnRowsAreNotCoveredByUnidentifiedCost(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	dir := writeCost(t, root, "local-loop-1", `{"at":"2026-05-15T12:00:00Z","est_usd":0.5}`)
	ledger := `[{"at":"2026-05-15T12:00:00Z","execution_id":"proj","projected_usd":null},` +
		`{"at":"2026-05-15T12:00:00Z","execution_id":"loop-x-1-fix","scope":"spawn","projected_usd":null}]`
	if err := os.WriteFile(filepath.Join(dir, "budget.json"), []byte(ledger), 0o600); err != nil {
		t.Fatal(err)
	}
	got := spend.AggregateMTD(stateDirOf(root), now)
	if got.KnownUSD != "0.5" || got.UnknownCount != 1 {
		t.Fatalf("AggregateMTD = %+v, want 0.5 known and only the spawn row unknown", got)
	}
}

// writeCost plants one bundle's cost.json and returns the bundle directory.
func writeCost(t *testing.T, root, bundle, body string) string {
	t.Helper()
	dir := filepath.Join(stateDirOf(root), bundle)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cost.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}
