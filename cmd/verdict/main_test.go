package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCheckoutRootFindsCheckout checks the walk-up from a bin dir.
func TestCheckoutRootFindsCheckout(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts", "review.sh"), []byte("#!/usr/bin/env bash\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := checkoutRoot(filepath.Join(root, "bin")); got != root {
		t.Fatalf("checkoutRoot(bin) = %q, want %q", got, root)
	}
}

// TestCheckoutRootFallsBackToBinaryDir checks the no-checkout fallback.
func TestCheckoutRootFallsBackToBinaryDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if got := checkoutRoot(dir); got != dir {
		t.Fatalf("checkoutRoot(%q) = %q, want fallback %q", dir, got, dir)
	}
}
