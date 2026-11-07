package postcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
)

// seedPostControls snapshots the sandbox's controls into the bundle and
// writes the run bundle that binds them: a snapshot without its run bundle
// is a deleted bundle, not a legacy one, and post refuses that pairing.
func seedPostControls(t *testing.T, bundleDir, base string) {
	t.Helper()
	snap, ref := controls.Resolve(t.Context(), nil, &controls.Request{BundleDir: bundleDir, BaseSHA: base, AllowHead: true})
	if ref != nil {
		t.Fatalf("seed controls: %v", ref)
	}
	rs := &config.RunState{
		BaseSHA:    base,
		Trusted:    true,
		CapturedAt: "2026-11-02T12:00:00Z",
		Evidence:   config.Evidence{SchemaVersion: "1", BaseSHA: base, Plan: config.Plan{Status: config.PendingStatus}},
		Resolved:   &config.Resolved{Plan: config.Plan{Status: config.PendingStatus}},
		Inventory:  config.InventoryHashes(snap),
	}
	if serr := config.WriteRunState(bundleDir, rs); serr != nil {
		t.Fatalf("seed run state: %v", serr)
	}
}
