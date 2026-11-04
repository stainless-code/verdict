package reportcore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestRenderOverviewGate proves the overview gate keeps its two halves
// apart: a forced-on overview renders a bundle below the size thresholds,
// the plain toggle still omits it, and a forced-off hides a large bundle.
func TestRenderOverviewGate(t *testing.T) {
	t.Parallel()
	small := overviewFixture(t, "diff --git a/a.go b/a.go\n@@ -1 +1 @@\n-a\n+b\n")
	large := overviewFixture(t, strings.Repeat("diff --git a/a.go b/a.go\n@@ -1 +1 @@\n-a\n+b\n", 9))
	tests := []struct {
		forces *core.GlobalForces
		name   string
		dir    string
		want   bool
	}{
		{nil, "toggle on, below threshold", small, false},
		{&core.GlobalForces{PublishOverviewForce: strTrue}, "forced on, below threshold", small, true},
		{&core.GlobalForces{PublishOverviewForce: strFalse}, "forced off, above threshold", large, false},
		{nil, "toggle on, above threshold", large, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := &core.ReportRequest{
				Effective: core.Effective{PublishOverview: strTrue, DiagramsStructural: strTrue},
			}
			req.Forces = tt.forces
			st := &state{dir: tt.dir, req: req}
			got := renderOverview(st) != ""
			if got != tt.want {
				t.Errorf("renderOverview() rendered = %v; want %v", got, tt.want)
			}
		})
	}
}

// overviewFixture writes one bundle with a full.diff and no PR.json, so the
// paths, counts, and hunk columns all come from the diff fallback.
func overviewFixture(t *testing.T, diff string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "full.diff"), []byte(diff), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
