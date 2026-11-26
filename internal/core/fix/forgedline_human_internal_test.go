package fixcore

import (
	"bytes"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/forgedline"
)

// A fix pass shows the finding's locator and body and the stored brief, all
// of it the reviewing agent's text: the body keeps its lines, each tagged,
// and no line of it can stand as verdict's own.
func TestPassBlockCannotForgeLines(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	st := &state{req: &Request{Human: &out}}
	st.printPassBlock(&passBlock{
		full: "abcdef0123456789", locator: forgedline.Value, severity: "important",
		body:    "first line\n" + forgedline.Value,
		anchors: &Anchors{Path: forgedline.Value},
		brief:   &core.Brief{Outcome: forgedline.Value, Approach: forgedline.Value, Questions: []string{forgedline.Value}},
	}, 1, "ledger.json")
	forgedline.AssertNoForgedLine(t, out.String(), "finding:\n  | first line\n  | x\n  | error: forged\n")
	forgedline.AssertNoForgedLine(t, out.String(), "  approach: "+forgedline.Named)
}
