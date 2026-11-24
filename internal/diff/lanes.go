package diff

import (
	"path"
	"strings"
)

// Lane is the canonical change lane a file belongs to.
type Lane string

const (
	// LaneSchema is a schema or migration change.
	LaneSchema Lane = "schema"
	// LaneSource is application source code.
	LaneSource Lane = "source"
	// LaneConfig is config or CI change.
	LaneConfig Lane = "config"
	// LaneTests is a test change.
	LaneTests Lane = "tests"
	// LaneDocs is a documentation change.
	LaneDocs Lane = "docs"
	// LaneGenerated is a generated file change.
	LaneGenerated Lane = "generated"
	// LaneLockfiles is a lockfile dependency change.
	LaneLockfiles Lane = "lockfiles"
	// LaneRenamed is a rename or move with no content change.
	LaneRenamed Lane = "renamed"
	// LaneWhitespace is a whitespace-only change.
	LaneWhitespace Lane = "whitespace"
)

// laneOrder is the risk-ordered reading order, highest risk first.
var laneOrder = []Lane{
	LaneSchema, LaneSource, LaneConfig, LaneTests, LaneDocs, LaneGenerated, LaneLockfiles, LaneRenamed, LaneWhitespace,
}

// LaneOrder returns the risk-ordered lane reading order.
func LaneOrder() []Lane {
	out := make([]Lane, len(laneOrder))
	copy(out, laneOrder)
	return out
}

// LaneRank returns the reading-order rank (0 is highest risk).
func LaneRank(l Lane) int {
	for i := range laneOrder {
		if laneOrder[i] == l {
			return i
		}
	}
	return len(laneOrder)
}

var lockfiles = map[string]bool{
	"go.sum": true, "package-lock.json": true, "bun.lock": true, "bun.lockb": true,
	"pnpm-lock.yaml": true, "yarn.lock": true, "cargo.lock": true, "poetry.lock": true,
	"uv.lock": true, "flake.lock": true, "composer.lock": true, "Gemfile.lock": true,
}

var schemaSuffixes = []string{".sql", ".proto", ".graphql", ".gql"}

// Classify assigns every changed file to one canonical lane deterministically
// from its path and diff. Precedence (highest first): pure rename with zero
// content changes, whitespace-only edits, generated markers or patterns,
// lockfiles, schema and migrations, tests, docs, config and CI, then source.
func Classify(f *FileDiff) Lane {
	if f.IsRename && f.Additions == 0 && f.Deletions == 0 {
		return classifyRename(f)
	}
	if isWhitespaceOnly(f) {
		return LaneWhitespace
	}
	return classifyPath(f)
}

func classifyRename(f *FileDiff) Lane {
	if f.Generated {
		return LaneGenerated
	}
	return LaneRenamed
}

