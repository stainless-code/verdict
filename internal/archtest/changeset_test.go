package archtest

// Repository guard for the changeset frontmatter. The release tooling only
// sees a `.changeset/*.md` file whose YAML frontmatter parses: a file born
// without the opening `---` is silently dropped from the release notes, and a
// bump naming a package that no `package.json` declares would fail the release
// run long after the change landed. This test fails, naming the file, when a
// changeset stops matching the documented shape (STA-71).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// changesetBump is the only shape a frontmatter row may take.
var changesetBump = regexp.MustCompile(`^"([^"]+)": (?:patch|minor|major)$`)

// changesetSkipDirs are trees that carry no package identity of their own.
var changesetSkipDirs = map[string]bool{
	".git":         true,
	".pi":          true,
	"bin":          true,
	"dist":         true,
	"node_modules": true,
}

// TestChangesetFrontmatter checks every pending changeset in the repo.
func TestChangesetFrontmatter(t *testing.T) {
	root := moduleRoot(t)
	packages := repoPackageNames(t, root)
	dir := filepath.Join(root, ".changeset")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, entry := range entries {
		name, ok := changesetFile(entry)
		if !ok {
			continue
		}
		t.Run(name, func(t *testing.T) {
			checkChangesetFile(t, dir, name, packages)
		})
	}
}

// checkChangesetFile reports every way one changeset drifts from the shape,
// naming the file each time.
func checkChangesetFile(t *testing.T, dir, name string, packages map[string]bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read changeset: %v", err)
	}
	for _, problem := range changesetProblems(string(raw), packages) {
		t.Errorf("%s: %s", name, problem)
	}
}

// changesetFile reports whether a directory entry is a pending changeset.
func changesetFile(entry os.DirEntry) (string, bool) {
	name := entry.Name()
	if entry.IsDir() || !strings.HasSuffix(name, ".md") || name == "README.md" {
		return "", false
	}
	return name, true
}

// TestChangesetProblems pins the checker itself: a guard nobody has seen fail
// is a guard nobody can trust.
func TestChangesetProblems(t *testing.T) {
	t.Parallel()
	const pkg = `"@stainless-code/verdict"`
	known := map[string]bool{
		"@stainless-code/verdict":    true,
		"@stainless-code/verdict-ts": true,
	}
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "shipped shape",
			src:  "---\n" + pkg + ": patch\n---\n\nA body.\n",
		},
		{
			name: "several packages, several levels",
			src:  "---\n" + pkg + ": minor\n\"@stainless-code/verdict-ts\": major\n---\n\nA body.\n",
		},
		{
			name: "missing opening delimiter",
			src:  pkg + ": patch\n---\n\nA body.\n",
			want: []string{"missing opening"},
		},
		{
			name: "missing both delimiters",
			src:  pkg + ": minor\n\nA body.\n",
			want: []string{"missing opening"},
		},
		{
			name: "missing closing delimiter",
			src:  "---\n" + pkg + ": patch\n\nA body.\n",
			want: []string{"missing closing"},
		},
		{
			name: "no package declared",
			src:  "---\n---\n\nA body.\n",
			want: []string{"declares no package"},
		},
		{
			name: "unknown package",
			src:  "---\n" + `"@stainless-code/ghost"` + ": patch\n---\n\nA body.\n",
			want: []string{"unknown package"},
		},
		{
			name: "bad bump level",
			src:  "---\n" + pkg + ": huge\n---\n\nA body.\n",
			want: []string{"not `\"<package>\": patch|minor|major`"},
		},
		{
			name: "unquoted package",
			src:  "---\n@stainless-code/verdict: patch\n---\n\nA body.\n",
			want: []string{"not `\"<package>\": patch|minor|major`"},
		},
		{
			name: "empty body",
			src:  "---\n" + pkg + ": patch\n---\n\n\n",
			want: []string{"empty body"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertChangesetProblems(t, changesetProblems(tc.src, known), tc.want)
		})
	}
}

