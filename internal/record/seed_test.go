package record_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
)

// seedControls snapshots the sandbox's root controls into each fixture
// bundle through the head opt-out, standing in for fetch's base resolution.
// The golden walk omits the seeded artifacts: they are deterministic test
// setup, and the controls tests pin their content.
func seedControls(t *testing.T, dir string) {
	t.Helper()
	bundles, err := filepath.Glob(filepath.Join(dir, ".verdict", "state", "*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, bundle := range bundles {
		seedBundle(t, dir, bundle)
	}
}

// seedBundle gives one fixture bundle the controls snapshot and typed capture
// the fetch producer would have written, skipping anything that is not a
// bundle directory with a stated base.
func seedBundle(t *testing.T, dir, bundle string) {
	t.Helper()
	base := recordcore.BundleBase(bundle, nil)
	if fi, statErr := os.Stat(bundle); statErr != nil || !fi.IsDir() || base == "" {
		return
	}
	req := &controls.Request{BundleDir: bundle, BaseSHA: base, AllowHead: true, Root: dir}
	snap, ref := controls.Resolve(t.Context(), nil, req)
	if ref != nil {
		t.Fatalf("seed controls: %v", ref)
	}
	if serr := config.WriteRunState(bundle, captureRunState(t, base, snap)); serr != nil {
		t.Fatalf("seed run state: %v", serr)
	}
}

// captureRunState builds the fixture's typed capture with the shared resolver
// and capture producer instead of fabricated effective values. The fixtures
// state no policy config or environment at capture time, so the resolved
// defaults are the documented defaults and the capture is honestly untrusted:
// the head opt-out stands in for a fetch, it does not make a trusted base.
func captureRunState(t *testing.T, base string, snap *controls.Snapshot) *config.RunState {
	t.Helper()
	inputs := &config.Inputs{BaseSHA: base}
	resolved, ref := config.Resolve(inputs)
	if ref != nil {
		t.Fatalf("resolve fixture config: %v", ref)
	}
	const capturedAt = "2026-11-02T12:00:00Z"
	evidence := config.Capture(inputs, resolved, nil, capturedAt)
	return &config.RunState{
		BaseSHA:      base,
		Trusted:      false,
		CapturedAt:   capturedAt,
		ConfigSHA256: evidence.ConfigSHA256,
		Evidence:     evidence,
		Resolved:     resolved,
		Root:         config.RootSet(inputs.Repo, base),
		Inventory:    config.InventoryHashes(snap),
	}
}

// isControlsArtifact reports the seeded snapshot files the golden walker
// omits (bundle controls.json and bundle controls/**).
func isControlsArtifact(rel string) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	return len(parts) >= 4 && parts[0] == ".verdict" && parts[1] == "state" &&
		(parts[3] == controls.FileName || parts[3] == controls.DirName)
}
