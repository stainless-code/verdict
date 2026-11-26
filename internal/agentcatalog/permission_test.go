package agentcatalog

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// keyEnvName is the env-var shape a workflow can render as both an env key and
// a secrets lookup.
var keyEnvName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// TestKeyEnvDeclaresItsCitation keeps the generated workflow's key wiring on
// the catalog: a row that names a key variable must cite the doc that says so,
// and the name must be renderable as a YAML env entry.
func TestKeyEnvDeclaresItsCitation(t *testing.T) {
	declared := 0
	for _, agent := range All() {
		if agent.Launch == nil || agent.Launch.KeyEnv == "" {
			continue
		}
		declared++
		checkKeyEnv(t, agent.Name, agent.Launch)
	}
	if declared != 2 {
		t.Fatalf("rows declaring a key env = %d; want the two documented ones (claude, codex)", declared)
	}
}

// checkKeyEnv pins one row's key variable to its citation and env shape.
func checkKeyEnv(t *testing.T, name string, launch *Launch) {
	t.Helper()
	if launch.KeyEnvCitation == "" {
		t.Errorf("%s declares KeyEnv %q without a citation", name, launch.KeyEnv)
	}
	if !keyEnvName.MatchString(launch.KeyEnv) {
		t.Errorf("%s declares KeyEnv %q; want an env name like ANTHROPIC_API_KEY", name, launch.KeyEnv)
	}
}

func TestPermissionHintsMatchManual(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "manual.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"codex", "claude", "agy"} {
		checkManualPermission(t, string(raw), name)
	}
}

func manualLaunchRow(manual, name, citation string) string {
	for candidate := range strings.SplitSeq(manual, "\n") {
		if strings.HasPrefix(candidate, "| `"+name+"`") && strings.Contains(candidate, citation) {
			return candidate
		}
	}
	return ""
}

func checkManualPermission(t *testing.T, manual, name string) {
	t.Helper()
	row, ok := Lookup(name)
	if !ok || row.Launch == nil {
		t.Fatal(name)
	}
	hint := row.Launch.PermissionHint
	if hint == "" || row.Launch.PermissionCitation == "" {
		t.Fatalf("missing permission data for %s", name)
	}
	line := manualLaunchRow(manual, name, row.Launch.Citation)
	if !strings.Contains(line, "`"+hint+"`") || !strings.Contains(line, row.Launch.PermissionCitation) {
		t.Fatalf("%s permission data absent from manual row: %s", name, line)
	}
}

func TestResumeOptionsCiteAgentDocs(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "manual.md"))
	if err != nil {
		t.Fatal(err)
	}
	declared := 0
	for _, agent := range All() {
		if agent.Launch != nil && len(agent.Launch.ResumeArgs) > 0 {
			declared++
			checkManualResume(t, string(raw), agent)
		}
	}
	if declared != 4 {
		t.Fatalf("rows declaring continuation options = %d", declared)
	}
}

func checkManualResume(t *testing.T, manual string, agent Agent) {
	t.Helper()
	line := manualLaunchRow(manual, agent.Name, agent.Launch.Citation)
	if agent.Launch.ResumeCitation == "" || !strings.Contains(line, agent.Launch.ResumeCitation) {
		t.Fatalf("%s declares continuation options without a cited row: %q", agent.Name, line)
	}
}

// discoveryRow is the literal a restricted row must declare: the exact flags
// the child receives, the doc that spells them, and the operator spellings
// that suppress them instead of duplicating them.
type discoveryRow struct {
	args      []string
	citation  string
	overrides []string
	// merges are the restriction's own denylist spellings run merges into.
	merges []string
}

// TestDiscoveryRestrictionsCiteAgentDocs pins the discovery-phase tool
// restrictions: only rows whose own CLI documents the exact spelling carry one,
// every one of them cites that doc, names the operator spellings that decide
// the same question, and appears in the manual's discovery table. The literals
// are the expectation — a fabricated or re-spelled flag has to change this
// table first.
func TestDiscoveryRestrictionsCiteAgentDocs(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "manual.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]discoveryRow{
		"bob": {
			args:     []string{"--disable-tool-groups", "execute,mcp"},
			citation: docBobShell,
			merges:   []string{"--disable-tool-groups"},
		},
		"claude": {
			args:      []string{"--disallowedTools=Bash,PowerShell"},
			citation:  docClaudeCLI,
			overrides: []string{"--tools"},
		},
		"pi": {
			args:     []string{"--exclude-tools", "bash,powershell"},
			citation: docPiCLI,
			merges:   []string{"--exclude-tools", "-xt"},
			overrides: []string{
				"--tools", "-t", "--no-builtin-tools", "-nbt", "--no-tools", "-nt",
			},
		},
	}
	declared := 0
	for _, agent := range All() {
		if agent.Launch == nil || len(agent.Launch.DiscoveryArgs) == 0 {
			continue
		}
		declared++
		row := want[agent.Name]
		checkDiscoveryRow(t, string(raw), agent, &row)
	}
	if declared != len(want) {
		t.Fatalf("rows declaring a discovery restriction = %d; want the %d documented ones", declared, len(want))
	}
}