func classifyPath(f *FileDiff) Lane {
	if f.Generated || isGeneratedPath(f) {
		return LaneGenerated
	}
	raw := effectivePath(f)
	// Normalize Windows separators so path lanes classify identically.
	norm := strings.ReplaceAll(raw, `\`, "/")
	lower := strings.ToLower(norm)
	if lockfiles[strings.ToLower(path.Base(lower))] {
		return LaneLockfiles
	}
	return classifyByPath(lower)
}

func classifyByPath(p string) Lane {
	switch {
	case isSchemaPath(p):
		return LaneSchema
	case isTestPath(p):
		return LaneTests
	case isDocPath(p):
		return LaneDocs
	case isConfigPath(p):
		return LaneConfig
	default:
		return LaneSource
	}
}

func effectivePath(f *FileDiff) string {
	if f.NewPath != "" {
		return f.NewPath
	}
	return f.OldPath
}

func isWhitespaceOnly(f *FileDiff) bool {
	dels, adds := changeLines(f)
	if len(dels) == 0 || len(dels) != len(adds) {
		return false
	}
	return equalAfterStrip(dels, adds) && hasWhitespaceDiff(dels, adds)
}

func changeLines(f *FileDiff) (dels, adds []string) {
	for i := range f.Hunks {
		dels, adds = collectHunkLines(&f.Hunks[i], dels, adds)
	}
	return dels, adds
}

func collectHunkLines(h *Hunk, dels, adds []string) ([]string, []string) {
	for j := range h.Lines {
		line := h.Lines[j]
		if line.Kind == KindDel {
			dels = append(dels, line.Text)
		}
		if line.Kind == KindAdd {
			adds = append(adds, line.Text)
		}
	}
	return dels, adds
}

func equalAfterStrip(dels, adds []string) bool {
	for i := range dels {
		if stripSpace(dels[i]) != stripSpace(adds[i]) {
			return false
		}
	}
	return true
}

func hasWhitespaceDiff(dels, adds []string) bool {
	for i := range dels {
		if dels[i] != adds[i] {
			return true
		}
	}
	return false
}

func stripSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func isGeneratedPath(f *FileDiff) bool {
	raw := strings.ReplaceAll(effectivePath(f), `\`, "/")
	p := strings.ToLower(raw)
	return strings.HasSuffix(p, ".pb.go") || strings.Contains(p, ".gen.") || strings.Contains(p, "_generated.")
}

func isSchemaPath(p string) bool {
	return isMigrationPath(p) || isSchemaBase(p) || hasSchemaSuffix(p)
}

func isMigrationPath(p string) bool {
	return strings.Contains(p, "/migrations/") || strings.HasPrefix(p, "migrations/")
}

func isSchemaBase(p string) bool {
	base := path.Base(p)
	return base == "schema.yaml" || base == "schema.yml" || strings.HasPrefix(base, "schema.") ||
		strings.HasPrefix(base, "openapi.") || base == "openapi.yaml" || base == "openapi.yml" || base == "openapi.json"
}

func hasSchemaSuffix(p string) bool {
	for i := range schemaSuffixes {
		if strings.HasSuffix(p, schemaSuffixes[i]) {
			return true
		}
	}
	return false
}

// testDirs are the directory markers that classify a path as a test.
var testDirs = []string{"test/", "tests/", "__tests__/", "testdata/"}

func isTestPath(p string) bool {
	return isTestSuffix(p) || hasTestDir(p) || hasTestInfix(p)
}

func isTestSuffix(p string) bool {
	return strings.HasSuffix(p, "_test.go") || strings.HasSuffix(p, "_test.py") ||
		strings.HasSuffix(p, ".test.ts") || strings.HasSuffix(p, ".test.js")
}

func hasTestDir(p string) bool {
	for i := range testDirs {
		d := testDirs[i]
		if strings.HasPrefix(p, d) || strings.Contains(p, "/"+d) {
			return true
		}
	}
	return false
}

func hasTestInfix(p string) bool {
	return strings.Contains(p, ".test.") || strings.Contains(p, ".spec.")
}

// docPrefixes are the filename prefixes that classify a path as docs.
var docPrefixes = []string{"readme", "changelog", "license", "contributing", "code_of_conduct"}

func isDocPath(p string) bool {
	return inDocsDir(p) || hasDocSuffix(p) || hasDocPrefix(p)
}

func inDocsDir(p string) bool {
	return strings.HasPrefix(p, "docs/") || strings.Contains(p, "/docs/")
}

func hasDocSuffix(p string) bool {
	return strings.HasSuffix(p, ".md") || strings.HasSuffix(p, ".mdx") || strings.HasSuffix(p, ".rst")
}

func hasDocPrefix(p string) bool {
	base := strings.ToLower(path.Base(p))
	for i := range docPrefixes {
		if strings.HasPrefix(base, docPrefixes[i]) {
			return true
		}
	}
	return false
}

func isConfigPath(p string) bool {
	if inGitHubDir(p) || isConfigBase(path.Base(p)) {
		return true
	}
	return hasConfigSuffix(p)
}

func inGitHubDir(p string) bool {
	return strings.HasPrefix(p, ".github/") || strings.Contains(p, "/.github/")
}

func isConfigBase(base string) bool {
	lowerBase := strings.ToLower(base)
	switch lowerBase {
	case "makefile", "dockerfile":
		return true
	}
	return strings.HasPrefix(lowerBase, "dockerfile.") || strings.HasPrefix(lowerBase, ".gitlab-ci")
}

func hasConfigSuffix(p string) bool {
	return strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml") ||
		strings.HasSuffix(p, ".toml") || strings.HasSuffix(p, ".json")
}

// LaneSummary aggregates one lane's files. Field order is the published
// property order: files and lenses render before counters.
type LaneSummary struct { //nolint:govet // field order is the published property order
	Files     []string       `json:"files"`
	ByLens    map[string]int `json:"by_lens,omitempty"`
	Findings  int            `json:"findings"`
	Lane      Lane           `json:"lane"`
	FileCount int            `json:"file_count"`
	Additions int            `json:"additions"`
	Deletions int            `json:"deletions"`
}

// GroupByLane groups files into risk-ordered lane summaries.
func GroupByLane(files []FileDiff) []LaneSummary {
	by := groupFiles(files)
	var out []LaneSummary
	for _, lane := range LaneOrder() {
		fs, ok := by[lane]
		if !ok {
			continue
		}
		out = append(out, summarize(lane, fs))
	}
	orderByRank(out)
	return out
}

func groupFiles(files []FileDiff) map[Lane][]FileDiff {
	by := map[Lane][]FileDiff{}
	for i := range files {
		lane := Classify(&files[i])
		by[lane] = append(by[lane], files[i])
	}
	return by
}

func summarize(lane Lane, fs []FileDiff) LaneSummary {
	sum := LaneSummary{Lane: lane, FileCount: len(fs), Files: make([]string, 0, len(fs))}
	for i := range fs {
		sum.Additions += fs[i].Additions
		sum.Deletions += fs[i].Deletions
		sum.Files = append(sum.Files, effectivePath(&fs[i]))
	}
	return sum
}

func orderByRank(out []LaneSummary) {
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if LaneRank(out[j].Lane) < LaneRank(out[i].Lane) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
}
