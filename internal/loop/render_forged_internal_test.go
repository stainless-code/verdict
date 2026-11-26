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

// A round names its bundle, which a loop read back from disk carries as
// written.
func TestRoundRenderCannotForgeLines(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	render(&out, &loopcore.Data{Stop: loopcore.StopRefused, Rounds: []loopcore.Round{{Round: 1, Bundle: forgedline.Value}}})
	forgedline.AssertNoForgedLine(t, out.String(), "loop: round 1  "+forgedline.Named)
}

// TestRefusalRenderNamesTheCodeOnce pins the stop line for a refusal whose
// message already leads with its code, as record's coverage-incomplete does.
func TestRefusalRenderNamesTheCodeOnce(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	render(&out, &loopcore.Data{Stop: loopcore.StopRefused, Refusal: &loopcore.Refusal{
		Code: "coverage-incomplete", Message: "coverage-incomplete: no coverage[] attestation in review.json",
	}})
	if want := "loop: coverage-incomplete: no coverage[] attestation in review.json\n"; !strings.Contains(out.String(), want) {
		t.Errorf("render = %q, want the line %q", out.String(), want)
	}
}
