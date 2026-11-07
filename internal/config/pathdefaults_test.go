package config

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// scopedFloors resolves a repository whose vendored scope carries stricter
// literal floors than the repository default, over a vendored and an ordinary
// path, so the consumer tests read a genuine capture.
func scopedFloors(t *testing.T) *RunState {
	t.Helper()
	display, post := "important", "important"
	criticalDisplay, criticalPost := "critical", "critical"
	repo := &core.Config{
		SchemaVersion: "1",
		Defaults:      &core.ConfigDefaults{DisplayMinSeverity: &display, PostMinSeverity: &post},
		Scopes: []core.ConfigScope{{
			Name:     "vendored",
			Include:  []string{"vendor/**"},
			Defaults: &core.ConfigDefaults{DisplayMinSeverity: &criticalDisplay, PostMinSeverity: &criticalPost},
		}},
	}
	resolved, ref := Resolve(&Inputs{
		Repo:  repo,
		Files: []FileInput{{Path: "vendor/lib.go"}, {Path: "app/main.go"}},
	})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	return &RunState{Resolved: resolved}
}

func finding(path, severity string) any {
	return jq.NewObj("path", path, "severity", severity)
}

// TestFloorsFollowTheMatchingReviewFile proves the effective floor is the one
// the finding's own resolved file carries, not the repository default: the
// vendored scope outranks the default for its paths only.
func TestFloorsFollowTheMatchingReviewFile(t *testing.T) {
	t.Parallel()
	rs := scopedFloors(t)
	tests := []struct {
		path string
		want string
	}{
		{"vendor/lib.go", "critical"},
		{"app/main.go", "important"},
		{"docs/unlisted.md", "important"},
		{"", "important"},
	}
	for _, tc := range tests {
		t.Run("path "+tc.path, func(t *testing.T) {
			t.Parallel()
			assertFloors(t, rs, tc.path, tc.want)
		})
	}
}

func assertFloors(t *testing.T, rs *RunState, path, want string) {
	t.Helper()
	if got := DisplayFloorFor(rs, path); got != want {
		t.Fatalf("display floor for %q = %q; want %q", path, got, want)
	}
	if got := PostFloorFor(rs, path); got != want {
		t.Fatalf("post floor for %q = %q; want %q", path, got, want)
	}
}

// TestScopedFloorWithholdsOnlyItsOwnPaths pins the filtering contract: an
// important finding on the vendored path is withheld by its critical scope
// floor while the same important finding on an ordinary path stays visible,
// and a critical one survives on both.
func TestScopedFloorWithholdsOnlyItsOwnPaths(t *testing.T) {
	t.Parallel()
	rs := scopedFloors(t)
	tests := []struct {
		entry  any
		name   string
		hidden bool
	}{
		{finding("vendor/lib.go", "important"), "important on vendored path", true},
		{finding("app/main.go", "important"), "important on ordinary path", false},
		{finding("vendor/lib.go", "critical"), "critical on vendored path", false},
		{finding("app/main.go", "critical"), "critical on ordinary path", false},
		{finding("vendor/lib.go", "spicy"), "unknown severity stays visible", false},
		{nil, "null entry stays visible", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertWithheld(t, rs, tc.entry, tc.hidden)
		})
	}
}

func assertWithheld(t *testing.T, rs *RunState, entry any, hidden bool) {
	t.Helper()
	if got := BelowDisplayFloor(rs, entry); got != hidden {
		t.Fatalf("display withheld = %v; want %v", got, hidden)
	}
	if got := BelowPostFloor(rs, entry); got != hidden {
		t.Fatalf("post withheld = %v; want %v", got, hidden)
	}
}

// TestRenameMatchesTheOldPathFloor proves a renamed file keeps the floor its
// contributing scope attached to the pre-rename path.
func TestRenameMatchesTheOldPathFloor(t *testing.T) {
	t.Parallel()
	critical := "critical"
	repo := &core.Config{
		SchemaVersion: "1",
		Scopes: []core.ConfigScope{{
			Name:     "vendored",
			Include:  []string{"vendor/**"},
			Defaults: &core.ConfigDefaults{DisplayMinSeverity: &critical},
		}},
	}
	resolved, ref := Resolve(&Inputs{
		Repo:  repo,
		Files: []FileInput{{Path: "lib/moved.go", OldPath: "vendor/lib.go"}},
	})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	rs := &RunState{Resolved: resolved}
	if got := DisplayFloorFor(rs, "lib/moved.go"); got != "critical" {
		t.Fatalf("floor for the renamed path = %q; want the old path's critical", got)
	}
	if got := DisplayFloorFor(rs, "vendor/lib.go"); got != "critical" {
		t.Fatalf("floor for the pre-rename path = %q; want critical", got)
	}
}

// TestOperatorFlagOutranksTheScope pins precedence at the filtering seam with
// literals: the vendored scope asks for critical, an explicit operator flag
// asks for important, and the capture the consumer reads answers important —
// so an important finding on the vendored path publishes instead of being
// withheld by the scope, while the un-flagged capture withholds it.
func TestOperatorFlagOutranksTheScope(t *testing.T) {
	t.Parallel()
	critical, flag := "critical", "important"
	repo := &core.Config{
		SchemaVersion: "1",
		Scopes: []core.ConfigScope{{
			Name:     "vendored",
			Include:  []string{"vendor/**"},
			Defaults: &core.ConfigDefaults{DisplayMinSeverity: &critical},
		}},
	}
	files := []FileInput{{Path: "vendor/lib.go"}}
	scoped, ref := Resolve(&Inputs{Repo: repo, Files: files})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	if got := DisplayFloorFor(&RunState{Resolved: scoped}, "vendor/lib.go"); got != "critical" {
		t.Fatalf("scope-only floor = %q; want critical", got)
	}
	if !BelowDisplayFloor(&RunState{Resolved: scoped}, finding("vendor/lib.go", "important")) {
		t.Fatal("important finding published under the scope-only critical floor")
	}

	flagged, ref := Resolve(&Inputs{Repo: repo, Files: files, Flags: Partial{DisplayMinSeverity: &flag}})
	if ref != nil {
		t.Fatalf("Resolve with flag: %v", ref)
	}
	rs := &RunState{Resolved: flagged}
	if got := DisplayFloorFor(rs, "vendor/lib.go"); got != "important" {
		t.Fatalf("flagged floor = %q; want the operator's important", got)
	}
	if BelowDisplayFloor(rs, finding("vendor/lib.go", "important")) {
		t.Fatal("the operator's important floor still withheld its own important finding")
	}
	if !BelowDisplayFloor(rs, finding("vendor/lib.go", "informational")) {
		t.Fatal("an informational finding survived the operator's important floor")
	}
}
