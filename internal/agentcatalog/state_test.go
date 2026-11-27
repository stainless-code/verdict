package agentcatalog

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

// TestStatePathsAreCitedAndShaped pins the rows whose review launches may
// write their own state in the OS review sandbox, each entry literal, and
// proves every declared path is cited and a clean path below HOME.
func TestStatePathsAreCitedAndShaped(t *testing.T) {
	want := map[string][]string{
		"claude":   {".claude", ".claude.json*", "Library/Keychains", "Library/Caches/claude-cli-nodejs"},
		"codex":    {".codex"},
		"opencode": {".local/share/opencode", ".config/opencode", ".local/state/opencode", ".cache/opencode"},
		"pi":       {".pi"},
	}
	got := map[string][]string{}
	for _, agent := range All() {
		if l := agent.Launch; l != nil && (len(l.StatePaths) > 0 || len(l.StateEnv) > 0) {
			got[agent.Name] = l.StatePaths
			checkStatePaths(t, agent.Name, l)
		}
	}
	if !maps.EqualFunc(got, want, slices.Equal) {
		t.Errorf("state paths = %q, want %q", got, want)
	}
}

// checkStatePaths fails a row whose state paths are uncited or misshapen.
func checkStatePaths(t *testing.T, name string, l *Launch) {
	t.Helper()
	if l.StateCitation == "" {
		t.Errorf("%s declares state paths without a citation", name)
	}
	for _, rel := range l.StatePaths {
		if why := StatePathShape(strings.TrimSuffix(rel, "*")); why != "" {
			t.Errorf("%s state path %q: %s", name, rel, why)
		}
	}
}

// TestConfinementIsCitedAndNarrow pins the rows that skip verdict's review
// sandbox for their own (codex) or still run without one because they
// confine their own writes (claude): every other row refuses there, so a new
// entry is a security decision, not a default.
func TestConfinementIsCitedAndNarrow(t *testing.T) {
	got := map[string]string{}
	for _, agent := range All() {
		if l := agent.Launch; l != nil && l.Confinement != "" {
			got[agent.Name] = l.Confinement
			checkConfinementCited(t, agent.Name, l)
		}
	}
	want := map[string]string{"claude": "own-writes", "codex": "own-sandbox"}
	if !maps.Equal(got, want) {
		t.Errorf("confinement = %v, want %v", got, want)
	}
	if codex, _ := Lookup("codex"); len(codex.Launch.SnapshotWriteArgs) == 0 {
		t.Error("codex runs its own sandbox without the args that start it")
	}
}

// checkConfinementCited fails a confinement claim without its citation.
func checkConfinementCited(t *testing.T, name string, l *Launch) {
	t.Helper()
	if l.ConfinementCitation == "" {
		t.Errorf("%s confines itself without a citation", name)
	}
}

// TestStatePathShape accepts a clean relative path below HOME and refuses
// anything that names HOME, leaves it, or is not slash-form relative.
func TestStatePathShape(t *testing.T) {
	for _, ok := range []string{".claude", ".local/share/opencode", "Library/Keychains", ".claude.json"} {
		if why := StatePathShape(ok); why != "" {
			t.Errorf("StatePathShape(%q) = %q, want none", ok, why)
		}
	}
	for _, bad := range []string{"", ".", "..", "../x", "a/../b", "a/", "./a", "/abs", `a\b`, "C:x", "a//b"} {
		if StatePathShape(bad) == "" {
			t.Errorf("StatePathShape(%q) passed, want a refusal", bad)
		}
	}
}
