package core

import (
	"reflect"

	"github.com/stainless-code/verdict/internal/jq"
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
// Checkout is the working directory the agent process starts in, for callers
// whose review target is a historical tree (backtest replay) rather than the
// live one: an empty value keeps RepoRoot. It moves the child's cwd only —
// the bundle, its artifact, and every recorded path stay anchored to
// RepoRoot, and the directive names them absolutely so a relative path can
// never resolve inside the checkout.
type RunRequest struct { //nolint:govet // field grouping documents the request surface
	ReadRequest
	LookPath    func(string) (string, error)
	PassThrough []string
	Environ     []string
	Agent       string
	Checkout    string
	TimeoutRaw  string
	RoundsRaw   string
	Print       bool
	JSONMode    bool
	Warnings    []string
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
// it out so its envelope stays byte-identical to the single-round shape.
type RunData struct { //nolint:govet // field order is the published property order
	Agent      string     `json:"agent"`
	Binary     string     `json:"binary"`
	Argv       []string   `json:"argv"`
	Bundle     string     `json:"bundle"`
	Artifact   string     `json:"artifact"`
	StartedAt  string     `json:"started_at,omitempty"`
	EndedAt    string     `json:"ended_at,omitempty"`
	DurationMS int        `json:"duration_ms,omitempty"`
	Exit       int        `json:"exit"`
	TimedOut   bool       `json:"timed_out"`
	Timeout    int        `json:"timeout"`
	Fresh      bool       `json:"artifact_fresh"`
	Printed    bool       `json:"printed,omitempty"`
	Record     string     `json:"record,omitempty"`
	Repair     *RunRepair `json:"repair,omitempty"`
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
// review hash it left, whether the shape check passed, the checker's problems
// in full, the checker's overflow past its problem cap, and the round's wall
// time in seconds.
type RunAttempt struct { //nolint:govet // field order is the published property order
	Round        int       `json:"round"`
	PromptSHA256 string    `json:"prompt_sha256"`
	ReviewSHA256 string    `json:"review_sha256"`
	CheckOK      bool      `json:"check_ok"`
	Problems     []Problem `json:"problems"`
	Truncated    int       `json:"truncated"`
	WallS        float64   `json:"wall_s"`
}

// Wire renders the data object in wire order.
func (d *RunData) Wire() *jq.Obj {
	return structObj(reflect.ValueOf(d).Elem())
}
