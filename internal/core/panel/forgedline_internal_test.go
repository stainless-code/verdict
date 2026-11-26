package panelcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/forgedline"
)

// A verdicts.json sig is the checker agent's value.
func TestVerdictSigCannotForgeErrorLine(t *testing.T) {
	t.Parallel()
	forgedline.AssertOneErrorLine(t, bad("verification/verdicts.json duplicate (sig, checker): "+forgedline.Value),
		"(sig, checker): "+forgedline.Named)
}
