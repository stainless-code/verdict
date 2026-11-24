//go:build unix

package spend_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/spend"
	"github.com/stainless-code/verdict/internal/testenv"
)

// TestMTDFIFOCostReturnsPromptly proves a FIFO named cost.json inside a
// bundle cannot hang the month-to-date scan: the non-blocking open is
// refused as non-regular and the ledger fallback still counts.
func TestMTDFIFOCostReturnsPromptly(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".verdict", "state", "42")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "cost.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "budget.json"),
		[]byte(`[{"at":"2026-05-02T12:00:00Z","projected_usd":5,"execution_id":"exec-1"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan spend.MTD, 1)
	go func() {
		done <- spend.AggregateMTD(filepath.Join(root, ".verdict", "state"),
			time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC))
	}()
	var got spend.MTD
	testenv.Until(t, func() bool {
		select {
		case got = <-done:
			return true
		default:
			return false
		}
	})
	if got.KnownUSD != "5" || got.KnownCount != 1 {
		t.Fatalf("FIFO scan = %+v, want ledger known 5 (FIFO cost skipped, not hung)", got)
	}
}
