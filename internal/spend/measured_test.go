package spend_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/spend"
)

// TestDisplayUSDRoundsFloatNoise pins the one precision every money line
// prints: four decimals, half away from zero, trailing zeros trimmed.
func TestDisplayUSDRoundsFloatNoise(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"1.30604299999999994": "1.306",
		"0.00005":             "0.0001",
		"0.00004":             "0",
		"9.5":                 "9.5",
		"12":                  "12",
		"2.99996":             "3",
		"not-a-number":        "not-a-number",
	}
	for in, want := range cases {
		if got := spend.DisplayUSD(in); got != want {
			t.Errorf("DisplayUSD(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestMeasuredSpendSumsAgentPricedRows proves only rows the agent CLI's own
// output priced count, once per invocation, summed exactly.
func TestMeasuredSpendSumsAgentPricedRows(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ledger := `[
  {"at":"2026-11-24T10:00:00Z","execution_id":"exec-A","scope":"invocation","invocation_id":"exec-A@1",
   "projected_usd":0.30604299999999994,"priced_by":"measured"},
  {"at":"2026-11-24T10:00:00Z","execution_id":"exec-A","scope":"invocation","invocation_id":"exec-A@1",
   "projected_usd":0.30604299999999994,"priced_by":"measured"},
  {"at":"2026-11-24T11:00:00Z","execution_id":"exec-B","scope":"invocation","invocation_id":"exec-B@1",
   "projected_usd":1,"priced_by":"tokens"},
  {"at":"2026-11-24T12:00:00Z","execution_id":"exec-C","scope":"invocation","invocation_id":"exec-C@1",
   "projected_usd":null},
  {"at":"2026-11-24T13:00:00Z","execution_id":"launch-1","scope":"spawn","projected_usd":4,"priced_by":"agent"},
  {"at":"2026-11-24T14:00:00Z","execution_id":"exec-D","scope":"invocation","invocation_id":"exec-D@1",
   "projected_usd":0,"priced_by":"not-started"},
  {"at":"2026-11-24T15:00:00Z","projected_usd":9.5,"consent":"--consent-over-budget"}
]`
	if err := os.WriteFile(filepath.Join(dir, "budget.json"), []byte(ledger), 0o600); err != nil {
		t.Fatal(err)
	}
	got := spend.MeasuredSpend(dir)
	if got.USD != "1.30604299999999994" || got.Rows != 2 {
		t.Fatalf("MeasuredSpend = %+v, want 1.30604299999999994 over 2 rows", got)
	}
	if empty := spend.MeasuredSpend(t.TempDir()); empty.Rows != 0 || empty.USD != "" {
		t.Fatalf("MeasuredSpend(no ledger) = %+v, want zero", empty)
	}
}

// TestMeasuredRowsSkipsAnUnreadableCopyWithoutSpendingItsKey proves an
// unreadable amount does not consume its invocation's dedupe key, so the
// readable copy of the same invocation still counts.
func TestMeasuredRowsSkipsAnUnreadableCopyWithoutSpendingItsKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ledger := `[
  {"at":"2026-11-24T10:00:00Z","execution_id":"exec-A","scope":"invocation","invocation_id":"exec-A@1",
   "projected_usd":"oops","priced_by":"measured"},
  {"at":"2026-11-24T10:00:00Z","execution_id":"exec-A","scope":"invocation","invocation_id":"exec-A@1",
   "projected_usd":0.5,"priced_by":"measured"}
]`
	if err := os.WriteFile(filepath.Join(dir, "budget.json"), []byte(ledger), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := spend.MeasuredSpend(dir); got.USD != "0.5" || got.Rows != 1 {
		t.Fatalf("MeasuredSpend = %+v, want 0.5 over 1 row", got)
	}
}

// TestInvocationRunsKeepsAReadableCopyAfterAnUnreadableOne proves an
// unreadable copy of an invocation neither spends its dedupe key nor leaves
// the run incomplete when a later copy of the same invocation settled, while
// an invocation no copy settled still leaves its run incomplete.
func TestInvocationRunsKeepsAReadableCopyAfterAnUnreadableOne(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ledger := `[
  {"at":"2026-11-24T10:00:00Z","execution_id":"exec-A","scope":"invocation","invocation_id":"exec-A@1",
   "projected_usd":"oops","priced_by":"measured"},
  {"at":"2026-11-24T10:00:00Z","execution_id":"exec-A","scope":"invocation","invocation_id":"exec-A@1",
   "projected_usd":0.5,"priced_by":"measured"},
  {"at":"2026-11-24T10:00:00Z","execution_id":"exec-B","scope":"invocation","invocation_id":"exec-B@1",
   "projected_usd":0.25,"priced_by":"measured"},
  {"at":"2026-11-24T11:00:00Z","execution_id":"exec-B","scope":"invocation","invocation_id":"exec-B@2",
   "projected_usd":null}
]`
	if err := os.WriteFile(filepath.Join(dir, "budget.json"), []byte(ledger), 0o600); err != nil {
		t.Fatal(err)
	}
	runs := spend.InvocationRuns(dir)
	if len(runs) != 2 {
		t.Fatalf("InvocationRuns = %+v, want exec-A and exec-B", runs)
	}
	if a := runs[0]; a.Execution != "exec-A" || !a.Complete || spend.FormatRat(a.USD) != "0.5" {
		t.Errorf("exec-A = %+v (%s), want complete at 0.5", a, spend.FormatRat(a.USD))
	}
	if b := runs[1]; b.Execution != "exec-B" || b.Complete || spend.FormatRat(b.USD) != "0.25" {
		t.Errorf("exec-B = %+v (%s), want incomplete at a 0.25 lower bound", b, spend.FormatRat(b.USD))
	}
}
