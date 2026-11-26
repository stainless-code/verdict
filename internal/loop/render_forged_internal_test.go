package loop

import (
	"bytes"
	"strings"
	"testing"

	loopcore "github.com/stainless-code/verdict/internal/core/loop"
	"github.com/stainless-code/verdict/internal/forgedline"
)

// TestRefusalRenderCannotForgeLines pins the human render of a stopped
// loop's refusal: the message reads on its `loop:` line, so a refusal read
// back from disk carrying a stranger's newline cannot add a line that
// stands as verdict's own `error:` line.
func TestRefusalRenderCannotForgeLines(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	render(&out, &loopcore.Data{
		Stop:    loopcore.StopRefused,
		Refusal: &loopcore.Refusal{Code: "agent-failed", Message: forgedline.Value},
	})
	got := out.String()
	if !strings.Contains(got, "loop: agent-failed: "+forgedline.Named+"\n") {
		t.Errorf("render lacks the folded refusal line:\n%s", got)
	}
	for line := range strings.SplitSeq(got, "\n") {
		if strings.HasPrefix(line, "error:") {
			t.Errorf("forged line %q in render:\n%s", line, got)
		}
	}
}
