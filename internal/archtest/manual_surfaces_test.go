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
		terms:   []string{"scopes[]", "profiles", "max_lenses", "plan.txt", "bad-scope"},
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
			"post-secret-shaped", "--allow-secret-values", "secret_shapes_allowed",
		},
	},
	{
		section: "serve local HTTP surface",
		heading: "## Serve (local HTTP surface)",
		terms: []string{
			"verdict serve", "--root", "--host", "--port", "--token", "--check",
			"verdict_session_<port>", "/v1/health", "/v1/roots", "/v1/bundles",
			"/v1/roots/{root}/bundles/{bundle}", "/findings/{sig}", "?token=",
			"127.0.0.1", "token-in-query", "bad-glob",
		},
	},
}

// currentDocs are the hand-written files that describe the current binary to
// users. Release changelogs own version history; these files do not explain
// older behavior or migration paths.
var currentDocs = []string{
	"README.md",
	filepath.Join("docs", "ci-recipe.md"),
	filepath.Join("docs", "manual.md"),
	filepath.Join("docs", "config.md"),
	filepath.Join("docs", "policy-contract.md"),
}

// legacyGuidance marks prose that explains an older version rather than the
// shipped behavior. The words are checked case-insensitively per line so the
// failure names the exact file and line to delete.
var legacyGuidance = []string{
	"legacy", "pre-0.5", "pre-policy", "pre-snapshot", "before 0.", "migration note",
}

// TestCurrentDocsHaveNoLegacyGuidance keeps user docs on current behavior.
// History belongs in the release changelog, not in an operator's manual.
func TestCurrentDocsHaveNoLegacyGuidance(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	for _, rel := range currentDocs {
		checkDocForLegacyGuidance(t, root, rel)
	}
}

// checkDocForLegacyGuidance reads one current doc and reports every line that
// explains an older version instead of the shipped behavior.
func checkDocForLegacyGuidance(t *testing.T, root, rel string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	for i, line := range strings.Split(string(raw), "\n") {
		checkLegacyTerms(t, rel, i+1, line)
	}
}

// checkLegacyTerms reports each legacy term found on one line.
func checkLegacyTerms(t *testing.T, rel string, lineNo int, line string) {
	t.Helper()
	lower := strings.ToLower(line)
	for _, term := range legacyGuidance {
		if strings.Contains(lower, term) {
			t.Errorf("%s:%d explains %q; document current behavior only", rel, lineNo, term)
		}
	}
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
		assertManualSurface(t, manual, surface)
	}
}

// assertManualSurface checks one surface's heading and every required term in
// the body that heading owns.
func assertManualSurface(t *testing.T, manual string, surface manualSurface) {
	t.Helper()
	body, ok := manualSection(manual, surface.heading)
	if !ok {
		t.Errorf("docs/manual.md lost the %q heading; the %s section is gone", surface.heading, surface.section)
		return
	}
	for _, term := range surface.terms {
		if !strings.Contains(body, term) {
			t.Errorf("docs/manual.md no longer names %q in the %s section (%q)", term, surface.section, surface.heading)
		}
	}
}

// manualSection returns the body under one exact heading line: the lines after
// it up to the next heading of the same or higher rank. Exact heading matching
// keeps a renamed section a failure rather than a silent fallthrough, and the
// bounded body keeps a term in a different section from satisfying the check.
func manualSection(manual, heading string) (string, bool) {
	lines := strings.Split(manual, "\n")
	start, rank, ok := headingLine(lines, heading)
	if !ok {
		return "", false
	}
	return strings.Join(sectionLines(lines, start, rank), "\n"), true
}

// headingLine finds the exact heading line and reports its index and rank.
func headingLine(lines []string, heading string) (int, int, bool) {
	for i, line := range lines {
		if line == heading {
			return i, headingRank(line), true
		}
	}
	return 0, 0, false
}

// sectionLines collects the lines after start until the next heading of the
// same or higher rank outside a fenced code block. A `#` comment in a shell
// example is literal text, not a section boundary.
func sectionLines(lines []string, start, rank int) []string {
	body := make([]string, 0, len(lines)-start)
	fence := ""
	for _, line := range lines[start+1:] {
		if fence != "" {
			body, fence = consumeFencedLine(body, line, fence)
			continue
		}
		if marker := fenceMarker(line); marker != "" {
			fence = marker
			body = append(body, line)
			continue
		}
		if isSectionHeading(line, rank) {
			break
		}
		body = append(body, line)
	}
	return body
}

// consumeFencedLine appends one line inside a fenced block and clears fence
// when the line closes it.
func consumeFencedLine(body []string, line, fence string) ([]string, string) {
	if strings.HasPrefix(strings.TrimSpace(line), fence) {
		fence = ""
	}
	return append(body, line), fence
}

// isSectionHeading reports whether line closes the section headed at rank.
func isSectionHeading(line string, rank int) bool {
	level := headingRank(line)
	return level > 0 && level <= rank
}

// fenceMarker reports the opening fence marker at the start of a line, if any.
func fenceMarker(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	switch {
	case strings.HasPrefix(trimmed, "```"):
		return "```"
	case strings.HasPrefix(trimmed, "~~~"):
		return "~~~"
	default:
		return ""
	}
}

// TestSectionLinesIgnoreFencedHeadings pins the boundary rule: a shell comment
// shaped like a heading stays in the fenced body and cannot truncate a section.
func TestSectionLinesIgnoreFencedHeadings(t *testing.T) {
	t.Parallel()
	lines := []string{
		"### Section",
		"```sh",
		"# Example",
		"term",
		"```",
		"### Next",
		"outside",
	}
	got := strings.Join(sectionLines(lines, 0, 3), "\n")
	if !strings.Contains(got, "# Example") || !strings.Contains(got, "term") {
		t.Fatalf("fenced body lost: %q", got)
	}
	if strings.Contains(got, "### Next") || strings.Contains(got, "outside") {
		t.Fatalf("section ran past the next heading: %q", got)
	}
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
