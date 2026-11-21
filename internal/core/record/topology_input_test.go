package recordcore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/reviewdoc"
	"github.com/stainless-code/verdict/internal/jq"
)

// renderTopologyBundle writes a diff bundle with n file sections and returns
// its directory. One file section per file crosses the 8-file gate at 9.
func renderTopologyBundle(t *testing.T, files int) string {
	t.Helper()
	dir := t.TempDir()
	var diff strings.Builder
	for i := 1; i <= files; i++ {
		fmt.Fprintf(&diff, "diff --git a/src/f%d.ts b/src/f%d.ts\n"+
			"--- a/src/f%d.ts\n+++ b/src/f%d.ts\n@@ -1,2 +1,3 @@\n top\n+edited f%d.ts\n",
			i, i, i, i, i)
	}
	if err := os.WriteFile(filepath.Join(dir, "full.diff"), []byte(diff.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// topologyState is one merge state over a bundle of files changed files.
func topologyState(t *testing.T, files int, overview, structural string) *state {
	t.Helper()
	return &state{
		dir:       renderTopologyBundle(t, files),
		effective: core.Effective{PublishOverview: overview, DiagramsStructural: structural},
		controls:  &controls.Snapshot{Dir: t.TempDir()},
		now:       "2026-11-20T09:00:00Z",
		repo:      "acme/app",
	}
}

// TestBuildRenderInputTopology proves the body carries the computed topology
// only when both knobs are on and the bundle crosses the size gate: the body
// honors the gate strictly, so --overview cannot force a block into a
// below-threshold PR.
func TestBuildRenderInputTopology(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                 string
		overview, structural string
		files                int
		wanted               bool
	}{
		{name: "both knobs on", overview: core.FlagOn, structural: core.FlagOn, files: 9, wanted: true},
		{name: "structural off", overview: core.FlagOn, structural: core.FlagOff, files: 9},
		{name: "overview off", overview: core.FlagOff, structural: core.FlagOn, files: 9},
		{name: "below the size gate", overview: core.FlagOn, structural: core.FlagOn, files: 8},
	}
	for i := range tests {
		t.Run(tests[i].name, func(t *testing.T) {
			t.Parallel()
			st := topologyState(t, tests[i].files, tests[i].overview, tests[i].structural)
			assertTopologyBlock(t, st.buildRenderInput(jq.NewObj(), nil, nil, nil, nil), tests[i].wanted)
		})
	}
}

func assertTopologyBlock(t *testing.T, in *reviewdoc.RenderInput, want bool) {
	t.Helper()
	if got := strings.Contains(in.Topology, "Change topology"); got != want {
		t.Errorf("topology block present = %v, want %v", got, want)
	}
	if !want && in.Topology != "" {
		t.Errorf("Topology = %q, want empty", in.Topology)
	}
}
