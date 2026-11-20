package core

import (
	"reflect"
	"time"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/spend"
)

// RunRequest is the run service invocation: the read runtime (commander,
// clock, progress observer) and positionals, the agent name, the raw
// --timeout value (so bad-timeout stays a typed refusal at exit 1), the raw
// --rounds value (so an out-of-range round count stays a typed bad-flags
// refusal), the print-only mode, the operator's pass-through arguments, the
// ordered process-environ snapshot the child inherits, whether --json mode
// defaults NO_COLOR on for the child, and the PATH probe the adapter injects.
//
// LookPath is the one adapter-injected process fact besides the environment:
// resolution has to answer before the watchdog can spawn, and a binary that
// does not resolve is a typed refusal (agent-missing) rather than a spawn
// error. Nil falls back to the host's exec.LookPath.
//
// Recipe is the --recipe slug: a name looked up in the base recipe control,
// then in the built-in set. An empty name asks for the recipe named default, so
// even the implicit pipeline is a file and every run records what it declared.
//
// StageRaw is the --stage value: the raw operand, so a bad index stays a typed
// bad-flags refusal. It names one stage of the resolved plan to render instead
// of launching, which is why it is only meaningful with Print.
//
// Checkout is the working directory the agent process starts in, for callers
// whose review target is a historical tree (backtest replay) rather than the
// live one: an empty value keeps RepoRoot. It moves the child's cwd only —
// the bundle, its artifact, and every recorded path stay anchored to
// RepoRoot, and the directive names them absolutely so a relative path can
// never resolve inside the checkout.
type RunRequest struct { //nolint:govet // field grouping documents the request surface
	ReadRequest
	SpendPlan         []spend.Phase
	SpendPolicy       *spend.Policy
	ConsentOverBudget bool
	LookPath          func(string) (string, error)
	PassThrough       []string
	Environ           []string
	Agent             string
	Recipe            string
	StageRaw          string
	Checkout          string
	TimeoutRaw        string
	RoundsRaw         string
	// ProbeTimeout overrides the identity probe's test-owned budget; zero keeps
	// the shipped default.
	ProbeTimeout time.Duration
	Print        bool
	JSONMode     bool
	Warnings     []string
}

// RunResponse carries the run outcome: the envelope data, the warnings, the
// next step, and the launched process's wait code. Human lines stream live
// through the runtime progress observer, so the response carries no rendered
// lines. A response accompanies a refusal only to carry Exit:
//
//   - exit is zero on success and on every refusal that is not the agent's own
//     failure, so the adapter refuses with exit 1;
//   - a non-zero agent exit is both a typed refusal (agent-failed) and the
//     process exit code CI has to see, so the adapter preserves it.
type RunResponse struct { //nolint:govet // field grouping documents the response surface
	Repo     string
	Pr       string
	At       string
	Data     *RunData
	Warnings []string
	Next     string
	Exit     int
}

// RunData is the run verb's --json data profile: what was launched, from
// where, for how long, and what the artifact assertion proved. Field order is
// the published order. The print-only mode renders the same shape without the
// run facts (nothing spawned, nothing written), and there Argv is the exact
// command the operator asked for; a real run records the same argv with the
// directive replaced by a placeholder and secret operands redacted. Repair is
// set only when --rounds asked for more than one round; a one-shot run leaves
// it out so its envelope stays byte-identical to the single-round shape. Recipe
// is the resolved pipeline in the published `recipes.schema.json#/$defs/plan`
// shape, which is also launcher.json's `recipe` key. Stage is set only when
// --stage asked for one stage's command, and names which row of Recipe.Stages
// the rendered Argv covers.
type RunData struct { //nolint:govet // field order is the published property order
	Agent      string             `json:"agent"`
	Binary     string             `json:"binary"`
	Argv       []string           `json:"argv"`
	Bundle     string             `json:"bundle"`
	Artifact   string             `json:"artifact"`
	Recipe     *RecipePlan        `json:"recipe,omitempty"`
	Stage      *RecipeStageRender `json:"stage,omitempty"`
	StartedAt  string             `json:"started_at,omitempty"`
	EndedAt    string             `json:"ended_at,omitempty"`
	DurationMS int                `json:"duration_ms,omitempty"`
	Exit       int                `json:"exit"`
	TimedOut   bool               `json:"timed_out"`
	Timeout    int                `json:"timeout"`
	Fresh      bool               `json:"artifact_fresh"`
	Printed    bool               `json:"printed,omitempty"`
	Record     string             `json:"record,omitempty"`
	Repair     *RunRepair         `json:"repair,omitempty"`
}

// RunRepair is the repair-loop profile: the round cap, how many rounds it
// took to reach a valid review (0 when no round was valid), whether the last
// round was valid, and one entry per attempted round. Attempts carry hashes
// and problems, never raw review bytes: the bytes live in the bundle's
// .run.rounds/ files.
type RunRepair struct { //nolint:govet // field order is the published property order
	Rounds        int          `json:"rounds"`
	RoundsToValid int          `json:"rounds_to_valid"`
	FinalValid    bool         `json:"final_valid"`
	Attempts      []RunAttempt `json:"attempts"`
}

// RunAttempt is one launch-to-check round: the prompt hash the agent saw, the
// review hash it left, whether the checks passed, the checker's problems in
// full, the checker's overflow past its problem cap, the round's evidence
// summary, and its wall time in seconds.
type RunAttempt struct { //nolint:govet // field order is the published property order
	Round        int          `json:"round"`
	PromptSHA256 string       `json:"prompt_sha256"`
	ReviewSHA256 string       `json:"review_sha256"`
	Evidence     *RunEvidence `json:"evidence,omitempty"`
	Problems     []Problem    `json:"problems"`
	Truncated    int          `json:"truncated"`
	CheckOK      bool         `json:"check_ok"`
	WallS        float64      `json:"wall_s"`
}

// RunEvidence is one round's evidence verdict, the half of the round the shape
// checker cannot see: how many quotes, hunk receipts and spec items the bundle
// left uncovered, how many of those gaps were errors, which finding quotes this
// round settled as permanently unverified, and whether the receipt withholds
// approval.
type RunEvidence struct {
	// Settled names the finding quotes whose one repair round was spent, as
	// path:side:line. A settled quote is permanently unverified for this run:
	// no later round asks about it and, when the finding would gate, the run
	// ends in review-insufficient.
	Settled      []string `json:"settled,omitempty"`
	QuoteGaps    int      `json:"quote_gaps"`
	ReceiptGaps  int      `json:"receipt_gaps"`
	SpecGaps     int      `json:"spec_gaps"`
	Errors       int      `json:"errors"`
	Insufficient bool     `json:"insufficient"`
}

// Wire renders the data object in wire order.
func (d *RunData) Wire() *jq.Obj {
	return structObj(reflect.ValueOf(d).Elem())
}
