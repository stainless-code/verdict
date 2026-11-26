package quality

import (
	"testing"

	"github.com/stainless-code/verdict/internal/forgedline"
)

// A fixture's id and label come from the fixture catalog on disk.
func TestHumanReportCannotForgeLines(t *testing.T) {
	t.Parallel()
	out := Human(&Report{Fixtures: []FixtureReport{{ID: forgedline.Value, Label: forgedline.Value}}})
	forgedline.AssertNoForgedLine(t, out, forgedline.Named+"  "+forgedline.Named)
}
