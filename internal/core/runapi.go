package core

import (
	"reflect"

	"github.com/stainless-code/verdict/internal/jq"
)

// RunRequest is the run service invocation: the read runtime (commander,
// clock, progress observer) and positionals, the agent name, the raw
// --timeout value (so bad-timeout stays a typed refusal at exit 1), the
// print-only mode, the operator's pass-through arguments, the ordered
// process-environ snapshot the child inherits, whether --json mode defaults
// NO_COLOR on for the child, and the PATH probe the adapter injects.
//
// LookPath is the one adapter-injected process fact besides the environment:
// resolution has to answer before the watchdog can spawn, and a binary that
// does not resolve is a typed refusal (agent-missing) rather than a spawn
// error. Nil falls back to the host's exec.LookPath.
type RunRequest struct { //nolint:govet // field grouping documents the request surface
	ReadRequest
	LookPath    func(string) (string, error)
	PassThrough []string
	Environ     []string
	Agent       string
	TimeoutRaw  string
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
// directive replaced by a placeholder and secret operands redacted.
type RunData struct { //nolint:govet // field order is the published property order
	Agent      string   `json:"agent"`
	Binary     string   `json:"binary"`
	Argv       []string `json:"argv"`
	Bundle     string   `json:"bundle"`
	Artifact   string   `json:"artifact"`
	StartedAt  string   `json:"started_at,omitempty"`
	EndedAt    string   `json:"ended_at,omitempty"`
	DurationMS int      `json:"duration_ms,omitempty"`
	Exit       int      `json:"exit"`
	TimedOut   bool     `json:"timed_out"`
	Timeout    int      `json:"timeout"`
	Fresh      bool     `json:"artifact_fresh"`
	Printed    bool     `json:"printed,omitempty"`
	Record     string   `json:"record,omitempty"`
}

// Wire renders the data object in wire order.
func (d *RunData) Wire() *jq.Obj {
	return structObj(reflect.ValueOf(d).Elem())
}
