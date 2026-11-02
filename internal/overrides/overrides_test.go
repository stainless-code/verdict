package overrides_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/overrides"
)

// refusalCode recovers the typed refusal code from an error.
func refusalCode(t *testing.T, err error) string {
	t.Helper()
	var ref *cli.RefusalError
	if !errors.As(err, &ref) {
		t.Fatalf("want typed refusal, got %v", err)
	}
	return ref.Code
}

// TestFooterOrderSubset checks the footer-order subset gate.
func TestFooterOrderSubset(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.MkdirAll(overrides.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	checkDefaultOrder(t)
	checkSubsetOrder(t)
	checkBogusOrder(t)
}

// checkDefaultOrder checks the absent-fragment default order.
func checkDefaultOrder(t *testing.T) {
	t.Helper()
	if order, ref := overrides.FooterOrder(); ref != nil || len(order) != 4 {
		t.Fatalf("default order = %v, %v; want 4 fields", order, ref)
	}
}

// checkSubsetOrder checks a two-field fragment.
func checkSubsetOrder(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(overrides.Dir, "footer-order.md"), []byte("link\ncost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	order, ref := overrides.FooterOrder()
	if ref != nil {
		t.Fatal(ref)
	}
	if len(order) != 2 || order[0] != "link" || order[1] != "cost" {
		t.Errorf("order = %v; want [link cost]", order)
	}
}

// checkBogusOrder checks an unknown field refuses.
func checkBogusOrder(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(overrides.Dir, "footer-order.md"), []byte("bogus\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := overrides.FooterOrder(); refusalCode(t, err) != "bad-override" {
		t.Errorf("bogus field refusal = %v; want bad-override", err)
	}
}

// TestThresholds checks the degenerate-guard threshold parsing.
func TestThresholds(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	files, size, ref := overrides.Thresholds()
	if ref != nil || files != 8 || size != 24576 {
		t.Fatalf("defaults = %d %d %v; want 8 24576", files, size, ref)
	}
	if err := os.MkdirAll(overrides.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(overrides.Dir, "walkthrough-threshold.md"), []byte("files: 3\nbytes = 100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, size, ref = overrides.Thresholds()
	if ref != nil || files != 3 || size != 100 {
		t.Errorf("parsed = %d %d %v; want 3 100", files, size, ref)
	}
}

// TestCheckLocked checks locked-invariant fragment rejection.
func TestCheckLocked(t *testing.T) {
	if err := overrides.CheckLocked("f", "> ✅ marker line"); refusalCode(t, err) != "bad-override" {
		t.Errorf("marker line refusal = %v; want bad-override", err)
	}
	if ref := overrides.CheckLocked("f", "plain prose"); ref != nil {
		t.Errorf("plain prose refusal = %v; want nil", ref)
	}
}
