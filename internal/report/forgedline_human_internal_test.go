package report

import (
	"bytes"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/forgedline"
)

// report's human lines quote finding locators, rules, and bodies; each
// prints as the one line it is, as every verb's lines do through
// Runner.HumanLines.
func TestHumanLinesCannotForgeLines(t *testing.T) {
	t.Parallel()
	var out, diag bytes.Buffer
	r := cli.NewRunner(cmdReport, "acme/app", "1", &out, &diag, func(string) string { return "" })
	emitReport(r, Request{}, &reportcore.Response{Human: []string{"finding: " + forgedline.Value}})
	forgedline.AssertNoForgedLine(t, out.String(), "finding: "+forgedline.Named+"\n")
}
