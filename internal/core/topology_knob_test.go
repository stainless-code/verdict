package core_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestStructuralTopology pins the two-knob predicate: the body carries the
// computed topology only when the feature knob and its topology half are both
// on, so `publish.overview: false` never still posts a diagram.
func TestStructuralTopology(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		eff  core.Effective
		want bool
	}{
		{
			name: "both on",
			eff:  core.Effective{PublishOverview: core.FlagOn, DiagramsStructural: core.FlagOn},
			want: true,
		},
		{
			name: "topology half off",
			eff:  core.Effective{PublishOverview: core.FlagOn, DiagramsStructural: core.FlagOff},
			want: false,
		},
		{
			name: "feature knob off",
			eff:  core.Effective{PublishOverview: core.FlagOff, DiagramsStructural: core.FlagOn},
			want: false,
		},
		{
			name: "both off",
			eff:  core.Effective{PublishOverview: core.FlagOff, DiagramsStructural: core.FlagOff},
			want: false,
		},
		{
			name: "unset knobs",
			eff:  core.Effective{},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.eff.StructuralTopology(); got != tc.want {
				t.Errorf("StructuralTopology(%+v) = %v, want %v", tc.eff, got, tc.want)
			}
		})
	}
}

// TestDefaultsPublishFixPrompt pins the shipped default: the copy-me block is
// on for production, and only a fixture or an operator turns it off.
func TestDefaultsPublishFixPrompt(t *testing.T) {
	t.Parallel()
	if got := core.Defaults().PublishFixPrompt; got != core.FlagOn {
		t.Fatalf("Defaults().PublishFixPrompt = %q; want %q", got, core.FlagOn)
	}
}
