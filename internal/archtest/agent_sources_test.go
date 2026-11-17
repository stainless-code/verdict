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
