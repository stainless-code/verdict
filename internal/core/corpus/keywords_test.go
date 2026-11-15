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
			"Use this.get('set-cookie') instead of this['set-cookie'] so case-insensitive header lookup works.",
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
