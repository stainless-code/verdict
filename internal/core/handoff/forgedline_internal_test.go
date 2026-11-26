package handoff

import (
	"errors"
	"testing"

	"github.com/stainless-code/verdict/internal/forgedline"
)

// A findings-store read error can quote the store's own bytes.
func TestStoreErrorCannotForgeErrorLine(t *testing.T) {
	t.Parallel()
	forgedline.AssertOneErrorLine(t, refusalOf(errors.New(forgedline.Value)), forgedline.Named)
}
