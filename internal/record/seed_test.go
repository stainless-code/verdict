package record_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/controls"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
)

// seedControls snapshots the sandbox's root controls into each fixture
// bundle through the head opt-out, standing in for fetch's base resolution.
// The golden walk omits the seeded artifacts: they are deterministic test
// setup, and the controls tests pin their content.
func seedControls(t *testing.T, dir string) {
	t.Helper()
	bundles, err := filepath.Glob(filepath.Join(dir, ".review", "*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, bundle := range bundles {
		base := recordcore.BundleBase(bundle)
		if fi, statErr := os.Stat(bundle); statErr != nil || !fi.IsDir() || base == "" {
			continue
		}
		req := controls.Request{BundleDir: bundle, BaseSHA: base, AllowHead: true, Root: dir}
		if _, ref := controls.Resolve(t.Context(), nil, req); ref != nil {
			t.Fatalf("seed controls: %v", ref)
		}
	}
}

// isControlsArtifact reports the seeded snapshot files the golden walker
// omits (bundle controls.json and bundle controls/**).
func isControlsArtifact(rel string) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	return len(parts) >= 3 && parts[0] == ".review" &&
		(parts[2] == controls.FileName || parts[2] == controls.DirName)
}
