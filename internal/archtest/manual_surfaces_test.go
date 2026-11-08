package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// manualSurface is one operator surface docs/manual.md must keep documenting:
// the section heading that owns it, and the exact commands and artifacts the
// section must name. The terms are the shipped spelling of a CLI flag or a
// bundle file, so a rename that forgets the manual fails here instead of in an
// operator's first run. A section is checked as a unit: a term moved into an
// unrelated section still fails, because the surface no longer documents it
// where the reader looks.
type manualSurface struct {
	section string
	heading string
	terms   []string
}

// manualSurfaces is deliberately specific — a term exists only because a verb
// reads or writes that exact name. Do not replace one with a description.
var manualSurfaces = []manualSurface{
	{
		section: "policy engine",
		heading: "## Policy engine (scopes, profiles, deep validation)",
		terms:   []string{"scopes[]", "max_lenses", "default_profile", "controls-stale"},
	},
	{
		section: "path scopes and profiles",
		heading: "### Path scopes and profiles",
		terms:   []string{"scopes[]", "profiles", "max_lenses", "plan.txt"},
	},
	{
		section: "deep validation",
		heading: "### Deep validation",
		terms:   []string{"verify --group", "--command", "validation-<group>.json", "head-mismatch", "requirement-unmet"},
	},
	{
		section: "readiness blockers",
		heading: "### Readiness blockers",
		terms:   []string{"ci-failing", "profile-work", "compliance-pending"},
	},
	{
		section: "severity floors",
		heading: "### Severity floors",
		terms:   []string{"post_min_severity", "display_min_severity", "below_floor_excluded", "post_floor_scoped"},
	},
	{
		section: "evidence ledger",
		heading: "### Evidence ledger",
		terms: []string{
			"run.json", "validation-<group>.json", "filesource.json", "full.raw.diff",
			"sensitive.txt", "incremental.diff.note", "plan.txt",
		},
	},
	{
		section: "guarded submission and operator surfaces",
		heading: "### Guarded submission and operator surfaces",
		terms: []string{
			"post --review-file", "triage --dry-run", "check-tree --expect",
			"upgrade --version", "upgrade-bad-version", "scripts/verdict-complete.sh",
			"Retry-After", "upstream-diff-too-large", "300-file", "3000",
		},
	},
}

// TestManualKeepsOperatorSurfaces reads docs/manual.md and fails when a
// documented section, command, or bundle artifact disappears. It reads the
// real file rather than a fixture: the manual is the operator contract, and a
// passing test that stopped reading it would certify nothing.
func TestManualKeepsOperatorSurfaces(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "docs", "manual.md"))
	if err != nil {
		t.Fatalf("read docs/manual.md: %v", err)
	}
	manual := string(raw)
	for _, surface := range manualSurfaces {
		body, ok := manualSection(manual, surface.heading)
		if !ok {
			t.Errorf("docs/manual.md lost the %q heading; the %s section is gone", surface.heading, surface.section)
			continue
		}
		for _, term := range surface.terms {
			if !strings.Contains(body, term) {
				t.Errorf("docs/manual.md no longer names %q in the %s section (%q)", term, surface.section, surface.heading)
			}
		}
	}
}

// manualSection returns the body under one exact heading line: the lines after
// it up to the next heading of the same or higher rank. Exact heading matching
// keeps a renamed section a failure rather than a silent fallthrough, and the
// bounded body keeps a term in a different section from satisfying the check.
func manualSection(manual, heading string) (string, bool) {
	lines := strings.Split(manual, "\n")
	start, rank := -1, 0
	for i, line := range lines {
		if line != heading {
			continue
		}
		start, rank = i, headingRank(line)
		break
	}
	if start < 0 {
		return "", false
	}
	body := make([]string, 0, len(lines)-start)
	for _, line := range lines[start+1:] {
		if level := headingRank(line); level > 0 && level <= rank {
			break
		}
		body = append(body, line)
	}
	return strings.Join(body, "\n"), true
}

// headingRank reports a markdown heading's rank, or 0 for any other line.
func headingRank(line string) int {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || n >= len(line) || line[n] != ' ' {
		return 0
	}
	return n
}
