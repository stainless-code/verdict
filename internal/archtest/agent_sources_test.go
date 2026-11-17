package archtest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	agentRowLine     = regexp.MustCompile(`^\s*Name: "`)
	agentSourceLine  = regexp.MustCompile(`^// https://\S+ — read 2026-11-17$`)
	agentNameLiteral = regexp.MustCompile(`Name: "([a-z0-9-]+)"`)
	agentBlockLine   = regexp.MustCompile(`^(//.*|\{)$`)
)

// TestBuiltinAgentsCarrySourceComments reads the shipped agent table and fails
// when a row loses the one-line primary-source citation above it. The citation
// is the evidence trail for a headless argv and a model flag; a row added from
// memory is not a row this repo can defend.
func TestBuiltinAgentsCarrySourceComments(t *testing.T) {
	t.Parallel()
	path := filepath.Join(moduleRoot(t), "internal", "core", "run", "agents.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if checkAgentSourceComments(t, path, strings.Split(string(raw), "\n")) == 0 {
		t.Fatalf("%s: scan found no agent rows; the guard would pass vacuously", path)
	}
}

// TestAgentNamesMatchTheirSurfaces pins the two human lists of --agent names
// against the shipped table. A row that exists in code but not in usage or the
// docs is undiscoverable; a name in the docs that no row answers is a lie.
func TestAgentNamesMatchTheirSurfaces(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	names := shippedAgentNames(t, filepath.Join(root, "internal", "core", "run", "agents.go"))
	for _, surface := range []struct {
		path   string
		quoted bool
	}{
		{path: filepath.Join(root, "cmd", "verdict", "usage.go")},
		{path: filepath.Join(root, "docs", "manual.md"), quoted: true},
		{path: filepath.Join(root, "skills", "verdict", "references", "commands.md"), quoted: true},
	} {
		assertSurfaceNames(t, surface.path, surface.quoted, names)
	}
}

func shippedAgentNames(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	matches := agentNameLiteral.FindAllStringSubmatch(string(raw), -1)
	if len(matches) == 0 {
		t.Fatal("agent table scan found no names; the guard would pass vacuously")
	}
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, match[1])
	}
	return names
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

func checkAgentSourceComments(t *testing.T, path string, lines []string) int {
	t.Helper()
	seen := 0
	for i, line := range lines {
		if !agentRowLine.MatchString(line) {
			continue
		}
		seen++
		assertAgentSourceRow(t, path, lines, i, line)
	}
	return seen
}

func assertAgentSourceRow(t *testing.T, path string, lines []string, i int, line string) {
	t.Helper()
	name := agentNameLiteral.FindStringSubmatch(line)
	if len(name) != 2 {
		t.Fatalf("%s:%d: row has no name literal: %s", path, i+1, line)
	}
	if !sourceCommentAbove(lines, i) {
		t.Errorf("%s:%d: agent %q has no source comment with a URL and read date", path, i+1, name[1])
	}
}

// sourceCommentAbove walks through the opening brace and the row's own comment
// block, stopping at the blank line that separates one row from the next.
func sourceCommentAbove(lines []string, row int) bool {
	for j := row - 1; j >= 0; j-- {
		trimmed := strings.TrimSpace(lines[j])
		if agentSourceLine.MatchString(trimmed) {
			return true
		}
		if !agentBlockLine.MatchString(trimmed) {
			return false
		}
	}
	return false
}
