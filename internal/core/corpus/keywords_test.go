package corpuscore

import (
	"fmt"
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
	// bug is the index into the case's bugs, so a case whose fix commit
	// repaired two defects pins each of them from both sides. Zero for the
	// common one-bug case. It trails the pointer fields so the GC's scan of
	// this struct stops before it (fieldalignment).
	bug int
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
			"A year-only layout that parses 0000 is treated as unset, so the caller's year zero is replaced with today.",
			"Parsing \"0000\" with the 2006 layout yields year 0 and the date is silently rewritten to the current day.",
			"A literal year 0 in a year-carrying layout is treated as unset and its date is replaced with today.",
			"The parsed timestamp is rewritten: year zero becomes the current date.",
			"Parsing 0000 succeeds, then the missing-year branch discards the year zero and stamps today.",
		},
		fails: []string{
			"The year-only layout fails to parse a value with a timezone suffix.",
			"TimestampFlag defaults to noon when no layout matches.",
			"The error from time.Parse is discarded, so an invalid value reads as zero.",
			"Rename layoutHasDate to layoutCarriesDate for clarity.",
			"The loop bound runs one past the layouts slice.",
			"A time-only value ignores the configured timezone.",
			"The flag's help text does not list the default layouts.",
			"For a time-only layout in UTC-5 this condition leaves January 1 instead of the current date, so the caller's zone is ignored.",
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
			"A double dash sitting exactly on the Nth positional is consumed by the `--` branch before the parser stops, so the action never sees it.",
			"The Nth-argument check runs after the branches that handle the argument, so an empty positional ends parsing and the rest are lost.",
			"Empty positional: the rest of the arguments are lost because the break runs first.",
			"An empty positional makes the loop break, so the Nth-argument check never runs and the trailing arguments vanish.",
			"A `--` on the boundary fires the early return first; the stop check is below it.",
			"Arguments are dropped because the branch that consumes the argument runs before the boundary check.",
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
	{
		// Two bugs, one fix commit: #4425 corrected the map key and replaced
		// destroy() with close(). Each side is pinned here so a finding about
		// either defect grades as a hit and a finding about the other does not.
		id: "undici-4223-agent-client-eviction",
		passes: []string{
			"The lookup is keyed on the URL the event carries while the map stores the origin string, so the count never moves.",
			"The entry is deleted under a key the map does not use, so the client outlives its connections.",
			"Bookkeeping that never fires leaves unbounded growth of the client map.",
			"Each Client is looked up with a different key than the one the map stores, so the lookup can never find the entry.",
			"The dispatcher is never evicted because the count never moves off zero.",
		},
		fails: []string{
			"Calling destroy() here kills in-flight requests that close() would have drained.",
			"The eviction tears down queued requests instead of letting the dispatcher finish them.",
			"Rename result to entry so the shape is obvious.",
			"The mock agent stores {count, dispatcher} too, which is fine.",
			"The eviction runs inside the disconnect event, which blocks the socket close.",
			"The disconnect handler does not check that targets is present, so a nil dereference is possible.",
		},
	},
	{
		id:  "undici-4223-agent-client-eviction",
		bug: 1,
		passes: []string{
			"Calling destroy() here kills in-flight requests that close() would have drained.",
			"The eviction tears down queued requests instead of letting the dispatcher finish them.",
			"Evicting with destroy() aborts pending work: a request queued right then fails with ClientDestroyedError.",
			"The handler drops requests that are still queued, so it should be closed instead of destroyed.",
			"The eviction destroys a dispatcher that still has work, erroring the queued request.",
			// audit-6: the three natural paraphrases of this defect that the
			// first board missed, starting with the finding that motivated the
			// bug entry at all. They stay pinned so a shorter board cannot
			// regress to grading them false.
			"Calling result.dispatcher.destroy() here abruptly aborts in-flight requests instead of using close().",
			"Using destroy() instead of close() will error queued requests with ClientDestroyedError.",
			"Calling destroy() when active connections drop to zero cancels pending requests instead of draining them with close().",
			"Evicting on disconnect errors queued requests instead of draining them.",
			"The handler cancels queued requests as soon as the count reaches zero.",
		},
		fails: []string{
			"The lookup is keyed on the URL the event carries while the map stores the origin string, so the count never moves.",
			"One Client is retained per origin for the life of the process, which is unbounded growth of the client map.",
			"Bookkeeping that never fires leaves the client map growing forever.",
			"Rename result to entry so the shape is obvious.",
			"destroy() is called once in this block, which matches the close path below.",
			"Add a comment explaining why the eviction happens on disconnect.",
			// audit-6's neutral remarks: they name destroy(), the dispatcher and
			// the count, so a board that matched words instead of a direction
			// would swallow them.
			"Calling result.dispatcher.destroy() here cleans up the dispatcher once count reaches zero; consider adding a comment.",
			"Calling destroy() here when count <= 0 ensures the dispatcher is disposed after eviction.",
			"Note that result.dispatcher.destroy() tears down the dispatcher when count drops to zero.",
		},
	},
}

// TestCheckedInCorpusKeywordsGradeBothWays proves the shipped vocabulary both
// ways: every sentence written for the defect names it, and every sentence
// about a neighboring defect leaves it unnamed.
func TestCheckedInCorpusKeywordsGradeBothWays(t *testing.T) {
	for _, kc := range keywordCases {
		// The bug index is in the name because a fix commit can repair more
		// than one defect in a case, so the id alone does not name the pin.
		name := fmt.Sprintf("%s/bug%d", kc.id, kc.bug)
		t.Run(name, func(t *testing.T) { checkKeywordLists(t, kc) })
	}
}

// checkKeywordLists grades one case's two lists against its manifest entry.
func checkKeywordLists(t *testing.T, kc keywordCase) {
	t.Helper()
	requireBothLists(t, kc)
	bug := keywordBug(t, kc)
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
		if !namesDefect(text, bug) {
			t.Errorf("graded false, want true (sentence names the defect): %q", text)
		}
	}
}

func noneNamesDefect(t *testing.T, bug *core.CorpusBug, texts []string) {
	t.Helper()
	for _, text := range texts {
		if namesDefect(text, bug) {
			t.Errorf("graded true, want false (sentence names another defect): %q", text)
		}
	}
}

// keywordBug is the ground-truth bug a row pins: the checked-in case's bug at
// the row's index.
func keywordBug(t *testing.T, kc keywordCase) *core.CorpusBug {
	t.Helper()
	c, ref := loadCase(t, filepath.Join("..", "..", "..", "bench", "corpus", kc.id, "case.json"))
	if ref != nil {
		t.Fatalf("%s: %v", kc.id, ref)
	}
	if kc.bug < 0 || kc.bug >= len(c.Bugs) {
		t.Fatalf("%s: bug %d, want 0..%d (%d ground-truth bugs)",
			kc.id, kc.bug, len(c.Bugs)-1, len(c.Bugs))
	}
	return &c.Bugs[kc.bug]
}
