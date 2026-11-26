package backtest

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/forgedline"
)

// A mined candidate names paths and kinds from the repository's history.
func TestCandidateLineCannotForgeLines(t *testing.T) {
	t.Parallel()
	rec := &forgedline.Recorder{}
	st := &state{rt: &core.Runtime{Progress: rec}}
	st.humanLine(candidateLine(&Candidate{Head: "abcdef0123", Bugs: []Bug{{Path: forgedline.Value, Line: 3, Kind: "logic"}}}))
	forgedline.AssertNoForgedLine(t, rec.String(), forgedline.Named+":3 logic")
}
