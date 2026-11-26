package verifycore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/forgedline"
)

// The env-allowlist source names a file the repository controls.
func TestEnvSourceCannotForgeErrorLine(t *testing.T) {
	t.Parallel()
	forgedline.AssertOneErrorLine(t, validateEnvNames(forgedline.Value, []string{"1bad"}),
		"invalid environment variable name in "+forgedline.Named)
}
