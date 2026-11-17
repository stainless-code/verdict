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
	lines := strings.Split(string(raw), "\n")
	seen := 0
	for i, line := range lines {
		if !agentRowLine.MatchString(line) {
			continue
		}
		seen++
		name := agentNameLiteral.FindStringSubmatch(line)
		if len(name) != 2 {
			t.Fatalf("%s:%d: row has no name literal: %s", path, i+1, line)
		}
		found := false
		for j := i - 1; j >= 0; j-- {
			trimmed := strings.TrimSpace(lines[j])
			if trimmed == "" {
				break
			}
			if agentSourceLine.MatchString(trimmed) {
				found = true
				break
			}
			if !strings.HasPrefix(trimmed, "//") && trimmed != "{" {
				break
			}
		}
		if !found {
			t.Errorf("%s:%d: agent %q has no source comment with a URL and read date", path, i+1, name[1])
		}
	}
	if seen == 0 {
		t.Fatalf("%s: scan found no agent rows; the guard would pass vacuously", path)
	}
}
