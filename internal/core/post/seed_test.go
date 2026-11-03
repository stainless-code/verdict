package postcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/controls"
)

// seedPostControls snapshots the sandbox's .review controls into bundleDir
// through the head opt-out, standing in for fetch's base resolution.
func seedPostControls(t *testing.T, bundleDir, base string) {
	t.Helper()
	if _, ref := controls.Resolve(t.Context(), nil, controls.Request{BundleDir: bundleDir, BaseSHA: base, AllowHead: true}); ref != nil {
		t.Fatalf("seed controls: %v", ref)
	}
}
