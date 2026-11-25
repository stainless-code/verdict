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
	// Tree is the tree id of the content the command ran on, proven equal to
	// the reviewed commit's tree before it ran.
	Tree       string `json:"tree"`
	RepoRoot   string `json:"repo_root"`
	Command    string `json:"command"`
	At         string `json:"at"`
	OutputTail string `json:"output_tail"`
	Exit       int    `json:"exit"`
	TimedOut   bool   `json:"timed_out"`
	Sandboxed  bool   `json:"sandboxed,omitempty"`
	Status     string `json:"status"`
	Failure    string `json:"failure,omitempty"`
	Passed     bool   `json:"passed"`
	// SandboxMechanism names the wrapper (bwrap, sandbox-exec, or the
	// network-only unshare-net) so a receipt never reads network isolation as
	// the full shape. Sandboxed is true only for bwrap and sandbox-exec.
	SandboxMechanism string `json:"sandbox_mechanism,omitempty"`
	// Shared lists, sorted, the gitignored directories verify.share exposed
	// to the command from the checkout (read-only under --sandbox): content
	// outside Tree the outcome may depend on. Absent means a clean snapshot.
	Shared []string `json:"shared,omitempty"`
}

// VerifyReceipt is the tool-only record of one patch-path verify run: the
// command verify executed, the finding sig patch.json named, its real exit
// and timeout, and the tree id of the content it ran on. Only verify writes it
// (verify-receipt.json beside patch.json); record and post read it, and never
// patch.json, to decide whether a VERIFIED claim was observed. Tree is absent
// when verify could not snapshot the content (not a git checkout).
type VerifyReceipt struct {
	SchemaVersion string `json:"schema_version"`
	Command       string `json:"command"`
	Sig           string `json:"sig,omitempty"`
	Tree          string `json:"tree,omitempty"`
	At            string `json:"at"`
	// Shared lists, sorted, the gitignored directories verify.share exposed
	// to the command from the checkout (read-only under --sandbox): content
	// outside Tree the outcome may depend on. Absent means a clean snapshot.
	Shared   []string `json:"shared,omitempty"`
	Exit     int      `json:"exit"`
	TimedOut bool     `json:"timed_out"`
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
	// Tree is the tree id of the content the command ran on, the binding a
	// receipt carries; absent when verify could not snapshot it.
	Tree      string `json:"tree,omitempty"`
	Receipt   string `json:"receipt,omitempty"`
	Sandboxed bool   `json:"sandboxed,omitempty"`
	// SandboxMechanism names the wrapper, so a --json reader can tell the full
	// bwrap/sandbox-exec sandbox (Sandboxed true) from the network-only
	// unshare-net opt-in (Sandboxed absent). Absent means no wrapper ran.
	SandboxMechanism string `json:"sandboxMechanism,omitempty"`
	// Shared lists, sorted, the gitignored directories verify.share exposed
	// to the command from the checkout; absent when the snapshot was clean.
	Shared []string `json:"shared,omitempty"`
}

// Wire renders the data object in wire order.
func (d *VerifyData) Wire() *jq.Obj {
	return structObj(reflect.ValueOf(d).Elem())
}
