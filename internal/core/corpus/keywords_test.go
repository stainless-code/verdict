package corpuscore

import (
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// keywordCase pins one checked-in bug's vocabulary from both sides: sentences a
// reviewer writes for the defect must name it, and sentences about a different
// defect in the same file must not. The lists are written from the defect, not
// from any review, and the case files are the graded artifact, so a keyword
// that stops naming the defect (or starts naming a neighbor) fails here
// instead of quietly grading a model wrong.
type keywordCase struct {
	id     string
	passes []string
	fails  []string
}

// minKeywordsPerBug keeps a case from passing this test on one lucky phrase.
const minKeywordsPerBug = 5

var keywordCases = []keywordCase{
	{
		id: "urfave-cli-2319-required-flags-help-name",
		passes: []string{
			"The exemption is keyed on cmd.Name == helpName, so a user command called help skips checkRequiredFlags.",
			"Required-flag enforcement is keyed on cmd.Name, so a user command named help is exempted from its required flags.",
			"checkAllRequiredFlags returns nil for any command called help, including a user-defined one whose --token is required.",
			"Matching the built-in help command by name lets a user-defined help command run without its required flag.",
			"The name comparison exempts every command called help from required-flag validation.",
			"A user command named help bypasses the required-flag check because the exemption tests cmd.Name.",
		},
		fails: []string{
			"The loop bound runs one past the slice.",
			"The error returned by Set is thrown away, so a rejected false value is accepted.",
			"Rename helpName to helpCommandName for clarity.",
			"The built-in help command is exempted from required flags, which is intended.",
			"Add a nil check before Set is called on the destination.",
			"The completion command's required flags are skipped too.",
			"The table of flag names is unsorted.",
			"Move the `cmd.Name` comparisons into a helper so the two call sites share one branch.",
		},
	},
	{
		id: "urfave-cli-2450-year-zero-timestamp",
		passes: []string{
			"An explicit year zero is overwritten with today's date because the check tests the parsed value instead of the layout.",
			"A year-only layout that parses 0000 loses the year the caller wrote.",
			"The missing-year branch never asks the layout whether it carries a year, so 0000 reads as unset.",
			"A timestamp whose year parses to zero is silently replaced with the current date.",
			"The year-zero test keys on the parsed value alone, so a layout that spells out the year still has its date replaced.",
			"The flag discards an explicit year zero and fills in today's date instead.",
		},
		fails: []string{
			"The year-only layout fails to parse a value with a timezone suffix.",
			"TimestampFlag defaults to noon when no layout matches.",
			"The error from time.Parse is discarded, so an invalid value reads as zero.",
			"Rename layoutHasDate to layoutCarriesDate for clarity.",
			"The loop bound runs one past the layouts slice.",
			"A time-only value ignores the configured timezone.",
			"The flag's help text does not list the default layouts.",
		},
	},
	{
		id: "urfave-cli-2163-stop-on-nth-arg-order",
		passes: []string{
			"The StopOnNthArg check sits below the empty-arg break and the -- early return, so those arguments are swallowed.",
			"An empty argument on the boundary terminates the loop and the remaining args are lost.",
			"The boundary check is placed after the branches that consume the argument, so a positional on the boundary is dropped.",
			"The StopOnNthArg check runs too late: the -- branch returns before it.",
			"Arguments after an empty positional are discarded because the empty-arg break comes first.",
			"The early return for -- skips the Nth-argument boundary.",
			"The Nth argument boundary is checked after the branches that consume the argument.",
			"An empty positional on the boundary ends flag parsing, so the rest of the arguments are lost.",
		},
		fails: []string{
			"The loop bound runs one past the slice.",
			"The io.Copy error is discarded, so a short write reads as success.",
			"The doc comment above parseFlags should name the new field.",
			"Rename posArgs to positionals for readability.",
			"A flag that needs an argument is reported only when it is last in a group.",
			"MutuallyExclusiveFlags are checked before dispatching to a subcommand.",
			"`StopOnNthArg` is a new field generated per parse; the count is tracked on the flag set.",
		},
	},
}

// TestCheckedInCorpusKeywordsGradeBothWays proves the shipped vocabulary both
// ways: every sentence written for the defect names it, and every sentence
// about a neighboring defect leaves it unnamed.
func TestCheckedInCorpusKeywordsGradeBothWays(t *testing.T) {
	for _, kc := range keywordCases {
		t.Run(kc.id, func(t *testing.T) { checkKeywordLists(t, kc) })
	}
}

// checkKeywordLists grades one case's two lists against its manifest entry.
func checkKeywordLists(t *testing.T, kc keywordCase) {
	t.Helper()
	requireBothLists(t, kc)
	bug := keywordBug(t, kc.id)
	eachNamesDefect(t, bug, kc.passes)
	noneNamesDefect(t, bug, kc.fails)
}

// requireBothLists refuses a one-sided list: a vocabulary proven only to accept
// its own sentences says nothing about the wrong answers it must reject.
func requireBothLists(t *testing.T, kc keywordCase) {
	t.Helper()
	if len(kc.passes) < minKeywordsPerBug || len(kc.fails) < minKeywordsPerBug {
		t.Fatalf("%d pass/%d fail sentences, want at least %d of each",
			len(kc.passes), len(kc.fails), minKeywordsPerBug)
	}
}

func eachNamesDefect(t *testing.T, bug *core.CorpusBug, texts []string) {
	t.Helper()
	for _, text := range texts {
		if !namesDefect(normalize(text), bug) {
			t.Errorf("graded false, want true (sentence names the defect): %q", text)
		}
	}
}

func noneNamesDefect(t *testing.T, bug *core.CorpusBug, texts []string) {
	t.Helper()
	for _, text := range texts {
		if namesDefect(normalize(text), bug) {
			t.Errorf("graded true, want false (sentence names another defect): %q", text)
		}
	}
}

// keywordBug is the single ground-truth bug of one checked-in case.
func keywordBug(t *testing.T, id string) *core.CorpusBug {
	t.Helper()
	c, ref := loadCase(t, filepath.Join("..", "..", "..", "bench", "corpus", id, "case.json"))
	if ref != nil {
		t.Fatalf("%s: %v", id, ref)
	}
	if len(c.Bugs) != 1 {
		t.Fatalf("%s: %d ground-truth bugs, want 1", id, len(c.Bugs))
	}
	return &c.Bugs[0]
}
