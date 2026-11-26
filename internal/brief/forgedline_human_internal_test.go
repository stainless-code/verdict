package brief

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/forgedline"
)

// A brief is the agent's text; fix and handoff both print Render's block.
func TestRenderCannotForgeLines(t *testing.T) {
	t.Parallel()
	out := Render(&core.Brief{
		Outcome: forgedline.Value, Approach: forgedline.Value,
		Sites:     []core.BriefSite{{Path: forgedline.Value}},
		Questions: []string{forgedline.Value},
	})
	forgedline.AssertNoForgedLine(t, out, "  outcome: "+forgedline.Named+"\n")
}
