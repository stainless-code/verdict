package triagecore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/forgedline"
)

// A triage status is an agent value; it is quoted on one line.
func TestStatusCannotForgeErrorLine(t *testing.T) {
	t.Parallel()
	forgedline.AssertOneErrorLine(t, checkStatus(forgedline.Value), "unknown status '"+forgedline.Named+"'")
}
