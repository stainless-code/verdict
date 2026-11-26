package doctorcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/forgedline"
)

// A check's detail quotes a host CLI's reply (an auth status, a scope
// header), so it reads on its check line.
func TestCheckDetailCannotForgeLines(t *testing.T) {
	t.Parallel()
	rec := &forgedline.Recorder{}
	st := &state{req: &Request{Runtime: &core.Runtime{Progress: rec}}}
	st.add("gh auth", severityWarn, false, forgedline.Value)
	forgedline.AssertNoForgedLine(t, rec.String(), "WARN gh auth — "+forgedline.Named)
}
