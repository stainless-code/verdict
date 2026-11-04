package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func handoffFixture(t *testing.T) Handoff {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "verdict")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(dir, "verdict.new")
	if err := os.WriteFile(newPath, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	receipt := filepath.Join(dir, ".verdict-installer")
	return Handoff{
		ParentPID:   4242,
		Target:      target,
		New:         newPath,
		ReceiptPath: receipt,
		ReceiptBody: "{\"binary\":\"verdict\",\"version\":\"1.2.3\",\"installed_by\":\"install.sh\"}\n",
		Version:     "1.2.3",
		ResultPath:  filepath.Join(dir, "result.json"),
		Wait:        func(context.Context, int) error { return nil },
		Smoke:       func(context.Context, string, string) error { return nil },
	}
}

// TestHandoffSuccess proves the Windows replacement sequence: old aside,
// new in, smoke, receipt, cleanup.
func TestHandoffSuccess(t *testing.T) {
	h := handoffFixture(t)
	if err := h.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	raw, err := os.ReadFile(h.Target)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "new" {
		t.Errorf("target = %q; want new", raw)
	}
	receipt, err := os.ReadFile(h.ReceiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(receipt) != h.ReceiptBody {
		t.Errorf("receipt = %q; want %q", receipt, h.ReceiptBody)
	}
	if _, err := os.Stat(h.Target + ".old"); !os.IsNotExist(err) {
		t.Errorf(".old survived: %v", err)
	}
	assertResult(t, h.ResultPath, true)
}

// TestHandoffSmokeFailureRestores proves a bad new binary restores the old
// one and parks the new image as .failed.
func TestHandoffSmokeFailureRestores(t *testing.T) {
	h := handoffFixture(t)
	h.Smoke = func(context.Context, string, string) error { return errors.New("bad version") }
	if err := h.Run(context.Background()); err == nil {
		t.Fatal("Run succeeded; want a smoke failure")
	}
	raw, err := os.ReadFile(h.Target)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "old" {
		t.Errorf("target = %q; want the restored old binary", raw)
	}
	failed, err := os.ReadFile(h.Target + ".failed")
	if err != nil {
		t.Fatalf("failed image missing: %v", err)
	}
	if string(failed) != "new" {
		t.Errorf(".failed = %q; want new", failed)
	}
	if _, err := os.Stat(h.ReceiptPath); !os.IsNotExist(err) {
		t.Errorf("receipt written on failure: %v", err)
	}
	assertResult(t, h.ResultPath, false)
}

// TestHandoffWaitFailureTouchesNothing proves a parent that never exits
// leaves every file unchanged.
func TestHandoffWaitFailureTouchesNothing(t *testing.T) {
	h := handoffFixture(t)
	h.Wait = func(ctx context.Context, _ int) error {
		deadline, cancel := context.WithTimeout(ctx, time.Millisecond)
		defer cancel()
		<-deadline.Done()
		return deadline.Err()
	}
	if err := h.Run(context.Background()); err == nil {
		t.Fatal("Run succeeded; want a wait failure")
	}
	raw, err := os.ReadFile(h.Target)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "old" {
		t.Errorf("target = %q; want old", raw)
	}
	if _, err := os.Stat(h.ReceiptPath); !os.IsNotExist(err) {
		t.Errorf("receipt written before the wait: %v", err)
	}
}

// TestHandoffArgvRoundTrip pins the helper args the parent builds.
func TestHandoffArgvRoundTrip(t *testing.T) {
	h := handoffFixture(t)
	parsed, err := parseHandoff(h.argv())
	if err != nil {
		t.Fatalf("parseHandoff: %v", err)
	}
	if parsed.ParentPID != h.ParentPID || parsed.Target != h.Target || parsed.New != h.New ||
		parsed.ReceiptPath != h.ReceiptPath || parsed.ReceiptBody != h.ReceiptBody ||
		parsed.Version != h.Version || parsed.ResultPath != h.ResultPath {
		t.Fatalf("round trip = %+v; want %+v", parsed, h)
	}
}

// TestParseHandoffRejectsMissingFlags proves the helper usage gate.
func TestParseHandoffRejectsMissingFlags(t *testing.T) {
	if _, err := parseHandoff([]string{"--parent", "1"}); err == nil {
		t.Fatal("parseHandoff accepted missing flags")
	}
	if _, err := parseHandoff([]string{"--bogus"}); err == nil {
		t.Fatal("parseHandoff accepted an unknown flag")
	}
}

func assertResult(t *testing.T, path string, wantOK bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Error string `json:"error"`
		OK    bool   `json:"ok"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.OK != wantOK {
		t.Errorf("result ok = %v; want %v (%s)", doc.OK, wantOK, doc.Error)
	}
	if !wantOK && !strings.Contains(doc.Error, "smoke") && !strings.Contains(doc.Error, "waiting") {
		t.Errorf("result error = %q", doc.Error)
	}
}
