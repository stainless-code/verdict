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
	// The bubbletea rows are the sentences the curation probe measured, plus the
	// shape a finding at the same line takes when it says nothing about the
	// defect at all: a tracing request (the review that prompted these rows was
	// "Add tracing for commands handed to the command handler"), a note about
	// the plumbing, or a suggestion. None of them may grade as a defect report,
	// which is why no keyword names a location or a mechanism on its own.
	{
		id: "bubbletea-1255-signal-send-shutdown-hang",
		passes: []string{
			"The handler blocks forever because the interrupt is written with no receiver left.",
			"Once the event loop has stopped, the handler is stuck in the send and the program never exits.",
			"shutdown hangs waiting for the handler, so shutdown never completes.",
			"This send has no context guard, unlike Program.Send, so it hangs the program on exit.",
			"A signal arriving during shutdown leaves the handler blocked and the program hung on exit.",
			"This is a deadlock.",
		},
		fails: []string{
			"InterruptMsg is returned before the final view is rendered, so the last frame is missing.",
			"The signal handler is registered twice, so two notifiers race for the same signal.",
			"ignoreSignals is read with a plain load, so a concurrent write is lost.",
			"ErrInterrupted is not wrapped in ErrProgramKilled, so errors.Is fails for callers.",
			"The handler stops listening after the first signal, so a second interrupt is ignored.",
			"The handler writes the interrupt message to the program's message channel.",
			"Add a log line naming which signal the handler received.",
			"Note that shutdown waits for the handler goroutine to finish; consider buffering the sig channel.",
		},
	},
	{
		id: "bubbletea-1276-exec-renderer-reset",
		passes: []string{
			"The line count no longer matches the screen, so the next frame is painted at the wrong row.",
			"This throws away the renderer's state while the subprocess output is still on screen.",
			"The cursor ends up in the wrong place after the command, and the view is duplicated.",
			"Zeroing the count while the lines are still on screen duplicates the view below the subprocess output.",
			"The program's own output is drawn below the child's, so the previous frame is never overwritten.",
			"This is a line count desync.",
		},
		fails: []string{
			"The subprocess inherits stderr instead of the program output, so the child errors are lost.",
			"The callback runs in a goroutine, so a message can arrive after the program stopped.",
			"ReleaseTerminal is called on the error path as well, so the terminal is restored twice.",
			"The exec callback error is discarded, so a failing command looks successful.",
			"The command stdin is never connected, so an interactive program sees EOF.",
			"The subprocess runs before the renderer is reset for the next frame.",
			"Add tracing around the exec callback to see when the subprocess finishes.",
		},
	},
	{
		id: "bubbletea-1463-batch-executed-in-event-loop",
		passes: []string{
			"This runs the commands in the event loop, so a batch no longer runs concurrently.",
			"Each command is executed one after another, blocking the event loop while it runs.",
			"A batch command that waits for a sibling that cannot start deadlocks instead of running them together.",
			"The event loop stalls while the command runs, so updates and rendering stop.",
			"Executing them one at a time serializes what the batch should start at once.",
			"This is a serialized batch.",
		},
		fails: []string{
			"The nested batch in a sequence is waited for with a WaitGroup, so the sequence blocks forever.",
			"The command value is evaluated before the nil check, so a nil command panics.",
			"The send happens from a goroutine, so messages can be delivered out of order.",
			"The program context is not checked, so a canceled program still runs the queued commands.",
			"The batch message is not compacted, so nil commands are still passed to the channel.",
			"Add tracing for commands handed to the command handler.",
			"This loop hands each non-nil batch command to p.Send in a new goroutine.",
			"Add a comment explaining that the commands in a batch are run one at a time.",
			"Commands handed to the command handler should be wrapped with panic recovery.",
		},
	},
	{
		id: "bubbletea-848-window-title-msg-unhandled",
		passes: []string{
			"The message switch in eventLoop has no case for the window title message, so a SetWindowTitle command never reaches the renderer.",
			"A window title command now falls through to the user's Update, so the title is never written.",
			"The resize check never runs because the window size case was dropped from the switch.",
			"The switch lost its internal cases, so internal messages are no longer handled.",
			"The terminal title never changes: the title command is silently dropped.",
			"This is an unhandled internal message.",
		},
		fails: []string{
			"The sequence goroutine recovers from a panic with recover, so a panic in a nested sequence is swallowed.",
			"The batch handler waits on a WaitGroup whose counter is never incremented.",
			"Commands in a sequence are executed one at a time, so a slow command delays the next one.",
			"The message filter runs before the internal switch, so a filtered QuitMsg is never handled.",
			"eventLoop returns the model on ctx.Done without draining the command channel, so a queued command is lost.",
			"The renderer's handleMessages is called after the switch, so a clear-screen message repaints twice.",
			"This loop reads each message from the program's message channel and dispatches it.",
			"Add tracing around the internal message switch so missing cases are easier to spot.",
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
