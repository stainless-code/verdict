package recordcore_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/controls"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
)

func seedControls(t *testing.T, bundleDir string) {
	t.Helper()
	base := recordcore.BundleBase(bundleDir, nil)
	if base == "" {
		return
	}
	if _, ref := controls.Resolve(t.Context(), nil, controls.Request{BundleDir: bundleDir, BaseSHA: base, AllowHead: true}); ref != nil {
		t.Fatalf("seed controls: %v", ref)
	}
}
