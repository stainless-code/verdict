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