// assertChangesetProblems compares the checker's findings with the expected
// ones, matching on substrings so the tests pin causes rather than wording.
func assertChangesetProblems(t *testing.T, got, want []string) {
	t.Helper()
	switch {
	case len(want) == 0 && len(got) != 0:
		t.Fatalf("changesetProblems() = %v; want no problems", got)
	case len(want) != 0 && len(got) == 0:
		t.Fatalf("changesetProblems() = no problems; want %v", want)
	}
	for _, expected := range want {
		if !containsProblem(got, expected) {
			t.Errorf("changesetProblems() = %v; want a problem containing %q", got, expected)
		}
	}
}

// containsProblem reports whether any finding names the expected cause.
func containsProblem(problems []string, expected string) bool {
	return slices.ContainsFunc(problems, func(problem string) bool {
		return strings.Contains(problem, expected)
	})
}

// changesetProblems reports every reason a changeset would be mis-parsed or
// dropped. The shape is fixed: an opening `---`, one or more
// `"<package>": patch|minor|major` rows, a closing `---`, then prose.
func changesetProblems(src string, packages map[string]bool) []string {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	rows, body, problems := splitFrontmatter(lines)
	if problems != nil {
		return problems
	}
	problems = append(problems, rowProblems(rows, packages)...)
	if strings.TrimSpace(strings.Join(body, "\n")) == "" {
		problems = append(problems, "empty body after the frontmatter")
	}
	return problems
}

// splitFrontmatter cuts the file into frontmatter rows and body, refusing a
// file whose delimiters cannot be found at all.
func splitFrontmatter(lines []string) (rows, body, problems []string) {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, nil, []string{"missing opening '---' frontmatter delimiter"}
	}
	end := indexDelimiter(lines[1:])
	if end < 0 {
		return nil, nil, []string{"missing closing '---' frontmatter delimiter"}
	}
	return lines[1 : end+1], lines[end+2:], nil
}

// indexDelimiter returns the offset of the closing delimiter, or -1 when the
// frontmatter never closes.
func indexDelimiter(lines []string) int {
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			return i
		}
	}
	return -1
}

// rowProblems checks each frontmatter row against the bump grammar and the
// repository's package names.
func rowProblems(rows []string, packages map[string]bool) []string {
	if len(rows) == 0 {
		return []string{"frontmatter declares no package"}
	}
	var problems []string
	for i, row := range rows {
		match := changesetBump.FindStringSubmatch(strings.TrimSpace(row))
		if match == nil {
			problems = append(problems, fmt.Sprintf(
				"frontmatter line %d is not `\"<package>\": patch|minor|major`: %q", i+2, row))
			continue
		}
		if !packages[match[1]] {
			problems = append(problems, fmt.Sprintf(
				"frontmatter names unknown package %q (no package.json declares it)", match[1]))
		}
	}
	return problems
}

// repoPackageNames collects every name the repository's package.json files
// declare, so a changeset can only name a package that ships.
func repoPackageNames(t *testing.T, root string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		return collectPackageName(t, path, entry, err, names)
	})
	if err != nil {
		t.Fatalf("scan package.json files: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no package.json names found; the guard would pass vacuously")
	}
	return names
}

// collectPackageName records one package.json name on the walk.
func collectPackageName(t *testing.T, path string, entry os.DirEntry, err error, names map[string]bool) error {
	t.Helper()
	if err != nil {
		return err
	}
	if entry.IsDir() {
		if changesetSkipDirs[entry.Name()] {
			return filepath.SkipDir
		}
		return nil
	}
	if entry.Name() != "package.json" {
		return nil
	}
	name, ok := packageName(path)
	if !ok {
		t.Fatalf("%s: package.json declares no name", path)
	}
	names[name] = true
	return nil
}

// packageName reads the name field of one package.json.
func packageName(path string) (string, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var manifest struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest.Name == "" {
		return "", false
	}
	return manifest.Name, true
}
