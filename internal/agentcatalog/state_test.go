package agentcatalog

import (
	"maps"
	"path"
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
			".claude/debug", ".claude/.credentials.json*", "Library/Caches/claude-cli-nodejs", "Library/Keychains",
		},
		"codex": {
			".codex/log", ".codex/logs_*", ".codex/cache", ".codex/tmp", ".codex/.tmp", ".codex/.tmp*",
			".codex/installation_id*", ".codex/.sandbox_migration*", ".codex/auth.json*",
		},
		"pi": {".pi/agent/auth.json*"},
	}
	readOnly := map[string][]string{
		"claude": {
			".claude.json", ".claude/settings.json", ".claude/settings.local.json", ".claude/plugins",
			".claude/skills", ".claude/agents", ".claude/commands", ".claude/hooks", ".claude/output-styles",
			".claude/CLAUDE.md", ".claude/shell-snapshots", ".claude/session-env", ".claude/sessions",
			".claude/todos", ".claude/file-history", ".claude/backups", ".claude/cache", ".claude/statsig",
		},
		"codex": {
			".codex/config.toml", ".codex/AGENTS.md", ".codex/rules", ".codex/prompts", ".codex/skills",
			".codex/plugins", ".codex/packages", ".codex/shell_snapshots", ".codex/sessions",
			".codex/archived_sessions", ".codex/sqlite", ".codex/session_index.jsonl", ".codex/state_*",
			".codex/goals_*", ".codex/memories*", ".codex/history.jsonl", ".codex/models_cache.json",
		},
		"opencode": {".config/opencode", ".cache/opencode", ".local/share/opencode", ".local/state/opencode"},
		"pi": {
			".pi/agent/sessions", ".pi/agent/settings.json", ".pi/agent/models.json", ".pi/agent/extensions",
			".pi/agent/skills", ".pi/agent/prompts", ".pi/agent/themes", ".pi/agent/AGENTS.md", ".pi/agent/mcp.json",
			".pi/agent/bin", ".pi/agent/install", ".pi/agent/npm",
		},
	}
	gotW, gotR := collectState(t)
	if !maps.EqualFunc(gotW, writable, slices.Equal) || !maps.EqualFunc(gotR, readOnly, slices.Equal) {
		t.Errorf("writable = %q, read-only = %q; want %q and %q", gotW, gotR, writable, readOnly)
	}
}

// collectState gathers every row's writable and read-only lists, checking
// each row that declares any.
func collectState(t *testing.T) (writable, readOnly map[string][]string) {
	t.Helper()
	writable, readOnly = map[string][]string{}, map[string][]string{}
	for _, agent := range All() {
		l := agent.Launch
		if l == nil || len(l.StateWritable) == 0 && len(l.StateReadOnly) == 0 {
			continue
		}
		if len(l.StateWritable) > 0 {
			writable[agent.Name] = l.StateWritable
		}
		readOnly[agent.Name] = l.StateReadOnly
		checkStatePaths(t, agent.Name, l)
	}
	return writable, readOnly
}

// checkStatePaths fails a row whose state paths are uncited, misshapen, or
// let a writable entry reach a read-only one.
func checkStatePaths(t *testing.T, name string, l *Launch) {
	t.Helper()
	if l.StateCitation == "" {
		t.Errorf("%s declares state paths without a citation", name)
	}
	checkStateShapes(t, name, l)
	for _, w := range l.StateWritable {
		if r, ok := overlapping(w, l.StateReadOnly); ok {
			t.Errorf("%s writable %q reaches its read-only %q", name, w, r)
		}
	}
	checkStartupDirs(t, name, l)
}

// checkStartupDirs fails a startup directory outside every read-only entry:
// one inside writable data needs no making.
func checkStartupDirs(t *testing.T, name string, l *Launch) {
	t.Helper()
	for _, d := range l.StateDirs {
		if !slices.ContainsFunc(l.StateReadOnly, func(r string) bool { return strings.HasPrefix(d, r+"/") }) {
			t.Errorf("%s startup directory %q sits in no read-only entry", name, d)
		}
	}
}

