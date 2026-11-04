package provenance

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// fenceLine matches a line that is exactly a backtick fence, optionally
// followed by an info string.
var fenceLine = regexp.MustCompile("^ {0,3}(`{3,})(.*)$")

// infoWord accepts the info strings these prompts use: a bare language tag.
var infoWord = regexp.MustCompile(`^[A-Za-z0-9_+.-]*$`)

// TestPromptBlocksBalanced guards the copyable skeletons in the served
// prompts. Every prompt is hashed into the review provenance, so a corrupt
// fence ships to every agent that reads the skill and nothing else compares
// its bytes to a shape.
func TestPromptBlocksBalanced(t *testing.T) {
	for _, name := range promptFiles {
		name := name
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", checkoutSkillDir, name))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			lines := strings.Split(string(raw), "\n")

			// Fence stack: a line closes the innermost open fence only at the
			// same length with no info string; a longer run nests.
			var fences []int
			for i, line := range lines {
				m := fenceLine.FindStringSubmatch(strings.TrimRight(line, " \t"))
				if m == nil {
					continue
				}
				run, info := len(m[1]), strings.TrimSpace(m[2])
				if !infoWord.MatchString(info) {
					t.Errorf("%s:%d: fence carries content on the fence line: %q", name, i+1, info)
				}
				switch {
				case len(fences) > 0 && run == fences[len(fences)-1] && info == "":
					fences = fences[:len(fences)-1]
				case len(fences) == 0 || run < fences[len(fences)-1]:
					fences = append(fences, run)
				default:
					t.Errorf("%s:%d: %d-backtick fence inside a %d-backtick fence", name, i+1, run, fences[len(fences)-1])
				}
			}
			if len(fences) > 0 {
				t.Errorf("%s: %d unclosed fence(s), innermost %d backticks", name, len(fences), fences[len(fences)-1])
			}

			// <details> pairs, in order: no closer may precede its opener.
			depth := 0
			for i, line := range lines {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "<details") {
					depth++
				}
				if strings.HasPrefix(trimmed, "</details>") {
					depth--
					if depth < 0 {
						t.Errorf("%s:%d: </details> without an opener", name, i+1)
						depth = 0
					}
				}
			}
			if depth != 0 {
				t.Errorf("%s: %d unclosed <details> block(s)", name, depth)
			}
		})
	}
}
