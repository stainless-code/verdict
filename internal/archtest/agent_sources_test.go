package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/agentcatalog"
)

func TestAgentCatalogIntegrity(t *testing.T) {
	seen := map[string]bool{}
	for _, row := range agentcatalog.All() {
		for _, name := range append([]string{row.Name}, row.Aliases...) {
			if seen[name] {
				t.Errorf("duplicate name or alias %q", name)
			}
			seen[name] = true
		}
		checkCatalogCitations(t, row)
	}
}

func checkCatalogCitations(t *testing.T, row agentcatalog.Agent) {
	t.Helper()
	citations := map[string]string{}
	if row.Launch != nil {
		citations["launch"] = row.Launch.Citation
	}
	if row.Skills != nil {
		citations["skills"] = row.Skills.Citation
	}
	if row.MCP != nil {
		citations["MCP"] = row.MCP.Citation
	}
	for group, cite := range citations {
		if !strings.HasPrefix(cite, "https://") {
			t.Errorf("%s %s missing citation", row.Name, group)
		}
	}
}

// TestAgentNamesMatchTheirSurfaces pins the two human lists of --agent names
// against the shipped table. A row that exists in code but not in usage or the
// docs is undiscoverable; a name in the docs that no row answers is a lie.
func TestAgentNamesMatchTheirSurfaces(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	var names []string
	for _, row := range agentcatalog.All() {
		if row.Launch != nil {
			names = append(names, row.Name)
		}
	}
	for _, surface := range []struct {
		path   string
		quoted bool
	}{
		{path: filepath.Join(root, "cmd", "verdict", "usage.go")},
		{path: filepath.Join(root, "docs", "manual.md"), quoted: true},
		{path: filepath.Join(root, "procedure", "references", "commands.md"), quoted: true},
	} {
		assertSurfaceNames(t, surface.path, surface.quoted, names)
	}
}

func assertSurfaceNames(t *testing.T, path string, quoted bool, names []string) {
	t.Helper()
	doc, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		want := surfaceWant(name, quoted)
		if !strings.Contains(string(doc), want) {
			t.Errorf("%s does not name agent %q (want %q)", path, name, want)
		}
	}
}

func surfaceWant(name string, quoted bool) string {
	if quoted {
		return "`" + name + "`"
	}
	return name
}
