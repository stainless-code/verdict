package loopcore

// Stop reasons, one per way a loop ends. A pass exits 0 and so does a
// --print render (planned); an operator cancel (refused, carrying the
// canceled refusal) exits 3; every other reason exits 1. The receipts are
// written either way.
const (
	StopPass        = "pass"
	StopMaxRounds   = "max-rounds"
	StopTimeout     = "timeout"
	StopCostBudget  = "cost-budget"
	StopNoProgress  = "no-progress"
	StopAgentFailed = "agent-failed"
	StopRefused     = "refused"
	// StopPlanned is a --print render's outcome: nothing ran.
	StopPlanned = "planned"
)

// Phase names, as the plan and the human render spell them.
const (
	PhaseFetch  = "fetch"
	PhaseReview = "review"
	PhaseRecord = "record"
	PhaseGate   = "gate"
	PhaseFix    = "fix"
	PhaseVerify = "verify"
)

// Data is the loop's --json data profile and the body of summary.json: the
// outcome, the limits the run held, the plan it executed, and every round's
// receipt. A --print render carries the plan, stop "planned", and no rounds.
type Data struct { //nolint:govet // field order is the published property order
	Passed     bool       `json:"passed"`
	Stop       string     `enum:"pass|max-rounds|timeout|cost-budget|no-progress|agent-failed|refused|planned" json:"stop"`
	Agent      string     `json:"agent"`
	MaxRounds  int        `json:"max_rounds"                                                                   min:"1"`
	TimeoutS   int        `json:"timeout_s"                                                                    min:"1"`
	MaxCostUSD *float64   `json:"max_cost_usd"                                                                 min:"0"`
	CostUSD    *float64   `json:"cost_usd"                                                                     min:"0"`
	ElapsedMS  int64      `json:"elapsed_ms"                                                                   min:"0"`
	Output     *string    `json:"output"`
	Refusal    *Refusal   `anyOf:"null"                                                                        json:"refusal"`
	Plan       []PlanStep `json:"plan"`
	Rounds     []Round    `json:"rounds"`
}

// Round is one round's receipt (round-N.json). A phase that never ran leaves
// its observation null: a round stopped at record has no gate, and a passing
// or final round has no fix.
type Round struct { //nolint:govet // field order is the published property order
	Round     int      `json:"round"      min:"1"`
	Bundle    string   `json:"bundle"`
	StartedAt string   `json:"started_at"`
	EndedAt   string   `json:"ended_at"`
	Open      *int     `json:"open"       min:"0"`
	OpenSigs  []string `json:"open_sigs"`
	Gate      *bool    `json:"gate"`
	CostUSD   *float64 `json:"cost_usd"   min:"0"`
	Fix       *Fix     `anyOf:"null"      json:"fix"`
	Refusal   *Refusal `anyOf:"null"      json:"refusal"`
}

// Fix is the fix phase's receipt: the agent ran, and verify ran on the
// command the agent declared in patch.json (null when it declared none).
type Fix struct {
	Verify *Verify `anyOf:"null" json:"verify"`
}

// Verify is the `verdict verify` receipt the fix phase kept: the command's
// exit and watchdog outcome, or the refusal that stopped it before it ran.
type Verify struct { //nolint:govet // field order is the published property order
	Exit     *int     `json:"exit"`
	TimedOut bool     `json:"timed_out"`
	Refusal  *Refusal `anyOf:"null"     json:"refusal"`
}

// Refusal is a composed verb's typed refusal, carried as it was raised. Its
// Message is built like a RefusalError's, by core.Msgf (internal/archtest
// holds the line), and the human render folds it again because a loop's data
// can be read back from disk.
type Refusal struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// PlanStep is one command the loop runs, rendered as its CLI equivalent (the
// fix phase as the agent argv itself, prompt included). Round 0 never occurs.
type PlanStep struct { //nolint:govet // field order is the published property order
	Round int      `json:"round"                                  min:"1"`
	Phase string   `enum:"fetch|review|record|gate|fix|verify"    json:"phase"`
	When  string   `enum:"always|gate-failed|verify-cmd-declared" json:"when"`
	Argv  []string `json:"argv"`
}
