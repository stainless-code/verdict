// Package verify ports the `verify` verb: execute patch.json's verifyCmd
// under a watchdog and record the outcome into the patch record. The gate
// (gate*.go) runs first and refuses hostile shapes typed before anything
// spawns; refusals leave stored state untouched. The execution model stays
// `bash -c` (the documented contract): the process runs in its own process
// group, a timeout kills the whole tree, the exit plus a redacted tail are
// recorded, and NO_COLOR is inherited per the never-prompts doctrine.
package verify

import (
	"github.com/stainless-code/verdict/internal/cli"
)

// Check runs the pre-exec allowlist gate over the raw verifyCmd, returning
// the typed refusal on the first hostile match and nil when clean. Arm
// order mirrors the shell: mid-word substitution, command-position
// substitution, glob/brace fragmentation, then destructive, credential, and
// networked classes, then repo escape. The normalized texts are built once
// and shared by every section.
func Check(raw string) *cli.RefusalError {
	g := normalize(raw)
	if ref := checkSentinel(g); ref != nil {
		return ref
	}
	if ref := checkCommandPosition(g); ref != nil {
		return ref
	}
	if ref := checkGlobFragment(g); ref != nil {
		return ref
	}
	if ref := checkDestructive(g); ref != nil {
		return ref
	}
	if ref := checkCredential(g); ref != nil {
		return ref
	}
	if ref := checkNetworked(g); ref != nil {
		return ref
	}
	if ref := checkRepoEscape(g); ref != nil {
		return ref
	}
	return nil
}

// Gate refusal codes, mirroring the shell's fail() call sites.
const (
	codeDestructive = "verify-destructive"
	codeNetworked   = "verify-networked"
	codeRepoEscape  = "verify-repo-escape"
)

// codeCredential is the credential-read refusal code (a gate code, not a
// secret).
//
//nolint:gosec // G101: refusal code, not a credential.
const codeCredential = "verify-credential-read"

// refuse builds the typed gate refusal: the message quotes the matched
// fragment exactly as grep -o printed it (byte-identical), and the try:
// hint rides the shared hint table.
func refuse(code, frag, detail string) *cli.RefusalError {
	return &cli.RefusalError{Code: code, Message: "refusing verifyCmd: matched '" + frag + "' (" + detail + ")"}
}
