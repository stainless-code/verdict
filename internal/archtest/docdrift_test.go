package archtest

import (
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// agentsPromptRule is the one AGENTS.md sentence that says how many skill files
// PromptsHash covers and which ones: the count word, the entry point, and the
// brace form naming the hashed prompts.
var agentsPromptRule = regexp.MustCompile(
	"Exactly ([a-z]+) skill files are hashed into goldens:\\*\\* " +
		"`skills/verdict/SKILL\\.md` and `prompts/\\{([a-z,]+)\\}\\.md`")

// countWords names the English numerals the contributor docs spell counts with.
var countWords = map[string]int{
	"one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
	"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
}

// TestAgentsHashedPromptSetMatchesProvenance holds the hand-written list in
// AGENTS.md to the files PromptsHash hashes, so a prompt added to one cannot
// leave the other naming the old set or the old count.
func TestAgentsHashedPromptSetMatchesProvenance(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	match := agentsPromptRule.FindStringSubmatch(flattenDoc(t, filepath.Join(root, "AGENTS.md")))
	if match == nil {
		t.Fatal("AGENTS.md no longer states the hashed skill files in the sentence this guard reads")
	}
	documented := []string{"SKILL.md"}
	for _, name := range strings.Split(match[2], ",") {
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

// hashedPromptFiles reads the list PromptsHash walks from its declaration.
// promptFiles is unexported, and a copy re-typed here would pass while the
// hashed set moved.
func hashedPromptFiles(t *testing.T, root string) []string {
	t.Helper()
	path := filepath.Join(root, "internal", "provenance", "provenance.go")
	for _, decl := range parseGo(t, path).Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || value.Names[0].Name != "promptFiles" {
				continue
			}
			lit, ok := value.Values[0].(*ast.CompositeLit)
			if !ok {
				t.Fatalf("%s: promptFiles is not a composite literal", path)
			}
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
	}
	t.Fatalf("%s declares no promptFiles list", path)
	return nil
}

// flattenDoc reads one Markdown file with its wrapped lines joined, so a guard
// matches a sentence however the paragraph is hard-wrapped.
func flattenDoc(t *testing.T, path string) string {
	t.Helper()
	return strings.Join(strings.Fields(readDoc(t, path)), " ")
}

// readDoc reads one documentation file.
func readDoc(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// TestRoadmapListsEveryShippedVerb holds the docs-site roadmap to the CLI
// synopsis: its "Shipped today" table is the page's claim about what ships, so
// a verb the binary offers and the table omits is a stale page, and a verb the
// table names that the synopsis dropped is a stale row.
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

// roadmapPage is the docs-site page whose table claims what ships today.
const roadmapPage = "apps/docs/content/04-roadmap/index.mdx"

// backtickedVerb matches one command name a table cell names in backticks.
var backtickedVerb = regexp.MustCompile("`([a-z][a-z-]*)`")

// roadmapCommands reads the commands the roadmap's "Shipped today" table lists
// in its Commands column.
func roadmapCommands(t *testing.T, page string) []string {
	t.Helper()
	start := strings.Index(page, "\n## Shipped today\n")
	end := strings.Index(page, "\n## Next\n")
	if start < 0 || end < start {
		t.Fatalf("%s has no \"## Shipped today\" section before \"## Next\"", roadmapPage)
	}
	var commands []string
	for _, line := range strings.Split(page[start:end], "\n") {
		columns := strings.Split(line, "|")
		if !strings.HasPrefix(line, "|") || len(columns) < 4 {
			continue
		}
		for _, match := range backtickedVerb.FindAllStringSubmatch(columns[2], -1) {
			commands = append(commands, match[1])
		}
	}
	if len(commands) == 0 {
		t.Fatalf("%s: the Shipped today table lists no commands", roadmapPage)
	}
	return commands
}
