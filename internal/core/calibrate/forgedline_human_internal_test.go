package calibratecore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/forgedline"
)

// A calibrate row quotes the review id, the PR state the host reported, and
// a finding's locator and outcome detail: each reads on its own row.
func TestLiveRowsCannotForgeLines(t *testing.T) {
	t.Parallel()
	rec := &forgedline.Recorder{}
	st := &state{req: &Request{Runtime: &core.Runtime{Progress: rec}}}
	live := map[string]any{"state": forgedline.Value, "mergedAt": forgedline.Value}
	rows := []outcome{{sig: "abc123", result: "fixed", locator: forgedline.Value, detail: forgedline.Value}}
	st.emitLiveHuman(forgedline.Value, "abc", live, rows, t.TempDir())
	forgedline.AssertNoForgedLine(t, rec.String(), "abc123 [fixed] "+forgedline.Named+" — "+forgedline.Named)
}
