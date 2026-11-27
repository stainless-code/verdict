package agentcatalog

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

// TestStatePathsAreCitedAndShaped pins, literally, the rows whose review
// launches may write their own data in the OS review sandbox and the config
// and code beside it they may not, and proves every entry cited, shaped, and
// clear of the other list.
func TestStatePathsAreCitedAndShaped(t *testing.T) {
	writable := map[string][]string{
		"claude": {
			".claude/projects", ".claude/sessions", ".claude/debug", ".claude/todos", ".claude/backups",
			".claude/cache", ".claude/statsig", ".claude/file-history", ".claude/.credentials.json*",
			"Library/Caches/claude-cli-nodejs", "Library/Keychains",
		},
		"codex": {
			".codex/sessions", ".codex/archived_sessions", ".codex/log", ".codex/sqlite", ".codex/cache",
			".codex/shell_snapshots", ".codex/tmp", ".codex/.tmp", ".codex/auth.json*",
			".codex/history.jsonl*", ".codex/session_index.jsonl*", ".codex/state_*", ".codex/logs_*",
			".codex/models_cache.json*",
		},
		"opencode": {
			".local/share/opencode/auth.json*", ".local/share/opencode/mcp-auth.json*",
			".local/share/opencode/opencode.db*", ".local/share/opencode/log",
			".local/share/opencode/storage", ".local/share/opencode/snapshot",
			".local/share/opencode/tool-output", ".local/state/opencode",
		},
		"pi": {".pi/agent/sessions", ".pi/agent/auth.json*"},
	}
	readOnly := map[string][]string{
		"claude": {
			".claude.json", ".claude/settings.json", ".claude/settings.local.json", ".claude/plugins",
			".claude/skills", ".claude/agents", ".claude/commands", ".claude/hooks", ".claude/output-styles",
			".claude/CLAUDE.md", ".claude/shell-snapshots", ".claude/session-env",
		},
		"codex": {
			".codex/config.toml", ".codex/AGENTS.md", ".codex/rules", ".codex/prompts", ".codex/skills",
			".codex/plugins", ".codex/packages",
		},
		"opencode": {
			".config/opencode", ".cache/opencode", ".local/share/opencode/bin",
			".local/share/opencode/node_modules", ".local/share/opencode/shell",
		},
		"pi": {
			".pi/agent/settings.json", ".pi/agent/models.json", ".pi/agent/extensions", ".pi/agent/skills",
			".pi/agent/prompts", ".pi/agent/themes", ".pi/agent/AGENTS.md", ".pi/agent/mcp.json",
			".pi/agent/bin", ".pi/agent/install", ".pi/agent/npm",
		},
	}
	gotW, gotR := map[string][]string{}, map[string][]string{}
	for _, agent := range All() {
		if l := agent.Launch; l != nil && (len(l.StateWritable) > 0 || len(l.StateReadOnly) > 0) {
			gotW[agent.Name], gotR[agent.Name] = l.StateWritable, l.StateReadOnly
			checkStatePaths(t, agent.Name, l)
		}
	}
	if !maps.EqualFunc(gotW, writable, slices.Equal) || !maps.EqualFunc(gotR, readOnly, slices.Equal) {
		t.Errorf("writable = %q, read-only = %q; want %q and %q", gotW, gotR, writable, readOnly)
	}
}

// checkStatePaths fails a row whose state paths are uncited, misshapen, or
// let a writable entry reach a read-only one.
func checkStatePaths(t *testing.T, name string, l *Launch) {
	t.Helper()
	if l.StateCitation == "" {
		t.Errorf("%s declares state paths without a citation", name)
	}
	for _, rel := range slices.Concat(l.StateWritable, l.StateReadOnly) {
		if why := StatePathShape(strings.TrimSuffix(rel, "*")); why != "" {
			t.Errorf("%s state path %q: %s", name, rel, why)
		}
	}
	for _, w := range l.StateWritable {
		if r, ok := overlapping(w, l.StateReadOnly); ok {
			t.Errorf("%s writable %q reaches its read-only %q", name, w, r)
		}
	}
}

// overlapping returns the first read-only entry the writable one holds, sits
// inside, or (as a prefix) names.
func overlapping(w string, readOnly []string) (string, bool) {
	stem := strings.TrimSuffix(w, "*")
	for _, r := range readOnly {
		switch {
		case stem != w && strings.HasPrefix(r, stem):
			return r, true
		case r == stem, strings.HasPrefix(r, stem+"/"), strings.HasPrefix(stem, r+"/"):
			return r, true
		}
	}
	return "", false
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
