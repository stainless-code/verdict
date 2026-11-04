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
}

// Wire renders the data object in wire order.
func (d *VerifyData) Wire() *jq.Obj {
	return structObj(reflect.ValueOf(d).Elem())
}
