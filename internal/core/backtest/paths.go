package backtest

import (
	"path"
	"slices"
	"strings"
)

// Path-class exclusions. Each is a data-only rule the manual publishes.
var lockfiles = map[string]bool{
	"bun.lock":          true,
	"bun.lockb":         true,
	"Cargo.lock":        true,
	"composer.lock":     true,
	"Gemfile.lock":      true,
	"go.sum":            true,
	"package-lock.json": true,
	"pnpm-lock.yaml":    true,
	"poetry.lock":       true,
	"yarn.lock":         true,
}

// pathClass names the exclusion class a path belongs to, or "".
func pathClass(p string) string {
	clean := path.Clean(p)
	for part := range strings.SplitSeq(clean, "/") {
		switch part {
		case "vendor":
			return reasonVendor
		case "node_modules":
			return reasonNodeModules
		case "third_party":
			return reasonThirdParty
		}
	}
	if generatedPath(clean) {
		return reasonGenerated
	}
	return ""
}

// generatedPath reports a generated source or a dependency lockfile.
func generatedPath(p string) bool {
	base := path.Base(p)
	switch {
	case strings.HasSuffix(base, ".pb.go"):
		return true
	case strings.HasPrefix(base, "zz_generated."):
		return true
	case strings.HasSuffix(base, ".gen.go"):
		return true
	case lockfiles[base]:
		return true
	default:
		return false
	}
}

// docsTestOnly reports a fix whose every file is documentation or a test.
func docsTestOnly(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	return !slices.ContainsFunc(paths, func(p string) bool { return !docsOrTest(p) })
}

// docsOrTest reports one documentation or test path.
func docsOrTest(p string) bool {
	clean := path.Clean(p)
	base := path.Base(clean)
	parts := strings.Split(clean, "/")
	if parts[0] == "docs" || parts[0] == "test" || parts[0] == "tests" || parts[0] == "testdata" {
		return true
	}
	if slices.Contains(parts[:len(parts)-1], "__tests__") {
		return true
	}
	switch {
	case strings.HasSuffix(base, ".md"), strings.HasSuffix(base, ".mdx"), strings.HasSuffix(base, ".rst"):
		return true
	case strings.HasSuffix(base, "_test.go"):
		return true
	case strings.HasSuffix(base, ".test.js"), strings.HasSuffix(base, ".test.ts"):
		return true
	case strings.HasSuffix(base, ".spec.js"), strings.HasSuffix(base, ".spec.ts"):
		return true
	case strings.HasPrefix(base, "README"), strings.HasPrefix(base, "CHANGELOG"), strings.HasPrefix(base, "LICENSE"):
		return true
	default:
		return false
	}
}

// blankLine reports a deleted line with no anchor text. A blank line cannot
// identify the place it was deleted from.
func blankLine(text string) bool {
	return strings.TrimSpace(text) == ""
}
