package reportcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/forgedline"
)

// A --compare target names a bundle a stranger may have named.
func TestCompareTargetCannotForgeErrorLine(t *testing.T) {
	t.Parallel()
	forgedline.AssertOneErrorLine(t, missingCompare(forgedline.Value), "--compare "+forgedline.Named+" names no bundle")
}
