package fetchcore

import (
	"errors"
	"testing"

	"github.com/stainless-code/verdict/internal/forgedline"
)

// An ignore-file parse error quotes the pattern the base revision carries.
func TestIgnoreErrorCannotForgeErrorLine(t *testing.T) {
	t.Parallel()
	forgedline.AssertOneErrorLine(t, ignoreRefusal(errors.New(forgedline.Value)), forgedline.Named)
}
