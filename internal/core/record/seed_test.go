package recordcore_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
)

func seedControls(t *testing.T, bundleDir string) {
	t.Helper()
	base := recordcore.BundleBase(bundleDir, nil)
	if base == "" {
		return
	}
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
