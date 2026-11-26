package learncore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/forgedline"
)

// learn's human lines quote rule ids and pack names an imported pack chose.
func TestHumanLinesCannotForgeLines(t *testing.T) {
	t.Parallel()
	rec := &forgedline.Recorder{}
	st := &state{req: &Request{Runtime: &core.Runtime{Progress: rec}}}
	st.humanf("imported %s from %s@%s sha256:%s\n", forgedline.Value, forgedline.Value, "1.0.0", "abc")
	forgedline.AssertNoForgedLine(t, rec.String(), "imported "+forgedline.Named+" from "+forgedline.Named)
}
