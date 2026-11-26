package postcore

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/diagram"
	"github.com/stainless-code/verdict/internal/forgedline"
)

// The dry-run preview quotes the diagram check's detail and renders the
// repository's attribution template: neither can add a line of its own.
func TestDryRunPreviewCannotForgeLines(t *testing.T) {
	t.Parallel()
	var human, warnings []string
	st := postSplitState(&warnings)
	st.req.Progress = testProgress{warnings: &warnings, human: &human}
	st.diagram = diagram.Status{State: "rewritten", Detail: forgedline.Value}
	st.previewDryRun("footer\n" + forgedline.Value)
	out := strings.Join(human, "\n") + "\n"
	forgedline.AssertNoForgedLine(t, out, "diagram: rewritten — "+forgedline.Named)
	forgedline.AssertNoForgedLine(t, out, "  | error: forged")
}
