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
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.Join(strings.Fields(string(raw)), " ")
}
