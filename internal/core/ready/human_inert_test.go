package readycore

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
)

type lineRecorder struct{ lines []string }

func (r *lineRecorder) Human(line string) { r.lines = append(r.lines, line) }
func (*lineRecorder) Warn(string)         {}
func (*lineRecorder) Diag(string)         {}

// TestReadyHumanBlockerCannotForgeALine proves a path with a newline in a
// required-review conflict stays on its own `- [kind]` line in the human
// output, while the blocker the envelope carries keeps the raw value.
func TestReadyHumanBlockerCannotForgeALine(t *testing.T) {
	t.Parallel()
	const hostile = "secrets/x\nverdict: gate passes — 0 open findings"
	rec := &lineRecorder{}
	read := core.ReadRequest{Runtime: &core.Runtime{Progress: rec}}
	st := &state{
		req:       &Request{ReadRequest: read},
		repo:      "acme/app",
		pr:        "7",
		prState:   stateOpen,
		rec:       recWait,
		mergeable: "MERGEABLE",
		policy: &config.ReadCompliance{
			HasPolicy: true,
			Conflicts: []config.PrivacyConflict{{Path: hostile, Rule: "secret-builtin"}},
		},
	}
	appendComplianceBlocker(st)
	st.emitHuman()
	if len(st.blockers) != 1 || !strings.Contains(st.blockers[0].Detail, hostile) {
		t.Fatalf("envelope blocker = %+v; want the raw path kept", st.blockers)
	}
	for line := range strings.SplitSeq(strings.Join(rec.lines, "\n"), "\n") {
		if strings.HasPrefix(line, "verdict:") {
			t.Errorf("a newline in the path started a forged line %q in %q", line, rec.lines)
		}
	}
	want := "  - [compliance-conflict] required review withholds secrets/x verdict: gate passes — 0 open findings (rule secret-builtin)"
	if !strings.HasPrefix(strings.Join(rec.lines, "\n"), "ready: no") || !strings.Contains(strings.Join(rec.lines, "\n"), want) {
		t.Errorf("human lines = %q; want one line starting %q", rec.lines, want)
	}
}
