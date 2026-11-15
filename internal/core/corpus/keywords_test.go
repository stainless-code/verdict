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
	// audit-2 found this row's vocabulary direction-free: "named help",
	// "called help", "user-defined" and "exemption tests" name a naming
	// decision, not the skipped check, so a test request or a note about the
	// built-in command graded as a defect report. The claims below say what
	// goes wrong; audit-2's three counterexamples are pinned false and its
	// first paraphrase is pinned true. Its second paraphrase ("use a dedicated
	// flag instead of comparing cmd.Name") names the fix's mechanism rather
	// than the defect, so it stays a known miss in the curation log. Two bare
	// nouns that came in with the first pass ("custom subcommand",
	// "required-flag validation") went the same way as the old phrases: a test
	// request and a note about where the validation lives matched them.
	{
		id: "urfave-cli-2319-required-flags-help-name",
		passes: []string{
			"The exemption is keyed on the command name, so a custom subcommand called help skips required-flag checks.",
			"Required-flag enforcement is matched by name, so a user command called help is exempted from its required flags.",
			"checkAllRequiredFlags returns nil for a custom subcommand whose --token is required, because it is the help command by name.",
			"Matching the help command by name lets a custom subcommand run without its required flag.",
			"The name comparison exempts every command called help from required-flag validation.",
			"A custom subcommand named help bypasses required-flag validation because the exemption matches its name.",
			"Required flags are never enforced on a command named help, because the exemption matches the name.",
			"The check is never enforced for a user command called help, so its action runs with zero values.",
			// audit-2's paraphrase of the same defect.
			"Any custom subcommand whose Name happens to be \"help\" skips required-flag validation.",
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
			// audit-2's counterexamples: a test request, a note about the
			// built-in command, and a question about the word "exemption".
			"Add a test for a subcommand named help that has a required flag.",
			"The built-in help command should still work for user-defined subcommands.",
			"Verify what this exemption tests when parent commands have required flags.",
			// The two bare nouns the first pass added, each in a neutral
			// sentence: a test request and a note about where the check lives.
			"Add a test for a custom subcommand named help.",
			"required-flag validation lives in checkAllRequiredFlags.",
		},
	},
	// audit-2 found "year zero" and "yields year 0" direction-free: they name
	// the value the defect is about, so a comment praising the new comparison
	// and a note about Go's parsing graded as defect reports. "explicit year
	// zero" went with them for the same reason, and the first pass's two
	// conditions ("layout carries a year", "layout specifies a year") went the
	// same way: a note about the layout and a question about what the check
	// should read matched them. Each claim below says the date is discarded;
	// audit-2's two counterexamples are pinned false, and its first paraphrase
	// is pinned true. Its second was a question, not a finding, so it moved to
	// the known misses in the curation log.
	{
		id: "urfave-cli-2450-year-zero-timestamp",
		passes: []string{
			"An explicit year zero is overwritten with today's date because the check tests the parsed value instead of the layout.",
			"A year-only layout that parses 0000 loses the year the caller wrote.",
			"The missing-year branch never asks the layout whether it carries a year, so 0000 reads as unset.",
			"A timestamp whose year parses to zero is silently replaced with the current date.",
			"The year-zero test keys on the parsed value alone, so a layout that spells out the year still has its date replaced.",
			"The flag discards an explicit year zero and the date is replaced with today.",
			"A year-only layout that parses 0000 is treated as unset, so the caller's year is replaced with today.",
			"Parsing \"0000\" with the 2006 layout succeeds and the date is silently rewritten to the current day.",
			"A literal year 0 in a year-carrying layout is treated as unset and its date is replaced with today.",
			"The branch drops the parsed year when layoutHasDate reports no date, so the caller's literal year never reaches the flag.",
			// audit-2's first paraphrase of the same defect.
			"A layout containing only a year like \"2006\" will have year 0000 overwritten with the current date.",
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
			// audit-2's counterexamples: praise for the comment beside the
			// comparison, and a neutral note about what Go's parser returns.
			"Nice comment explaining why year zero needs location-aware comparison.",
			"Go time.Parse yields year 0 when the layout omits the year component.",
			// The two conditions the first pass added, each in a neutral
			// sentence, and audit-2's second paraphrase, which is a question
			// about what the check should read rather than a defect report.
			"Note that the layout carries a year for the 2006 form.",
			"A layout that specifies a year is handled by layoutHasDate.",
			"Check whether the layout specifies a year instead of testing timestamp.Year() == 0.",
		},
	},
	// audit-2 found "early return", "empty positional", "boundary check",
	// "on the boundary", "argument boundary" and "empty-arg" direction-free:
	// they name the code the argument travels through, not what goes wrong, so
	// a praise sentence and two wrong-defect findings graded as hits. The first
	// pass added two more location fragments ("after the empty", "after the
	// double-dash") that a note about the trace or the append matched, and they
	// are gone too. Each claim below says an argument is consumed or lost;
	// audit-2's three counterexamples are pinned false and its first paraphrase
	// true. Its second paraphrase ("move the check to the top of the loop")
	// names where the fix goes rather than the defect, so it stays a known miss
	// in the curation log.
	{
		id: "urfave-cli-2163-stop-on-nth-arg-order",
		passes: []string{
			"The StopOnNthArg check sits below the empty-arg break and the -- branch, so those arguments are swallowed.",
			"The StopOnNthArg check runs too late: the -- branch returns before it.",
			"A `--` sitting exactly on the Nth positional is consumed by the `--` branch before the parser stops, so the action never sees it.",
			"The Nth-argument boundary is checked after the branches that consume the argument.",
			"An empty positional on the boundary ends flag parsing, so the rest of the arguments are lost.",
			"A `--` on the boundary reaches its branch first, so the args are lost.",
			"An argument that lands on the boundary is lost after the separator, so the action never receives it.",
			// audit-2's paraphrase of the same defect.
			"Checking StopOnNthArg after the empty-string and double-dash branches means those arguments bypass the limit.",
		},
		fails: []string{
			"The loop bound runs one past the slice.",
			"The io.Copy error is discarded, so a short write reads as success.",
			"The doc comment above parseFlags should name the new field.",
			"Rename posArgs to positionals for readability.",
			"A flag that needs an argument is reported only when it is last in a group.",
			"MutuallyExclusiveFlags are checked before dispatching to a subcommand.",
			"`StopOnNthArg` is a new field generated per parse; the count is tracked on the flag set.",
			// audit-2's counterexamples: praise for the early return, a finding
			// about the later empty-argument bug, and a wrong-defect comparison.
			"Good use of an early return here to avoid mutating the outer slice.",
			"The empty positional should be appended to posArgs instead of breaking out of the loop.",
			"The boundary check should use >= instead of == in case len(posArgs) overshoots.",
			// The two location fragments the first pass added, each in a
			// neutral sentence about the same line.
			"The trace runs after the empty-arg check, which is fine.",
			"Note that the append happens after the double-dash branch is handled.",
		},
	},
	// The fourth urfave row pins the branch that classifies a single-dash
	// argument by its first byte: a name that is not a letter is appended to
	// the positionals with everything after it, so a flag named "4" is never
	// applied and a non-ASCII name is misclassified because the byte is read
	// as a rune. Both halves are this one branch and two later fixes repair
	// them, so a sentence about either names the defect. The fails list is a
	// praise sentence for the branch's intent, a neutral note about the dash
	// branch above it, and neighboring defects in the same file.
	{
		id: "urfave-cli-2088-non-letter-flag-unparsed",
		passes: []string{
			"A numeric flag name is never applied: `-4 value` is appended to the positional args instead.",
			"The dash form of a numeric flag becomes a positional argument, so the value the caller passed never reaches the flag.",
			"A defined flag is misclassified as an argument because the first byte of its name is not a letter, so it is never looked up as a flag.",
			"A flag whose name starts with a digit can never be set, so a CLI that names a flag 4 takes its value as a positional.",
			"Only a name starting with a letter is ever treated as a flag, so the caller's value is lost and the flag keeps its default.",
			"The argument names a flag but is skipped, so the caller's value is lost and the flag keeps its default.",
			"A flag whose name is not a letter can never be set: the branch returns before the lookup runs.",
			"Calling unicode.IsLetter on rune(firstArg[1]) misclassifies a multi-byte name, so `-\u00fc` can never be set.",
		},
		fails: []string{
			"Good: an argument that is not a letter is kept out of flag parsing, so a negative number stays positional.",
			"The `--` branch sets shortOptionHandling to false before this else-if is reached.",
			"A `--` argument is never parsed as a flag, which is intended.",
			"Empty positional arguments are dropped instead of appended.",
			"firstArg[1] is indexed without checking the length, so a bare dash panics.",
			"The args accessor functions should be grouped by receiver.",
			"Add a test that a negative number stays positional.",
		},
	},
	{
		id: "dbt-core-8589-semantic-model-same-model",
		passes: []string{
			"same_model compares the model string to the bound method old.same_model, so the equality is always False.",
			"The right-hand side is the method object rather than the other node's model field.",
			"Every semantic model is reported as modified because the comparison is always false.",
			"The field is compared to a bound method, so the two nodes never compare equal.",
			"same_model can never succeed: the second operand is the method, not old.model.",
			"Every run reports a change, because same_model never returns true.",
		},
		fails: []string{
			"same_contents does not compare the node's metadata or tags.",
			"The defaults are compared by identity, so two equal dicts read as different.",
			"Rename same_model to same_model_field so the intent is clear.",
			"The new selector method filters by package instead of by resource type.",
			"same_group compares groups before the primary entity is resolved.",
			"The list task drops the semantic model's version from its output.",
			"Semantic models reach graph selection but their tests do not.",
		},
	},
	{
		id: "dbt-core-11768-python-model-config-warning",
		passes: []string{
			"Python models record config keys when they call dbt.config.get, so this validation raises a spurious deprecation for keys the user never wrote.",
			"The validation cannot tell the python model's own bookkeeping properties apart from user config, so a spurious deprecation is raised for the key that was read.",
			"The keys recorded by dbt.config.get are not in the jsonschema, so validating them raises a false deprecation.",
			"It validates keys the user never wrote, because a dbt.config.get call puts them in _config_call_dict.",
			"The keys a python model read are validated as user config, and the validator warns for keys the model read.",
			"A false deprecation is raised for keys the python model added itself.",
			"With DBT_ENV_PRIVATE_RUN_JSONSCHEMA_VALIDATIONS set, the validator warns for keys the model read with dbt.config.get, even though the model wrote no config.",
		},
		// Neutral or praising sentences at the same line, then findings about
		// neighboring defects in the same file: none may name this one.
		fails: []string{
			"Check whether validate_model_config already ignores keys the user never wrote in _config_call_dict.",
			"The change means dbt warns on every python model, even one that never reads config.",
			"Document the python model's own config keys in the README.",
			"Bookkeeping keys are stored in _config_call_dict.",
			"Ensure internal keys are stripped before serializing the manifest.",
			"This validation should raise an error rather than a deprecation warning.",
			"The validation runs once per node, not once per config read.",
			"validate_config_call_dict defaults to False, so most callers skip validation.",
			"The post-hook and pre-hook keys are compared before the deprecation is raised.",
			"The config dict is copied instead of referenced, which costs an extra allocation.",
			"Python models need a separate parser entry point.",
			"Move the validation into the jsonschema module.",
		},
	},
	{
		id: "dbt-core-12386-column-dimension-expr",
		passes: []string{
			"When the dimension name differs from the column name, no expression is set, so MetricFlow queries a column that does not exist.",
			"The dimension name falls back to the column only when the dimension has no name; otherwise the expression is never set.",
			"A dimension whose name differs from its column queries a column that does not exist.",
			"The reader builds the dimension with the friendly name and leaves the expression unset.",
			"MetricFlow resolves the dimension name instead of the column, so the query fails because the expression is missing.",
			"The comment says expressions are unsupported for column-based dimensions, but the name can differ, so the generated SQL points at the dimension name instead of the column.",
			"The dimension is built without expr, so the warehouse column is never read.",
			"A column-based dimension named differently from its column reads the wrong column.",
			"When the dimension name differs from the column name, expr is not set, so MetricFlow uses the dimension name as the SQL identifier.",
		},
		// Neutral or praising sentences at the same line, then findings about
		// neighboring defects in the same file: none may name this one.
		fails: []string{
			"Good that expr is left unset here per the comment on column-based dimensions.",
			"The missing expression is acceptable here; dimension names should be used as column names.",
			"Good: this now sets expr for column-based dimensions.",
			"Consider a no mapping fallback log for unknown dimension types.",
			"Good that no expression is needed for column-based dimensions per the comment.",
			"Check whether column.dimension.label also differs from the column name.",
			"If DimensionType is invalid, MetricFlow will report an invalid identifier.",
			"The metrics parsing path does not handle the filter block.",
			"Entities are derived only from derived_semantics, never from columns.",
			"The new method skips columns without a dimension block, which is correct.",
			"Unlike dimensions, the entity branch keeps the fallback name in the wrong order.",
			"Column-level dimensions lose their config meta merge when the dimension block has no meta.",
			"The v2 reader should validate type before constructing the dimension.",
			"Dimensions defined at the model level still use the old parser.",
		},
	},
	{
		id: "dbt-core-9421-partial-parse-versioning",
		passes: []string{
			"Adding a version to an existing model never schedules its referencing nodes for reparsing, so the dependents keep the parse they already had.",
			"The gate reads the old node's flag, so it does not reparse after a version is added and the dependents stay stale.",
			"Downstream models are never reparsed when a new version is added to the schema, so they keep the old parse.",
			"Partial parsing misses the version-added case, so referencing nodes are never reparsed.",
			"Dependents of a model that gains its first version are not scheduled for reparsing, so each one keeps the old parse.",
			"A model that becomes versioned does not reparse after a version is added, so its referencing nodes keep the old parse.",
		},
		// Neutral or praising sentences at the same line, then findings about
		// neighboring defects in the same file: none may name this one.
		fails: []string{
			"This gate does not reparse dependents when an unversioned model remains unversioned, avoiding unnecessary work.",
			"When a model is unversioned, referencing nodes are not reparsed, which avoids unnecessary work.",
			"Newly versioned models are handled by the versions loop below.",
			"Nice that a model that becomes versioned keeps its alias.",
			"Good that this comment clarifies adding a version vs removing one.",
			"Removing a version leaves downstream refs with a stale parse if node_patches is not cleared.",
			"The unit-test scheduling block runs before the macro branch.",
			"delete_schema_mssa_links should return the removed node ids.",
			"The versioned test fixture uses a snake_case model name.",
			"Deleting a version from the schema does not remove its patch.",
			"The saved manifest copy is deep-copied on every file change, which is expensive.",
			"Group changes schedule referencing nodes even when the group is unchanged.",
			"The new test mocks the scheduler instead of asserting the parsed manifest.",
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
			"Go evaluates cmd() before starting the goroutine, so batch commands execute sequentially on the event loop and a slow command freezes rendering.",
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
	{
		id: "axios-5707-getsetcookie-caseless-key",
		passes: []string{
			"The accessor bypasses the caseless accessor and reads one fixed key, so a header set as Set-Cookie comes back empty.",
			"AxiosHeaders stores a header under the name the caller passed, and this method misses a header stored under its original casing.",
			"getSetCookie silently drops the values for a mixed-case header name instead of going through the case-insensitive getter.",
			"A header the caller set is never returned: the header is not found when its name is not lower case.",
			"The method returns nothing for a header the caller already set a line earlier.",
			"It silently loses the values when the name is not lower case, because the key is hard-coded.",
			"Direct property lookup this[\"set-cookie\"] is case-sensitive, so Set-Cookie headers stored with mixed casing return [].",
			"The accessor misses the stored cookie when the name was set as Set-Cookie.",
		},
		fails: []string{
			"The parser splits a header line on every colon, so a value containing a colon is truncated.",
			"toString drops headers whose value is false instead of rendering them.",
			"set throws on a header name that contains a space instead of normalizing it.",
			"Header names are camel-cased for the accessors, so DELETE is spelled Delete.",
			"clear() leaves the accessor methods bound to the previous instance.",
			"Reading the property directly avoids the accessor machinery, which looks like a reasonable micro-optimization.",
			"The accessor is declared in the type file, so TypeScript users can call it.",
			"Reading this['set-cookie'] directly avoids the overhead of calling this.get().",
			"getSetCookie returns a mutable reference to the internal array, so later set calls mutate it.",
			"getSetCookie should return undefined instead of an empty array when the header is absent.",
			"Prefer this.get('set-cookie') instead of this['set-cookie'] for consistency with the other accessors.",
		},
	},
	{
		id: "axios-6192-absolute-url-flag-precedence",
		passes: []string{
			"The false flag bypasses the guard that requires a base, so the joiner is handed undefined and throws.",
			"Operator precedence drops the base check: allowAbsoluteUrls == false alone makes the whole condition true.",
			"It throws when the flag is off and no base is set, instead of returning the requested URL.",
			"The call crashes joining a URL onto an undefined base.",
			"It fails to build the full path when the base is missing.",
			"The condition combines a URL with a missing base instead of returning it unchanged.",
			"Missing parentheses around isRelativeUrl || allowAbsoluteUrls == false: when allowAbsoluteUrls is false and baseURL is undefined, combineURLs(undefined, requestedURL) throws a TypeError.",
			"Operator precedence causes && to bind tighter than ||, so allowAbsoluteUrls == false invokes combineURLs even when baseURL is undefined.",
		},
		fails: []string{
			"The flag defaults to false in the instance defaults, so every request stops honoring absolute URLs.",
			"getUri ignores the config argument and always merges the instance defaults.",
			"The README documents the option but the type declaration does not add it.",
			"The new option is validated as a string rather than a boolean.",
			"The combined URL keeps a duplicate slash when the base ends in one.",
			"The new flag is copied from the instance defaults when the request config leaves it undefined.",
			"Taking the absolute-URL check into a local makes the condition easier to read.",
			"Checking allowAbsoluteUrls == false in the same conditional keeps buildFullPath concise.",
			"Use strict equality === false so a caller passing 0 does not turn the flag on.",
			"Using && instead of || in the condition would make the base check redundant.",
		},
	},
	{
		id: "axios-6873-iterated-headers-overwrite",
		passes: []string{
			"Each iteration overwrites the value stored before, so a Headers with three set-cookie values keeps one.",
			"Duplicate values from a repeated key are dropped because every pair goes through the setter on its own.",
			"Only the last value of a repeated header survives when the source is an iterable.",
			"Multiple values collapse into one instead of forming the array the source provided.",
			"Repeated values are overwritten instead of accumulated, so a caller loses every earlier entry.",
			"Repeated pairs lose every value but the last when the source is a Map or a Headers.",
			"Calling setHeader(entry[1], entry[0], rewrite) on each entry overwrites earlier values for the same header name instead of preserving them as an array.",
			"Iterating a Headers object with multiple set-cookie entries will clobber previous cookies because setHeader is called per pair.",
		},
		fails: []string{
			"The iterable branch throws a TypeError for an entry that is not an array, so a Map-like source is rejected.",
			"isIterable is added to the utils export but not to the type declaration.",
			"The branch now accepts any iterable, so a string source reaches the pair loop instead of the header parser.",
			"The plain-object check drops the iterator exclusion, so an object with a custom iterator is treated as plain.",
			"The pair loop reads the key and value in the wrong order, so every header name becomes a value.",
			"The branch accepts any object with a Symbol.iterator, which is broader than the old Headers check.",
			"Throwing a TypeError for a non-array entry is a clear contract for custom sources.",
			"Calling setHeader inside the for-of loop reuses the existing single-header validation path.",
			"entry[1] and entry[0] are swapped, so every header name becomes a value.",
			"Iterating an object without checking entry.length === 2 accepts malformed pairs.",
		},
	},
	{
		id: "axios-11087-nullish-interceptor-handlers",
		passes: []string{
			"A falsy public list makes every request throw Cannot read properties of null (reading 'length').",
			"Every request crashes once the public array is falsy, because the sync pass runs before any existence check.",
			"User code that nulls the public array crashes the next request instead of behaving as a no-op.",
			"The sync pass crashes when the public collection is replaced with a falsy value.",
			"The new pass does not tolerate a caller-assigned falsy collection, though clear() has always allowed it.",
			"It rejects a replaced collection instead of treating it as empty.",
			"syncHandlerEntries reads handlers.length without checking if handlers is null or undefined, causing a TypeError when user code sets interceptors.request.handlers = null.",
			"If manager.handlers is set to null, handlers.length throws a TypeError in syncHandlerEntries; guard against nullish handlers.",
			"The sync pass crashes on null handlers, so a caller that nulls the public field breaks every request.",
		},
		fails: []string{
			"Ejecting a handler shifts the remaining indexes, so a later eject removes the wrong interceptor.",
			"The next id is never reused after clear(), so ids grow without bound.",
			"trimHandlers removes interior tombstones too, so forEach skips a live handler.",
			"The iteration depth is decremented twice when a handler throws.",
			"Numeric-string ids are no longer accepted by eject after the move to a Map.",
			"forEach no longer visits handlers added during iteration.",
			"The bookkeeping map is cleared when the handler list reference changes, which keeps the id table in sync.",
			"Trimming trailing tombstones after an eject keeps the array compact.",
			"Storing handlers.length in internals.handlersLength lets syncHandlerEntries detect in-place array mutations.",
			"handlers.length is read inside syncHandlerEntries on every call instead of caching it on the instance.",
			"Setting handlersLength here will cause an off-by-one error when a handler is ejected.",
			"We already guard against nullish handlers upstream, so this check is redundant.",
		},
	},
	// The hono entries pin two independent audits in both directions: the last
	// rows of each fails list are a neutral or praising sentence the earlier
	// vocabularies graded as a hit, and each passes list carries a paraphrased
	// finding a widened phrase now covers.
	{
		id: "hono-4659-bearer-auth-scheme-regex-metacharacters",
		passes: []string{
			"Every metacharacter in the configured scheme is live, so a scheme containing a dot accepts a different scheme than the one configured.",
			"The scheme is interpreted as regex metacharacters: a prefix of X.Y accepts the header scheme XZY.",
			"An unmatched bracket in the scheme makes the pattern fail to compile, so the middleware throws while it is built.",
			"The scheme is concatenated into the pattern without escaping, so X.Auth+v2 rejects its own literal scheme because + is a quantifier.",
			"Because the configured scheme is not escaped, a different scheme is accepted whenever the prefix contains a dot.",
			"A scheme holding an unmatched bracket cannot compile, so the service fails at startup.",
			"An unescaped scheme lets a dot in the prefix match any character, so a different scheme is accepted.",
			"Passing options.prefix directly into new RegExp(...) without escaping regex special characters allows characters like . or + in the scheme to act as wildcards or throw a SyntaxError.",
		},
		fails: []string{
			"The token comparison is not constant time, so the time it takes leaks how much of the value is right.",
			"The unauthorized reply carries no Cache-Control, so an intermediary may store the 401.",
			"verifyToken's resolved value is not awaited, so a rejected promise still counts as success.",
			"A request with no Authorization header is rejected with 400 instead of 401.",
			"The realm is written into the challenge without quoting, so a space breaks the header grammar.",
			"Note that options.prefix is used as a pattern with the 'i' flag; verify TOKEN_STRINGS is meant to be case-insensitive too.",
			"Comparing prefix as a literal string with startsWith would avoid allocating a RegExp.",
			"In bearerAuth, options.prefix is interpolated into a regular expression with the 'i' flag for case-insensitive matching.",
		},
	},
	{
		id: "hono-3936-pattern-router-wildcard-overmatch",
		passes: []string{
			"The compiled route matches a path it should not: /path/* answers /pathfoo because nothing requires the end of the path.",
			"With the suffix dropped, the route accepts a path it should reject, such as /pathological.",
			"The trailing wildcard pattern matches a sibling path, so /paths reaches the handler registered for /path/*.",
			"This is a prefix overmatch: the suffix form /assets* answers /asset and /assetx as well.",
			"The pattern over-matches every path that starts with the registered prefix, including /pathfoo.",
			"The trailing wildcard omits the end anchor, so the route matches paths beyond the prefix: /path/* answers /pathfoo.",
			"A wildcard route lacks a segment boundary, so a sibling path like /paths is routed to the wildcard handler.",
			"Because endsWithWildcard omits the end anchor (/?$), a route like /api/* will also match /apiv2 instead of requiring a slash boundary.",
		},
		fails: []string{
			"Routes are matched in registration order, so a later specific route is shadowed by an earlier one.",
			"UnsupportedPathError is thrown after some routes were already stored, leaving the router half-registered.",
			"The catch block rethrows a generic error, so the caller cannot tell which path was rejected.",
			"Params are decoded twice, so a percent-encoded value is corrupted.",
			"The pattern is rebuilt on every lookup instead of once at registration.",
			"Consider adding a comment explaining why the end anchor is omitted when endsWithWildcard is true.",
			"Pushing routes unconditionally means duplicate routes could match the wrong handler.",
			"When endsWithWildcard is true, omitting /?$ ensures the route matches a longer path such as /path/a/b.",
			"Using an empty suffix leaves no boundary after the wildcard, which is intentional for suffix matching like /assets*.",
		},
	},
	{
		id: "hono-4171-etag-if-none-match-whitespace",
		passes: []string{
			"Whitespace before the comma is left on the entry, so a matching tag never equals the response tag and the request is answered 200 instead of 304.",
			"Entries are not trimmed, so a tag written with a trailing space is never recognized and the client gets the full body again.",
			"Splitting on the comma leaves trailing whitespace before the comma on every tag but the last, so the entry never matches.",
			"If-None-Match allows whitespace before the comma as well as after it, but the split consumes only the right-hand side.",
			"Spaces before the comma survive the split, so a tag the response carries is not recognized and the conditional request is answered in full.",
			"Every entry but the last keeps a trailing space, so a tag that is present in the list never matches.",
			"Whitespace is not stripped from the tag that precedes the comma, so that tag never matches the response.",
		},
		fails: []string{
			"The 304 response keeps the Content-Encoding header, so a cached client tries to decode the body twice.",
			"The generated tag is hashed from the uncompressed body, so it differs between gzip and identity responses.",
			"The stored 304 drops every header not on the retained list, including Vary.",
			"The tag is computed after the body is consumed, so a replayed request re-reads a disturbed stream.",
			"The middleware generates a tag for HEAD responses, where there is no body to hash.",
			"The regex `/,\\s*/` already handles optional whitespace after the comma; you could also trim the outer header.",
			"Splitting on comma breaks ETags containing a comma inside quotes (e.g. \"a,b\"), so it never equals and still returns 200.",
			"Any characters to the left of the comma are passed directly into stripWeak.",
			"The regex /,\\s*/ consumes whitespace on one side of the delimiter, which is fine for standard headers.",
		},
	},
	{
		id: "hono-5133-clone-formdata-stale-content-length",
		passes: []string{
			"The clone keeps the original content length, so the declared header still describes the pre-clone body.",
			"Only the content type is dropped: the clone leaves the original content length in place, and the header disagrees with the body it re-serialized.",
			"The stale content length declares 77 bytes for a 169-byte multipart body.",
			"The clone keeps the stale length from the original request, so the header is wrong for the re-serialized body.",
			"A request built from this clone would advertise a wrong content-length and be cut short on the wire.",
			"The block removes the stale type but keeps the original content length, which the fresh boundary invalidates as well.",
			"The stale content-length survives the clone, so the request declares more bytes than the re-serialized body carries.",
		},
		fails: []string{
			"The FormData is re-serialized for every clone, so a large upload is buffered twice in memory.",
			"The clone copies the signal of the original request, so an aborted caller cancels the clone too.",
			"The cache key is chosen from the media type, so a request validated as JSON is re-read as text.",
			"The clone drops the duplex option, so a streamed body cannot be sent through fetch.",
			"Headers are copied without normalizing their case, so a lookup by name misses.",
			"Should content-length be preserved if the FormData boundary happens to be the same length?",
			"Good catch deleting the stale content-type; check whether content-length is also needed by the server.",
			"Deleting content-type lets Request regenerate the boundary; the declared length header is left untouched.",
			"The cloned request preserves the content-length of the original body from req.header().",
			"Good catch deleting content-type since the original boundary no longer matches the body.",
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
	{
		// cli/cli #9934 made the extension update check blocking; the later
		// fix (bc8c46b0b, Fixes #10235) restored the non-blocking select.
		id: "cli-cli-9934-extension-update-blocks",
		passes: []string{
			"The receive is now unconditional, so a slow check will stall the CLI after the extension has already run.",
			"Every extension invocation waits for the update check before the process ends.",
			"The command hangs on the update check instead of running to completion.",
			"A slow network delays the extension's exit because the channel is received unconditionally.",
			"A slow lookup holds the command open after the extension has already run.",
			"The update check is waited for instead of skipped once the command finishes.",
			// audit-10's fresh paraphrase: it says the command stalls, not that
			// a receive blocks, so the short phrase is what has to catch it.
			"Removing the select with time.After makes PostRun wait indefinitely on updateMessageChan, so a slow or stalled network check will stall the CLI after the extension exits.",
			"Because updateMessageChan is now received unconditionally without a timeout or default branch, every extension command blocks until the background release lookup completes.",
		},
		fails: []string{
			"The update notice is fetched on every invocation instead of being cached for a day.",
			"The debug flag is ignored, so a failed lookup is silent.",
			"The notice is printed to stdout instead of stderr.",
			"The pinned extension hint uses the wrong upgrade flag.",
			"The channel is unbuffered, so the goroutine leaks when the command exits early.",
			"The release lookup runs in PreRun, so it repeats the network call the extension already made.",
			// audit-10's neutral remark: it describes the receive without
			// claiming the wait is wrong, so it must stay ungraded.
			"Note that the receive blocks until the update check finishes; verify that PreRun's goroutine always sends on updateMessageChan.",
			"PreRun launches the lookup in a goroutine and PostRun prints the notice to stderr when a newer version exists.",
		},
	},
	{
		// cli/cli #10384 double-encoded a package name the caller had already
		// escaped; PR #10630 reverted it and names issue #10592.
		id: "cli-cli-10384-double-escaped-package",
		passes: []string{
			"The helper escapes the captured name as if it were raw text, so an already escaped name is escaped again: %2F becomes %252F.",
			"The path rewrite double-encodes the package name, so the packages endpoint answers 404 for it.",
			"The percent sign of a pre-escaped name is encoded twice, so the request asks the server for %252F.",
			"An already escaped name no longer resolves because its escape sequence gains another layer.",
			"Callers that already encoded the name break: the command worked before this change and now the endpoint 404s.",
			"The helper re-encodes the caller's escape sequence instead of escaping only an unescaped name.",
			// audit-10's fresh paraphrases, repointed: the pin states their claim
			// in encoding terms, because a bare "the path is corrupted" no longer
			// counts (CodeRabbit on PR #151). The originals are pinned in fails.
			"If the caller passes a URL-encoded package name such as ghcr.io%2Fuser%2Fpkg, url.QueryEscape double-encodes the percent sign into %25.",
			"Calling url.QueryEscape unconditionally on packageName double-encodes paths that are already percent-encoded, causing 404 responses.",
		},
		fails: []string{
			"The regexp is compiled on every call instead of once at package level, which costs a compile per request.",
			"The capture group is lazy, so a name that ends with the word versions stops the match early.",
			"The helper should return an error instead of the original path when the pattern does not match.",
			"The new net/url import is used only by this helper and could be scoped to the function.",
			"The helper handles the container type but not the npm or nuget package types.",
			"The added tests only cover paths that need escaping, not paths that are already valid.",
			// audit-10's neutral remarks: they approve the replacement or the
			// no-match path and must not be read as the corruption claim.
			"Good that strings.Replace uses n=1 so only the captured package segment is substituted.",
			"The helper returns path unchanged when pathWithPackageNameRE does not match.",
			// A bare path-corruption claim is not this defect, wherever it comes
			// from (CodeRabbit on PR #151).
			"This path is corrupted when the base URL has a trailing slash.",
			// audit-10's paraphrases verbatim. They name the corrupted path and
			// not the encoding claim, so they are known misses after the
			// narrowing; pinning them as false keeps it from quietly reversing.
			"If the caller passes a URL-encoded package name such as ghcr.io%2Fuser%2Fpkg, url.QueryEscape will escape the % characters into %25, corrupting the path.",
			"Calling url.QueryEscape unconditionally on packageName corrupts paths that are already percent-encoded (turning %2F into %252F), causing 404 responses.",
		},
	},
	{
		// cli/cli #11172 rewrote the log path and left its early return above
		// the exit-status check; the flag was already ignored at the base, so
		// the provenance note discloses a pre-existing defect on a rewritten
		// line. Fix 24964681a, Fixes #12674.
		id: "cli-cli-11172-exit-status-ignored-with-log",
		passes: []string{
			"With --log a failed run still exits zero, so CI marks the job green.",
			"The failure is never reported to the shell because the log path returns first.",
			"exit-status has no effect with logs: the flag is parsed and then dropped.",
			"Scripts cannot detect a failed run when --log is also passed.",
			"The log branch returns without setting the failure exit, so `echo $?` reports success.",
			"The command exits successfully for a failed run when logs are printed.",
			// audit-10's fresh paraphrases: they name the flag and the skipped
			// check, not the exit code the user sees.
			"Returning displayLogSegments(...) here bypasses the --exit-status check later in runView, so gh run view --log --exit-status will exit 0 even when the workflow run failed.",
			// audit-10's second paraphrase, repointed to name the flag: "ignores
			// the flag" alone also credits a comment about --log-failed
			// (CodeRabbit on PR #151). The original is pinned in fails.
			"When --log or --log-failed is passed alongside --exit-status, this early return skips the opts.ExitStatus failure check below and ignores --exit-status.",
		},
		fails: []string{
			"The fallback fetches job logs one request at a time, so a wide matrix is slow.",
			"The 25-request cap should be a named constant rather than a literal.",
			"The zip log map drops files whose names the server truncated differently.",
			"The progress indicator is stopped twice on the error path.",
			"The new helper closes the response body only after checking the status code.",
			"Skipped jobs are still requested from the logs endpoint, which wastes a request.",
			// audit-10's neutral remarks: they describe the branch and its
			// cleanup without claiming the return drops the flag.
			"Returning displayLogSegments directly keeps the log-printing branch separate from the summary table below.",
			"Deferring runLogZip.Close() ensures the temporary zip reader is cleaned up before displayLogSegments runs.",
			// A flag other than --exit-status being ignored is a different
			// defect, so this must not be credited as this bug.
			"`--log-failed` ignores the flag when no step failed.",
			// audit-10's paraphrase verbatim: "ignores the flag" without naming
			// the flag is a known miss after the narrowing, pinned false so the
			// narrowing cannot quietly reverse.
			"When --log or --log-failed is passed alongside --exit-status, this early return skips the opts.ExitStatus failure check below and ignores the flag.",
		},
	},
	{
		// cli/cli #12596 returned the reformatted scope error before callers
		// could treat it as ignorable; PR #12915 reverted it (issue #12904).
		id: "cli-cli-12596-scope-error-hard-fail",
		passes: []string{
			"The new interception fails the command instead of degrading, so a partial response is discarded.",
			"Replacing the server's message turns an ignorable scope failure into a fatal error for every caller.",
			"Because the error is swapped before the caller inspects it, the caller never sees the partial result.",
			"Graceful degradation is lost: gh pr view prints the scope error and shows nothing.",
			"A missing project scope aborts the whole command even though the rest of the query succeeded.",
			"A response that also carries usable data is thrown away when one field needs read:project.",
			// audit-10's fresh paraphrases: they name the broken predicate and the
			// needless failure, not the partial result.
			"Returning scopeErr here replaces GraphQLError with a plain formatted error, breaking ProjectsV2IgnorableError checks in callers like gh pr view when read:project is missing.",
			"Because GenerateScopeErrorForGQL strips the original server error message and GraphQLError wrapper, callers can no longer treat missing read:project scopes as non-fatal and will error out unnecessarily.",
		},
		fails: []string{
			"The regular expression is not anchored, so a message with two scope lists parses the wrong one.",
			"The helper does not trim the trailing comma from the joined scope list.",
			"The set package is imported in the wrong group for this file.",
			"The error message hardcodes the refresh command instead of reusing the shared helper.",
			"The message is printed to stdout instead of stderr, which breaks scripted callers.",
			"The scope list is sorted, so the suggestions lose the server's order.",
			// audit-10's neutral remarks: they approve the unwrapping and the
			// fallback, which must not be read as the fatal-error claim.
			"Good that GenerateScopeErrorForGQL uses errors.As to unwrap wrapped GraphQLError values before formatting the scope hint.",
			"Falling back to GraphQLError when scopeErr is nil preserves existing handling for non-scope GraphQL errors.",
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