// checkDiscoveryRow pins one row's discovery restriction to its citation and to
// the manual table that documents it.
func checkDiscoveryRow(t *testing.T, manual string, agent Agent, want *discoveryRow) {
	t.Helper()
	launch := agent.Launch
	if !slices.Equal(launch.DiscoveryArgs, want.args) {
		t.Errorf("%s discovery args = %q, want %q", agent.Name, launch.DiscoveryArgs, want.args)
	}
	if launch.DiscoveryCitation != want.citation {
		t.Errorf("%s discovery citation = %q, want %q", agent.Name, launch.DiscoveryCitation, want.citation)
	}
	if !slices.Equal(launch.DiscoveryOverrideFlags, want.overrides) {
		t.Errorf("%s discovery override flags = %q, want %q", agent.Name, launch.DiscoveryOverrideFlags, want.overrides)
	}
	if !slices.Equal(launch.DiscoveryMergeFlags, want.merges) {
		t.Errorf("%s discovery merge flags = %q, want %q", agent.Name, launch.DiscoveryMergeFlags, want.merges)
	}
	checkDiscoveryManualRow(t, manual, agent)
}

// checkDiscoveryManualRow proves the manual's discovery table names the row
// with the exact flags and the cited source, so an operator reads the same
// words the launcher emits.
func checkDiscoveryManualRow(t *testing.T, manual string, agent Agent) {
	t.Helper()
	row := ""
	for candidate := range strings.SplitSeq(manual, "\n") {
		if strings.HasPrefix(candidate, "| `"+agent.Name+"`") &&
			strings.Contains(candidate, "`"+strings.Join(agent.Launch.DiscoveryArgs, " ")+"`") {
			row = candidate
			break
		}
	}
	if row == "" {
		t.Fatalf("%s discovery flags absent from the manual: want a row carrying %q",
			agent.Name, strings.Join(agent.Launch.DiscoveryArgs, " "))
	}
	if !strings.Contains(row, agent.Launch.DiscoveryCitation) {
		t.Fatalf("%s discovery row cites no source: %s", agent.Name, row)
	}
}

// TestGrantsAreScopedAndCited pins the two grants verdict adds on its own: the
// loop's fix-phase edit grant and run's review-write grant. Exactly the rows
// whose docs name an equally scoped flag carry one, each cites its doc, and
// no grant is an approval or sandbox bypass — agy's documented hint is one,
// so it must never be reused as a grant.
func TestGrantsAreScopedAndCited(t *testing.T) {
	t.Parallel()
	edit, review := map[string][]string{}, map[string][]string{}
	for _, agent := range All() {
		l := agent.Launch
		if l == nil {
			continue
		}
		assertNoBypass(t, agent.Name, slices.Concat(l.EditArgs, l.ReviewWriteArgs))
		collectGrant(t, edit, agent.Name, "EditArgs", l.EditArgs, l.EditCitation)
		collectGrant(t, review, agent.Name, "ReviewWriteArgs", l.ReviewWriteArgs, l.ReviewWriteCitation)
	}
	wantEdit := map[string][]string{
		"claude": {"--permission-mode", "acceptEdits", "--disallowedTools=Bash,PowerShell"},
		"codex":  {"--sandbox", "workspace-write"},
	}
	wantReview := map[string][]string{"claude": {"--allowedTools=Edit({review-dir}/**)"}}
	if !maps.EqualFunc(edit, wantEdit, slices.Equal) {
		t.Errorf("EditArgs rows = %v, want %v", edit, wantEdit)
	}
	if !maps.EqualFunc(review, wantReview, slices.Equal) {
		t.Errorf("ReviewWriteArgs rows = %v, want %v", review, wantReview)
	}
}

// bypassWords are the spellings of an approval or sandbox bypass; no grant may
// carry one.
var bypassWords = []string{"dangerous", "skip-permissions", "yolo", "bypass", "danger-full-access", "full-auto", "allow-all"}

// bypassModes are permission-mode values that approve without asking; a
// grant may name a mode, never one of these.
var bypassModes = []string{"auto", "dontask", "bypasspermissions"}

// assertNoBypass fails on a grant argument that spells a bypass, as a flag
// or as the mode a --permission-mode grant selects.
func assertNoBypass(t *testing.T, name string, args []string) {
	t.Helper()
	if mode := permissionMode(args); slices.Contains(bypassModes, strings.ToLower(mode)) {
		t.Errorf("%s grants --permission-mode %s, a bypass", name, mode)
	}
	for _, arg := range args {
		for _, word := range bypassWords {
			if strings.Contains(strings.ToLower(arg), word) {
				t.Errorf("%s grants %q, a bypass (%s)", name, arg, word)
			}
		}
	}
}

// permissionMode is the mode a --permission-mode argument selects, in either
// spelling, "" when none does.
func permissionMode(args []string) string {
	for i, arg := range args {
		flag, value, inline := strings.Cut(arg, "=")
		switch {
		case flag != "--permission-mode":
		case inline:
			return value
		case i+1 < len(args):
			return args[i+1]
		}
	}
	return ""
}

// collectGrant records one row's grant and fails when it cites no doc.
func collectGrant(t *testing.T, into map[string][]string, name, field string, args []string, citation string) {
	t.Helper()
	if len(args) == 0 {
		return
	}
	into[name] = args
	if citation == "" {
		t.Errorf("%s declares %s without a citation", name, field)
	}
}
