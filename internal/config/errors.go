package config

import (
	"fmt"

	"github.com/stainless-code/verdict/internal/core"
)

// Refusal codes this package emits. Each is registered in
// core.RefusalCodes: the archtest guard fails in both directions when the
// set drifts, and cmd/tsgen renders the client union from the same list.
const (
	// CodeTooLarge refuses a repository configuration over the parser cap.
	CodeTooLarge = "config-too-large"
	// CodeBadConfig refuses malformed or semantically invalid configuration.
	CodeBadConfig = "bad-config"
	// CodeBadCredentials refuses a configuration carrying a credential key.
	CodeBadCredentials = "bad-config-credentials" //nolint:gosec // G101: refusal code, not a credential.
	// CodeBadRequirement refuses an unsatisfiable or invalid requirement.
	CodeBadRequirement = "bad-requirement"
	// CodeBadScope refuses an invalid path scope.
	CodeBadScope = "bad-scope"
)

// refuse builds the layer's sealed refusal. The message always names the
// field path or rule so one input produces one diagnostic.
func refuse(code, format string, args ...any) *core.RefusalError {
	return &core.RefusalError{Code: code, Message: fmt.Sprintf(format, args...)}
}
