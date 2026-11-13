package exclude

import (
	"path"
	"strings"
)

// Defaults returns generated-path exclusions; tests remain reviewable.
func Defaults() []Rule {
	patterns := []string{
		"vendor/**", "node_modules/**", "dist/**", "build/**", "out/**", "target/**",
		"*.min.js", "*.min.css", "*.pb.go", "*.g.dart", "*.generated.*", "*__snapshots__/*", "*.snap",
	}
	for _, name := range []string{
		"package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock",
		"Cargo.lock", "Gemfile.lock", "poetry.lock", "go.sum",
	} {
		patterns = append(patterns, name, "*/"+name)
	}

	rules := make([]Rule, 0, len(patterns))
	for _, p := range patterns {
		rules = append(rules, Rule{ID: "default:generated", Pattern: p, Reason: "default"})
	}
	return rules
}

// Attributes reads root linguist attributes without discovering nested files.
// Each attribute is independently last-match-wins, including explicit false.
func Attributes(raw []byte, file string) bool {
	values := map[string]bool{}
	for line := range strings.SplitSeq(string(raw), "\n") {
		fields := strings.Fields(line)
		if !attributeMatch(fields, file) {
			continue
		}
		applyAttributes(values, fields[1:])
	}
	return values["linguist-generated"] || values["linguist-vendored"]
}

func applyAttributes(values map[string]bool, attrs []string) {
	for _, attr := range attrs {
		key, value, _ := strings.Cut(strings.TrimLeft(attr, "-!"), "=")
		if key == "linguist-generated" || key == "linguist-vendored" {
			values[key] = attributeValue(attr, value)
		}
	}
}

func attributeValue(attr, value string) bool {
	return !strings.HasPrefix(attr, "-") && !strings.HasPrefix(attr, "!") && (value == "" || value == "true")
}

func attributeMatch(fields []string, file string) bool {
	if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
		return false
	}
	name := file
	if !strings.Contains(fields[0], "/") {
		name = path.Base(file)
	}
	return validPattern(fields[0]) && Match(fields[0], name)
}
