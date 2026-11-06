package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
)

// TestProducedCaptureBindsAndStillRefusesDeletion pins the producer contract
// the shipped record verb relies on: every bundle the runner writes carries a
// run.json that verifies against its controls snapshot, states plainly that
// the bench capture is untrusted, and still refuses controls-stale once that
// run.json is deleted.
func TestProducedCaptureBindsAndStillRefusesDeletion(t *testing.T) {
	cfg := shippedFixture(t)
	if _, err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, dir := range captureDirs(t, cfg.WorkDir) {
		assertBoundUntrusted(t, dir)
		assertDeletionRefused(t, dir)
	}
}

// assertBoundUntrusted requires a produced capture that verifies, binds its
// inputs, and never claims a trusted source the bench does not have.
func assertBoundUntrusted(t *testing.T, dir string) {
	t.Helper()
	rs, ref := config.VerifyRunState(dir)
	if ref != nil {
		t.Fatalf("VerifyRunState(%s) = %v, want the produced capture to verify", dir, ref)
	}
	if rs.Trusted {
		t.Errorf("%s capture is marked trusted; bench inputs have no trusted source", dir)
	}
	if rs.InputsSHA256 == "" {
		t.Errorf("%s capture carries no inputs binding", dir)
	}
}

// assertDeletionRefused requires controls-stale once the produced run.json is
// gone: the capture the gate verified must stay the thing that authenticates
// the bundle.
func assertDeletionRefused(t *testing.T, dir string) {
	t.Helper()
	if err := os.Remove(filepath.Join(dir, "run.json")); err != nil {
		t.Fatal(err)
	}
	if _, ref := config.VerifyRunState(dir); ref == nil || ref.Code != "controls-stale" {
		t.Fatalf("deleted run.json in %s = %v, want controls-stale", dir, ref)
	}
}

// captureDirs lists the produced bundles that carry a typed capture.
func captureDirs(t *testing.T, workDir string) []string {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(workDir, ".verdict", "state", "*"))
	if err != nil {
		t.Fatalf("glob state: %v", err)
	}
	var out []string
	for _, dir := range dirs {
		if _, err := os.Stat(filepath.Join(dir, "run.json")); err == nil {
			out = append(out, dir)
		}
	}
	if len(out) == 0 {
		t.Fatalf("the run produced no typed capture under %s", workDir)
	}
	return out
}
