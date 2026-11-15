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
	// The hono entries end each fails list with a counterexample from the
	// independent corpus audit (corpus-audit-1): a neutral or neighboring
	// sentence that a direction-free keyword used to grade as a hit.
	{
		id: "hono-4659-bearer-auth-scheme-regex-metacharacters",
		passes: []string{
			"Every metacharacter in the configured scheme is live, so a scheme containing a dot accepts a different scheme than the one configured.",
			"The scheme is interpreted as regex metacharacters: a prefix of X.Y accepts the header scheme XZY.",
			"An unmatched bracket in the scheme makes the pattern fail to compile, so the middleware throws while it is built.",
			"The option is interpolated into a regular expression, so X.Auth+v2 rejects its own literal scheme because + is a quantifier.",
			"Because the configured scheme is not escaped, a different scheme is accepted whenever the prefix contains a dot.",
			"A scheme holding an unmatched bracket cannot compile, so the service fails at startup.",
		},
		fails: []string{
			"The token comparison is not constant time, so the time it takes leaks how much of the value is right.",
			"The unauthorized reply carries no Cache-Control, so an intermediary may store the 401.",
			"verifyToken's resolved value is not awaited, so a rejected promise still counts as success.",
			"A request with no Authorization header is rejected with 400 instead of 401.",
			"The realm is written into the challenge without quoting, so a space breaks the header grammar.",
			"Note that options.prefix is used as a pattern with the 'i' flag; verify TOKEN_STRINGS is meant to be case-insensitive too.",
			"Comparing prefix as a literal string with startsWith would avoid allocating a RegExp.",
		},
	},
	{
		id: "hono-3936-pattern-router-wildcard-overmatch",
		passes: []string{
			"The compiled route matches a path it should not: /path/* answers /pathfoo because nothing requires the end of the path.",
			"With no boundary after the wildcard, the route accepts a path it should reject, such as /pathological.",
			"The trailing wildcard pattern matches a sibling path, so /paths reaches the handler registered for /path/*.",
			"This is a prefix overmatch: the suffix form /assets* answers /asset and /assetx as well.",
			"The pattern over-matches every path that starts with the registered prefix, including /pathfoo.",
			"Dropping the suffix means the pattern matches a longer path than the route declares.",
		},
		fails: []string{
			"Routes are matched in registration order, so a later specific route is shadowed by an earlier one.",
			"UnsupportedPathError is thrown after some routes were already stored, leaving the router half-registered.",
			"The catch block rethrows a generic error, so the caller cannot tell which path was rejected.",
			"Params are decoded twice, so a percent-encoded value is corrupted.",
			"The pattern is rebuilt on every lookup instead of once at registration.",
			"Consider adding a comment explaining why the end anchor is omitted when endsWithWildcard is true.",
			"Pushing routes unconditionally means duplicate routes could match the wrong handler.",
		},
	},
	{
		id: "hono-4171-etag-if-none-match-whitespace",
		passes: []string{
			"Whitespace before the comma is left on the entry, so a matching tag never equals the response tag and the request is answered 200 instead of 304.",
			"Entries are not trimmed, so a tag written with a trailing space is never recognized and the client gets the full body again.",
			"The split consumes only the right-hand side of the delimiter, so the entry left of the comma keeps its spaces.",
			"If-None-Match allows whitespace on one side of the delimiter as well as the other, but only the right-hand side is consumed.",
			"Spaces before the comma survive the split, so a tag the response carries is not recognized and the conditional request is answered in full.",
			"Every entry but the last keeps a trailing space, so a tag that is present in the list never matches.",
		},
		fails: []string{
			"The 304 response keeps the Content-Encoding header, so a cached client tries to decode the body twice.",
			"The generated tag is hashed from the uncompressed body, so it differs between gzip and identity responses.",
			"The stored 304 drops every header not on the retained list, including Vary.",
			"The tag is computed after the body is consumed, so a replayed request re-reads a disturbed stream.",
			"The middleware generates a tag for HEAD responses, where there is no body to hash.",
			"The regex `/,\\s*/` already handles optional whitespace after the comma; you could also trim the outer header.",
			"Splitting on comma breaks ETags containing a comma inside quotes (e.g. \"a,b\"), so it never equals and still returns 200.",
		},
	},
	{
		id: "hono-5133-clone-formdata-stale-content-length",
		passes: []string{
			"The clone keeps the original content length, so the declared length still describes the pre-clone body.",
			"Only the content type is dropped: the clone leaves the original content length in place, and it no longer matches the body that was re-serialized.",
			"The stale content length declares 77 bytes for a 169-byte multipart body.",
			"Because the length of the original body is copied over, the clone's declared length disagrees with what it sends.",
			"A request built from this clone would advertise the declared length of the original body and be cut short on the wire.",
			"The block removes the stale type but keeps the original content length, which the fresh boundary invalidates as well.",
		},
		fails: []string{
			"The FormData is re-serialized for every clone, so a large upload is buffered twice in memory.",
			"The clone copies the signal of the original request, so an aborted caller cancels the clone too.",
			"The cache key is chosen from the media type, so a request validated as JSON is re-read as text.",
			"The clone drops the duplex option, so a streamed body cannot be sent through fetch.",
			"Headers are copied without normalizing their case, so a lookup by name misses.",
			"Should content-length be preserved if the FormData boundary happens to be the same length?",
			"Good catch deleting the stale content-type; check whether content-length is also needed by the server.",
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
