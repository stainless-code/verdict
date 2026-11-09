package readcommon

import (
	"regexp"
	"strings"
)

// PathRole is the one roles_for_paths role heuristic, shared by the fetch
// roles build and the report overview: test, generated, docs, config, or
// source.
func PathRole(path string) string {
	base := path
	if _, after, ok := strings.CutLast(path, pathSep); ok {
		base = after
	}
	switch {
	case roleTestRe.MatchString(path) || roleTestBaseRe.MatchString(base) || roleTestExtRe.MatchString(base):
		return "test"
	case isGeneratedPath(path, base):
		return "generated"
	case roleDocsBaseRe.MatchString(base) || strings.HasPrefix(path, "docs/"):
		return "docs"
	case roleConfigBaseRe.MatchString(base) || roleConfigRcRe.MatchString(base) || roleConfigExtRe.MatchString(base):
		return "config"
	default:
		return "source"
	}
}

// Generated reports whether the shared role heuristic classifies path as
// generated: lockfiles, build output, vendored trees, minified assets. The
// review manifest, the report overview, and the coverage exemption all read
// this one predicate instead of owning a second list.
func Generated(path string) bool {
	return PathRole(path) == "generated"
}

// role patterns, mirroring roles_for_paths.
var (
	roleTestRe       = regexp.MustCompile(`(^|/)(test|tests|__tests__|e2e|spec)(s)?/`)
	roleTestBaseRe   = regexp.MustCompile(`^(test_|spec_)`)
	roleTestExtRe    = regexp.MustCompile(`[._](test|spec)\.`)
	roleDocsBaseRe   = regexp.MustCompile(`\.md$|\.txt$|^LICENSE`)
	roleConfigBaseRe = regexp.MustCompile(`^(package\.json|tsconfig.*|Dockerfile.*|Makefile|CMakeLists\.txt|pom\.xml|` +
		`pyproject\.toml|go\.mod|go\.sum|Cargo\.toml|Gemfile)`)
	roleConfigRcRe  = regexp.MustCompile(`^\..*rc`)
	roleConfigExtRe = regexp.MustCompile(`\.(yml|yaml|toml)$`)
)

// generatedSubstrings lists the generated-path literal markers.
var generatedSubstrings = []string{
	"package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock",
	"Cargo.lock", "Gemfile.lock", "poetry.lock", ".min.js", ".min.css",
	"__snapshots__/", ".snap", ".pb.go", ".g.dart", ".generated.",
}

// isGeneratedPath owns the generated branch, including the start-anchored
// dist/build/vendor prefix.
func isGeneratedPath(path, base string) bool {
	for _, sub := range generatedSubstrings {
		if strings.Contains(path, sub) {
			return true
		}
	}
	for _, prefix := range []string{"dist/", "build/", "vendor/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return strings.HasSuffix(base, ".snap")
}

// pathSep separates path segments.
const pathSep = "/"