// checkStateShapes fails a misshapen entry, and a `*` segment outside the
// read-only list.
func checkStateShapes(t *testing.T, name string, l *Launch) {
	t.Helper()
	for _, rel := range slices.Concat(l.StateWritable, l.StateReadOnly, l.StateDirs) {
		if why := StatePathShape(strings.TrimSuffix(rel, "*")); why != "" {
			t.Errorf("%s state path %q: %s", name, rel, why)
		}
	}
	for _, rel := range slices.Concat(l.StateWritable, l.StateDirs) {
		if slices.Contains(strings.Split(rel, "/"), "*") {
			t.Errorf("%s writable or startup path %q has a * segment, which only a read-only entry may", name, rel)
		}
	}
}

// overlapping returns the first read-only entry the writable one sits
// inside or (as a prefix) names, or that it holds as anything but a
// directory: a writable directory may hold read-only directories, made
// before the launch and kept read-only over it, but a read-only file it
// holds the agent could create first.
func overlapping(w string, readOnly []string) (string, bool) {
	stem := strings.TrimSuffix(w, "*")
	for _, r := range readOnly {
		switch {
		case stem != w && strings.HasPrefix(r, stem):
			return r, true
		case r == stem, strings.HasPrefix(stem, r+"/"):
			return r, true
		case strings.HasPrefix(r, stem+"/") && strings.Contains(path.Base(r), "."):
			return r, true
		}
	}
	return "", false
}

// TestTrustedStateIsEphemeralOrScoped pins how the rows whose later
// sessions trust their own data get any writable data at all: opencode an
// ephemeral data and state home seeded with its logins, claude only its
// launch's own project directory with memory read-only.
func TestTrustedStateIsEphemeralOrScoped(t *testing.T) {
	opencode, _ := Lookup("opencode")
	want := []EphemeralVar{
		{Name: "XDG_DATA_HOME", Root: ".local/share", Seed: []string{"opencode/auth.json", "opencode/mcp-auth.json"}},
		{Name: "XDG_STATE_HOME", Root: ".local/state"},
	}
	if got := opencode.Launch.StateEphemeral; !slices.EqualFunc(got, want, sameEphemeral) {
		t.Errorf("opencode ephemeral = %+v, want %+v", got, want)
	}
	claude, _ := Lookup("claude")
	if p := claude.Launch.ProjectState; p == nil || p.Root != ".claude/projects" || p.MaxSlug != 200 ||
		!slices.Equal(p.ReadOnly, []string{"memory"}) {
		t.Errorf("claude project state = %+v", p)
	}
}

// TestOnlyTrustingRowsHaveEphemeralOrProjectState keeps ephemeral homes and
// per-launch project directories to the rows whose cited layout needs them.
func TestOnlyTrustingRowsHaveEphemeralOrProjectState(t *testing.T) {
	for _, agent := range All() {
		if l := agent.Launch; l != nil && !slices.Contains([]string{"opencode", "claude"}, agent.Name) &&
			(len(l.StateEphemeral) > 0 || l.ProjectState != nil) {
			t.Errorf("%s declares ephemeral or project state", agent.Name)
		}
	}
}

// sameEphemeral compares two ephemeral variables field by field.
func sameEphemeral(a, b EphemeralVar) bool {
	return a.Name == b.Name && a.Root == b.Root && slices.Equal(a.Seed, b.Seed)
}

// TestSourcedCodeIsNeverWritable proves no row's writable state names a
// directory that holds what a later session sources or loads — shell
// snapshots, plugins, packages, memory — whatever the row: such a directory
// is read-only, even inside writable data.
func TestSourcedCodeIsNeverWritable(t *testing.T) {
	code := []string{
		"shell_snapshots", "shell-snapshots", "session-env", "shell", "bin", "node_modules", "plugins",
		"plugin", "extensions", "hooks", "skills", "agents", "commands", "rules", "prompts", "packages",
		"memory", "npm", "install",
	}
	namesCode := func(w string) bool {
		return slices.ContainsFunc(strings.Split(strings.TrimSuffix(w, "*"), "/"), func(seg string) bool {
			return slices.Contains(code, seg)
		})
	}
	for _, agent := range All() {
		if agent.Launch == nil {
			continue
		}
		if i := slices.IndexFunc(agent.Launch.StateWritable, namesCode); i >= 0 {
			t.Errorf("%s writable %q names a code directory", agent.Name, agent.Launch.StateWritable[i])
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
