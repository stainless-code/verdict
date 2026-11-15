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
			"Python models add internal config keys when their config is accessed, so this validation raises a deprecation warning for keys the user never wrote.",
			"The validation cannot tell a python model's internal properties apart from user config, so every python model gets a spurious deprecation.",
			"The internal keys are not in the jsonschema, so validating them warns even though the user cannot set them.",
			"A false positive: the config keys a python model adds itself are reported as unknown custom keys.",
			"Every python model raises a deprecation warning for its own bookkeeping keys.",
			"This fires custom-key-in-config-deprecation for internal keys no user wrote.",
		},
		fails: []string{
			"The validation runs once per node, not once per config read.",
			"validate_config_call_dict defaults to False, so most callers skip validation.",
			"The post-hook and pre-hook keys are compared before the deprecation is raised.",
			"The config dict is copied instead of referenced, which costs an extra allocation.",
			"Python models need a separate parser entry point.",
			"The new test does not cover seeds or snapshots.",
			"Move the validation into the jsonschema module.",
		},
	},
	{
		id: "dbt-core-12386-column-dimension-expr",
		passes: []string{
			"When the dimension name differs from the column name, no expression is set, so MetricFlow queries a column that does not exist.",
			"The dimension name falls back to the column only when the dimension has no name; otherwise the expression is never set.",
			"A dimension whose name differs from its column gets no mapping, so the wrong identifier is queried.",
			"The reader builds the dimension with the friendly name and leaves the expression unset.",
			"MetricFlow resolves the dimension name as if it were a warehouse column, which is why validation reports an invalid identifier.",
			"The comment says expressions are unsupported for column-based dimensions, but the name can differ, so the generated SQL points at the dimension name instead of the column.",
		},
		fails: []string{
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
			"Adding a version to an existing model never schedules its referencing nodes for reparsing, so the downstream refs keep the stale parse.",
			"The condition only covers a changed latest version or a removed one, so a model that gains versioning is not reparsed.",
			"A model that becomes versioned leaves its dependents pointing at the old parse, and dbt fails with a node that is not in the graph.",
			"Because the gate reads the old node's flag, adding versioning does not reparse any referencing node.",
			"Downstream models keep resolving against the old version when a new version is added to the schema.",
			"Partial parsing misses the version-added case, so referencing nodes are never reparsed.",
		},
		fails: []string{
			"The unit-test scheduling block runs before the macro branch.",
			"delete_schema_mssa_links should return the removed node ids.",
			"The versioned test fixture uses a snake_case model name.",
			"Deleting a version from the schema does not remove its patch.",
			"The saved manifest copy is deep-copied on every file change, which is expensive.",
			"Group changes schedule referencing nodes even when the group is unchanged.",
			"The new test mocks the scheduler instead of asserting the parsed manifest.",
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
