package archtest

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var agentsPromptRule = regexp.MustCompile(
	"Exactly ([a-z]+) Procedure files are in `promptFiles`:\\*\\* " +
		"`procedure/PROCEDURE\\.md` and `procedure/prompts/\\{([a-z,]+)\\}\\.md`")

var countWords = map[string]int{
	"one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
	"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
}

func TestAgentsHashedPromptSetMatchesProvenance(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	match := agentsPromptRule.FindStringSubmatch(flattenDoc(t, filepath.Join(root, "AGENTS.md")))
	if match == nil {
		t.Fatal("AGENTS.md no longer states the hashed Procedure files in the sentence this guard reads")
	}
	documented := []string{"PROCEDURE.md"}
	for name := range strings.SplitSeq(match[2], ",") {
		documented = append(documented, "prompts/"+name+".md")
	}
	slices.Sort(documented)
	hashed := hashedPromptFiles(t, root)
	if !slices.Equal(documented, hashed) {
		t.Errorf("AGENTS.md documents %v; PromptsHash hashes %v", documented, hashed)
	}
	want, ok := countWords[match[1]]
	if !ok {
		t.Fatalf("AGENTS.md says %q skill files; add that numeral to countWords", match[1])
	}
	if want != len(hashed) {
		t.Errorf("AGENTS.md says %s skill files; PromptsHash hashes %d", match[1], len(hashed))
	}
}

// promptFiles is unexported, and a copy re-typed here would pass while the
// hashed set moved.
func hashedPromptFiles(t *testing.T, root string) []string {
	t.Helper()
	path := filepath.Join(root, "internal", "provenance", "provenance.go")
	lit := promptFilesLiteral(t, path)
	names := make([]string, 0, len(lit.Elts))
	for _, elt := range lit.Elts {
		name, ok := stringLiteral(elt)
		if !ok {
			t.Fatalf("%s: promptFiles holds a non-string entry", path)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func promptFilesLiteral(t *testing.T, path string) *ast.CompositeLit {
	t.Helper()
	var lit *ast.CompositeLit
	ast.Inspect(parseGo(t, path), func(node ast.Node) bool {
		value, ok := node.(*ast.ValueSpec)
		if !ok || len(value.Names) != 1 || value.Names[0].Name != "promptFiles" {
			return true
		}
		if composite, ok := value.Values[0].(*ast.CompositeLit); ok {
			lit = composite
		}
		return false
	})
	if lit == nil {
		t.Fatalf("%s declares no promptFiles composite literal", path)
	}
	return lit
}

func readDoc(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

func flattenDoc(t *testing.T, path string) string {
	t.Helper()
	return strings.Join(strings.Fields(readDoc(t, path)), " ")
}

const roadmapPage = "apps/docs/content/04-roadmap/index.mdx"

var backtickedVerb = regexp.MustCompile("`([a-z][a-z-]*)`")

func TestRoadmapListsEveryShippedVerb(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	documented := roadmapCommands(t, readDoc(t, filepath.Join(root, filepath.FromSlash(roadmapPage))))
	slices.Sort(documented)
	offered := cliVerbs(t, root)
	slices.Sort(offered)
	if !slices.Equal(documented, offered) {
		t.Errorf("the roadmap's Shipped today table lists %v; the CLI synopsis offers %v", documented, offered)
	}
}

func roadmapCommands(t *testing.T, page string) []string {
	t.Helper()
	commands := tableCommands(roadmapSection(t, page))
	if len(commands) == 0 {
		t.Fatalf("%s: the Shipped today table lists no commands", roadmapPage)
	}
	return commands
}

func roadmapSection(t *testing.T, page string) string {
	t.Helper()
	start := strings.Index(page, "\n## Shipped today\n")
	end := strings.Index(page, "\n## Next\n")
	if start < 0 || end < start {
		t.Fatalf("%s has no \"## Shipped today\" section before \"## Next\"", roadmapPage)
	}
	return page[start:end]
}

func tableCommands(section string) []string {
	var commands []string
	for line := range strings.SplitSeq(section, "\n") {
		columns := strings.Split(line, "|")
		if !strings.HasPrefix(line, "|") || len(columns) < 4 {
			continue
		}
		commandCell := columns[2]
		for _, match := range backtickedVerb.FindAllStringSubmatch(commandCell, -1) {
			commands = append(commands, match[1])
		}
	}
	return commands
}

func TestBenchmarkCorpusCountMatchesTree(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	dirs := corpusCaseDirs(t, root)
	realCases := 0
	for _, dir := range dirs {
		if caseKind(t, filepath.Join(dir, "case.json")) == "real" {
			realCases++
		}
	}
	doc := flattenDoc(t, filepath.Join(root, "docs", "benchmark.md"))
	for _, want := range []string{
		fmt.Sprintf("%d case directories", len(dirs)),
		fmt.Sprintf("%d real-PR cases", realCases),
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/benchmark.md never says %q; bench/corpus/ holds that many", want)
		}
	}
}

func corpusCaseDirs(t *testing.T, root string) []string {
	t.Helper()
	corpus := filepath.Join(root, "bench", "corpus")
	entries, err := os.ReadDir(corpus)
	if err != nil {
		t.Fatalf("read %s: %v", corpus, err)
	}
	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, filepath.Join(corpus, entry.Name()))
		}
	}
	if len(dirs) == 0 {
		t.Fatalf("%s holds no case directories; the guard would pass vacuously", corpus)
	}
	return dirs
}

func caseKind(t *testing.T, path string) string {
	t.Helper()
	var manifest struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(readDoc(t, path)), &manifest); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return manifest.Kind
}
