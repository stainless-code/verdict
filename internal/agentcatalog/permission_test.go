package agentcatalog

import (
	"os"
	"path/filepath"
	"regexp"
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
