package spend_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/spend"
)

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
	got := spend.AggregateMTD(root, now)
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
	got := spend.AggregateMTD(root, now)
	if got.KnownUSD != "1" {
		t.Fatalf("KnownUSD = %q, want 1 (prior month excluded)", got.KnownUSD)
	}
}

func TestMTDUnknownStaysUnknown(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	writeReceipt(t, root, `[{"at":"2026-05-02T12:00:00Z","projected_usd":null},{"at":"2026-05-03T12:00:00Z","projected_usd":0.5}]`)
	got := spend.AggregateMTD(root, now)
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
	got := spend.AggregateMTD(root, now)
	if got.KnownUSD != "0.3" {
		t.Fatalf("KnownUSD = %q, want exact 0.3", got.KnownUSD)
	}
}

func TestMTDNoReceipts(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	got := spend.AggregateMTD(root, now)
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
	got := spend.AggregateMTD(root, now)
	if got.KnownUSD != "2.5" || got.UnknownCount != 1 {
		t.Fatalf("cost MTD = %+v, want 2.5/1 unknown", got)
	}
}

func TestMTDMonthBoundaryUTC(t *testing.T) {
	// Just after UTC midnight at month boundary belongs to the new month.
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	root := t.TempDir()
	writeReceipt(t, root, `[{"at":"2026-05-31T23:59:59Z","projected_usd":10},{"at":"2026-06-01T00:00:00Z","projected_usd":1}]`)
	got := spend.AggregateMTD(root, now)
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
