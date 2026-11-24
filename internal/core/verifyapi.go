package core

import (
	"reflect"

	"github.com/stainless-code/verdict/internal/jq"
)

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
	// SandboxMechanism names the wrapper (bwrap, sandbox-exec, or the
	// network-only unshare-net) so a receipt never reads network isolation as
	// the full shape. Sandboxed is true only for bwrap and sandbox-exec.
	SandboxMechanism string `json:"sandbox_mechanism,omitempty"`
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
	// SandboxMechanism names the wrapper, so a --json reader can tell the full
	// bwrap/sandbox-exec sandbox (Sandboxed true) from the network-only
	// unshare-net opt-in (Sandboxed absent). Absent means no wrapper ran.
	SandboxMechanism string `json:"sandboxMechanism,omitempty"`
}

// Wire renders the data object in wire order.
func (d *VerifyData) Wire() *jq.Obj {
	return structObj(reflect.ValueOf(d).Elem())
}
