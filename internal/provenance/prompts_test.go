package provenance

import (
	"fmt"
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

// TestPromptsHashUnchanged pins the shipped prompt-set hash. The built-in
// lens pack is not part of promptFiles, so shipping it must leave this value
// byte-identical; a move here is a deliberate prompt edit, never a side effect.
func TestPromptsHashUnchanged(t *testing.T) {
	t.Parallel()
	const want = "1ab11a40d5636e6d444cdcded1f23ad3bfe5c3ae90c83a3380ef4e082a364010"
	if got := PromptsHash(filepath.Join("..", "..")); got != want {
		t.Fatalf("PromptsHash = %s, want %s", got, want)
	}
}

// TestPromptBlocksBalanced guards the copyable skeletons in the served
// prompts. Every prompt is hashed into the review provenance, so a corrupt
// fence ships to every agent that reads the skill and nothing else compares
// its bytes to a shape.
func TestPromptBlocksBalanced(t *testing.T) {
	for _, name := range promptFiles {
		t.Run(name, func(t *testing.T) {
			for _, problem := range promptProblems(promptLines(t, name)) {
				t.Errorf("%s: %s", name, problem)
			}
		})
	}
}

// promptLines reads one served prompt and splits it into lines.
func promptLines(t *testing.T, name string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", checkoutSkillDir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return strings.Split(string(raw), "\n")
}

// promptProblems reports every way one prompt's copyable blocks drift from the
// shape an agent would copy verbatim. Line numbers are 1-based.
func promptProblems(lines []string) []string {
	return append(fenceProblems(lines), detailsProblems(lines)...)
}

// fenceProblems walks the fence stack: a line closes the innermost open fence
// only at the same length with no info string, and a longer run nests.
func fenceProblems(lines []string) []string {
	var problems []string
	var fences []int
	for i, line := range lines {
		fences, problems = stepFences(fences, problems, i+1, line)
	}
	return append(problems, unclosedFences(fences)...)
}

// stepFences applies one line to the fence stack, reporting content on a fence
// line and fences that nest by length.
func stepFences(fences []int, problems []string, lineno int, line string) ([]int, []string) {
	m := fenceLine.FindStringSubmatch(strings.TrimRight(line, " \t"))
	if m == nil {
		return fences, problems
	}
	run, info := len(m[1]), strings.TrimSpace(m[2])
	if !infoWord.MatchString(info) {
		problems = append(problems, fmt.Sprintf("line %d: fence carries content on the fence line: %q", lineno, info))
	}
	if problem := nestingProblem(fences, run, info); problem != "" {
		problems = append(problems, fmt.Sprintf("line %d: %s", lineno, problem))
	}
	return closedFences(fences, run, info), problems
}

// nestingProblem reports a fence that opens inside one it cannot nest in: a
// longer run, or one at the same length that carries an info string and so
// cannot close the fence it sits in.
func nestingProblem(fences []int, run int, info string) string {
	if len(fences) == 0 {
		return ""
	}
	top := fences[len(fences)-1]
	if run < top || (run == top && info == "") {
		return ""
	}
	return fmt.Sprintf("%d-backtick fence inside a %d-backtick fence", run, top)
}

// closedFences returns the stack after one fence line: a same-length line with
// no info string closes the innermost fence, a shorter run nests, and anything
// else leaves the stack alone.
func closedFences(fences []int, run int, info string) []int {
	if len(fences) == 0 {
		return append(fences, run)
	}
	switch top := fences[len(fences)-1]; {
	case run == top && info == "":
		return fences[:len(fences)-1]
	case run < top:
		return append(fences, run)
	}
	return fences
}

// unclosedFences reports the fences still open at the end of a prompt.
func unclosedFences(fences []int) []string {
	if len(fences) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("%d unclosed fence(s), innermost %d backticks", len(fences), fences[len(fences)-1])}
}

// detailsProblems checks <details> pairs in order: no closer may precede its
// opener.
func detailsProblems(lines []string) []string {
	var problems []string
	depth := 0
	for i, line := range lines {
		depth, problems = stepDetails(depth, problems, i+1, line)
	}
	if depth == 0 {
		return problems
	}
	return append(problems, fmt.Sprintf("%d unclosed <details> block(s)", depth))
}

// stepDetails applies one line to the <details> depth, reporting a closer that
// arrives with nothing open.
func stepDetails(depth int, problems []string, lineno int, line string) (int, []string) {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "<details") {
		depth++
	}
	if !strings.HasPrefix(trimmed, "</details>") {
		return depth, problems
	}
	if depth--; depth < 0 {
		return 0, append(problems, fmt.Sprintf("line %d: </details> without an opener", lineno))
	}
	return depth, problems
}

// TestPromptProblems pins the checker itself: a guard nobody has seen fail is
// a guard nobody can trust.
func TestPromptProblems(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "balanced blocks",
			src:  "text\n```go\nfmt.Println()\n```\n<details>\nhidden\n</details>\n",
		},
		{
			name: "unclosed fence",
			src:  "```go\nfmt.Println()\n",
			want: []string{"unclosed fence"},
		},
		{
			name: "content on the fence line",
			src:  "```go fmt.Println()\n```\n",
			want: []string{"content on the fence line"},
		},
		{
			name: "four backticks closed by three",
			src:  "````markdown\n```\n````\n",
			want: []string{"unclosed fence"},
		},
		{
			name: "longer run nests inside a shorter one",
			src:  "```\n````\n```\n",
			want: []string{"inside a"},
		},
		{
			name: "orphan closer",
			src:  "</details>\n",
			want: []string{"without an opener"},
		},
		{
			name: "unclosed details",
			src:  "<details>\n",
			want: []string{"unclosed <details> block"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertProblems(t, promptProblems(strings.Split(tc.src, "\n")), tc.want)
		})
	}
}

// assertProblems compares the checker's findings with the expected ones,
// matching on substrings so the cases pin causes rather than wording.
func assertProblems(t *testing.T, got, want []string) {
	t.Helper()
	switch {
	case len(want) == 0 && len(got) != 0:
		t.Fatalf("promptProblems() = %v; want no problems", got)
	case len(want) != 0 && len(got) == 0:
		t.Fatalf("promptProblems() = no problems; want a problem containing %q", want[0])
	}
	for _, expected := range want {
		if !problemContains(got, expected) {
			t.Errorf("promptProblems() = %v; want a problem containing %q", got, expected)
		}
	}
}

// problemContains reports whether any finding names the expected cause.
func problemContains(problems []string, expected string) bool {
	for _, problem := range problems {
		if strings.Contains(problem, expected) {
			return true
		}
	}
	return false
}
