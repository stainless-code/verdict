package democore

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/agentcatalog"
)

// fakeLookPath answers one installed-binary set.
func fakeLookPath(installed ...string) func(string) (string, error) {
	set := make(map[string]bool, len(installed))
	for _, name := range installed {
		set[name] = true
	}
	return func(bin string) (string, error) {
		if set[bin] {
			return "/usr/local/bin/" + bin, nil
		}
		return "", fmt.Errorf("%s: not found", bin)
	}
}

// TestDetectAgentPreferenceOrder proves the documented preference list decides
// which installed agent wins, independent of the catalog's own table order.
func TestDetectAgentPreferenceOrder(t *testing.T) {
	t.Parallel()
	row, ok := detectAgent(fakeLookPath("codex", "opencode"))
	if !ok {
		t.Fatal("detectAgent found nothing with two installed agents")
	}
	if row.Name != "codex" {
		t.Errorf("detected %q, want codex (preference order)", row.Name)
	}
	row, ok = detectAgent(fakeLookPath("pi", "qwen", "goose"))
	if !ok || row.Name != "pi" {
		t.Errorf("detected %q (ok=%v), want pi", row.Name, ok)
	}
}

// TestDetectAgentSkipsUnattributableRows proves the two skip rules on the rows
// that carry them: a row that soft-denies writes (a launch warning) and a row
// whose binary name another tool shares (a probe) are never picked
// automatically, while ordinary rows still are.
func TestDetectAgentSkipsUnattributableRows(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{
		// agy carries the headless soft-denial warning; bob and forge probe
		// their binary's identity because another tool shares the name.
		"agy": false, "bob": false, "forge": false,
		"claude": true, "pi": true, "opencode": true,
	} {
		assertAutoRunnable(t, name, want)
	}
}

// assertAutoRunnable proves one catalog row's detection verdict.
func assertAutoRunnable(t *testing.T, name string, want bool) {
	t.Helper()
	row, ok := agentcatalog.Lookup(name)
	if !ok {
		t.Fatalf("catalog row %q is missing", name)
	}
	if got := autoRunnable(row); got != want {
		t.Errorf("autoRunnable(%s) = %v, want %v", name, got, want)
	}
}

// TestCatalogSplitsIntoRunnableAndSkipped proves the filter never swallows the
// whole table: detection needs at least one row it may pick, and the skip rules
// have to exclude at least one.
func TestCatalogSplitsIntoRunnableAndSkipped(t *testing.T) {
	t.Parallel()
	var skipped, runnable []string
	for _, row := range agentcatalog.All() {
		if row.Launch == nil {
			continue
		}
		if autoRunnable(row) {
			runnable = append(runnable, row.Name)
			continue
		}
		skipped = append(skipped, row.Name)
	}
	if len(skipped) == 0 || len(runnable) == 0 {
		t.Fatalf("catalog splits into %d skipped and %d runnable rows, want both non-empty",
			len(skipped), len(runnable))
	}
}

// TestDetectAgentSkipsAnUnrunnableOnlySet proves detection never falls back to
// a row it excluded: with only agy (the warning row) installed, nothing is
// detected.
func TestDetectAgentSkipsAnUnrunnableOnlySet(t *testing.T) {
	t.Parallel()
	if _, ok := detectAgent(fakeLookPath("agy")); ok {
		t.Fatal("detectAgent picked the launch-warning row")
	}
}

// TestPreferenceNamesResolveInCatalog proves the preference list names real
// runnable rows: a catalog rename fails here instead of silently shrinking the
// detection order.
func TestPreferenceNamesResolveInCatalog(t *testing.T) {
	t.Parallel()
	if len(agentPreference) == 0 {
		t.Fatal("the preference list is empty")
	}
	for _, name := range agentPreference {
		assertLaunchRow(t, name)
	}
	for _, name := range guideAgents {
		assertLaunchRow(t, name)
	}
}

// assertLaunchRow proves one name is a launch row in the catalog.
func assertLaunchRow(t *testing.T, name string) {
	t.Helper()
	row, ok := agentcatalog.Lookup(name)
	if !ok || row.Launch == nil {
		t.Errorf("%q is not a launch row in the catalog", name)
	}
}

// TestNoAgentRefusalIsTwoLines proves the setup guide: what happened, then the
// shortlist with the catalog's own install command for the first row.
func TestNoAgentRefusalIsTwoLines(t *testing.T) {
	t.Parallel()
	ref := noAgentRefusal()
	if ref.Code != codeAgentMissing {
		t.Errorf("code = %q, want %q", ref.Code, codeAgentMissing)
	}
	lines := strings.Split(ref.Message, "\n")
	if len(lines) != 2 {
		t.Fatalf("message has %d lines, want 2: %q", len(lines), ref.Message)
	}
	if lines[0] != msgNoAgent {
		t.Errorf("line 1 = %q, want %q", lines[0], msgNoAgent)
	}
	want := "try: claude (`" + claudeInstall(t) + "`), codex, opencode, or pi, or pass --agent <name>"
	if lines[1] != want {
		t.Errorf("line 2 = %q, want %q", lines[1], want)
	}
}

// claudeInstall reads the install command the refusal must quote.
func claudeInstall(t *testing.T) string {
	t.Helper()
	row, ok := agentcatalog.Lookup("claude")
	if !ok || row.Launch == nil || row.Launch.Install == "" {
		t.Fatal("the claude catalog row carries no install command")
	}
	return row.Launch.Install
}
