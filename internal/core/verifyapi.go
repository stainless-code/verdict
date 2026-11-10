package core

import (
	"reflect"

	"github.com/stainless-code/verdict/internal/jq"
)

// VerifyRequest is the verify service invocation: the read runtime and
// positionals, the raw --timeout value (so bad-timeout stays a typed
// refusal at exit 1), the ordered process-environ snapshot the child
// inherits (order preserved like [os.Environ]), and whether --json mode
// defaults NO_COLOR on for the child. Commander arrives via the runtime;
// tests inject a scripted one, the adapter installs the production
// watchdog.
type VerifyRequest struct { //nolint:govet // field grouping documents the request surface
	ReadRequest
	TimeoutRaw string
	Environ    []string
	JSONMode   bool
	Warnings   []string
	// Group selects deep group-validation mode: the stable PlanGroup.ID
	// whose validation this run records, with Command the explicit
	// operator-supplied command to execute. Both are required together;
	// neither keeps the ordinary patch.json verify path, which never reads
	// these fields.
	Group   string
	Command string
	// Sandbox wraps the executed command in the enforced Linux bwrap sandbox:
	// no network, empty credential dirs, a writable reviewed root, and a
	// private /tmp. A host that cannot enforce it refuses
	// sandbox-unavailable instead of running unwrapped.
	Sandbox bool
}

// ValidationReceipt is the tool-produced deep-validation record for one
// plan group: the command actually executed, its real status, and the
// run/group/head identity it is bound to. It is written by verify, never
// by the reviewed agent, and a receipt for another run, group, or head
// never certifies this one.
type ValidationReceipt struct { //nolint:govet // field order is the published property order
	SchemaVersion string `json:"schema_version"`
	Run           string `json:"run"`
	InputsSHA256  string `json:"inputs_sha256"`
	Group         string `json:"group"`
	ReviewedHead  string `json:"reviewed_head"`
	ExecutionHead string `json:"execution_head"`
	RepoRoot      string `json:"repo_root"`
	Command       string `json:"command"`
	At            string `json:"at"`
	OutputTail    string `json:"output_tail"`
	Exit          int    `json:"exit"`
	TimedOut      bool   `json:"timed_out"`
	Sandboxed     bool   `json:"sandboxed,omitempty"`
	Status        string `json:"status"`
	Failure       string `json:"failure,omitempty"`
	Passed        bool   `json:"passed"`
}

// VerifyResponse carries the verify outcome: the envelope data, warnings,
// the next step, and the recorded patch fields. Human lines stream live
// through the runtime progress observer (they interleave with warnings on
// the diagnostic stream), so the response carries no rendered lines.
type VerifyResponse struct { //nolint:govet // field grouping documents the response surface
	Repo     string
	Pr       string
	At       string
	Data     *VerifyData
	Warnings []string
	Next     string
	Exit     int
	TimedOut bool
	Tail     string
	PatchDir string
}

// VerifyData is the verify verb's --json data profile. Field order is the
// published order.
type VerifyData struct { //nolint:govet // field order is the published property order
	Exit       int    `json:"exit"`
	TimedOut   bool   `json:"timedOut"`
	OutputTail string `json:"outputTail"`
	Patch      string `json:"patch"`
	Group      string `json:"group,omitempty"`
	Head       string `json:"head,omitempty"`
	Receipt    string `json:"receipt,omitempty"`
	Sandboxed  bool   `json:"sandboxed,omitempty"`
}

// Wire renders the data object in wire order.
func (d *VerifyData) Wire() *jq.Obj {
	return structObj(reflect.ValueOf(d).Elem())
}
