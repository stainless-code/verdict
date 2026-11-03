package recordcore_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/controls"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
)

// seedControls snapshots the sandbox's .review controls into the bundle
// through the head opt-out, standing in for fetch's base resolution. A
// bundle without a recorded base is left unseeded and must refuse.
func seedControls(t *testing.T, bundleDir string) {
	t.Helper()
	base := recordcore.BundleBase(bundleDir)
	if base == "" {
		return
	}
	if _, ref := controls.Resolve(t.Context(), nil, controls.Request{BundleDir: bundleDir, BaseSHA: base, AllowHead: true}); ref != nil {
		t.Fatalf("seed controls: %v", ref)
	}
}
