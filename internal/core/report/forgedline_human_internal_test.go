package reportcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core/coverage"
	"github.com/stainless-code/verdict/internal/forgedline"
)

// The coverage block names each unattested file of the PR's diff on its own
// gap line; a path cannot add a line to it.
func TestCoverageGapCannotForgeLines(t *testing.T) {
	t.Parallel()
	percent := 50
	sum := &coverage.Summary{Percent: &percent, Total: 2, Files: 1, Unattested: []coverage.Gap{{File: forgedline.Value, Ranges: "1-2"}}}
	forgedline.AssertNoForgedLine(t, coverageHuman(sum, false), "\n- "+forgedline.Named+":1-2")
}
