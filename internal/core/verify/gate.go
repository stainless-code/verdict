// Package verifycore is the verify domain service: `verify` as a typed
// (ctx, request) → (response, refusal) call. It owns the allowlist gate,
// the watchdog spawn through the injected [core.Commander], and the patch
// outcome write; internal/verify owns argv parsing, stream writes, and exit
// mapping. The execution model stays `bash -c` (the documented contract):
// the process runs in its own process group, a timeout kills the whole
// tree, the exit plus a redacted tail are recorded, and NO_COLOR is
// inherited per the never-prompts doctrine.
package verifycore

import (
	"github.com/stainless-code/verdict/internal/core"
)

// Check runs the pre-exec allowlist gate over the raw verifyCmd, returning
// the typed refusal on the first hostile match and nil when clean. The arm
// order is fixed: mid-word substitution, command-position
// substitution, glob/brace fragmentation, then destructive, credential, and
// networked classes, then repo escape. The normalized texts are built once
// and shared by every section.
func Check(raw string) *core.RefusalError {
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

const (
	codeDestructive = "verify-destructive"
	codeNetworked   = "verify-networked"
	codeRepoEscape  = "verify-repo-escape"
)

//nolint:gosec // G101: refusal code, not a credential.
const codeCredential = "verify-credential-read"

func refuse(code, frag, detail string) *core.RefusalError {
	return &core.RefusalError{Code: code, Message: "refusing verifyCmd: matched '" + frag + "' (" + detail + ")"}
}
