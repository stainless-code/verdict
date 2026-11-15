package trendcore

import (
	"strings"
	"testing"
)

// TestTrendQuotesCorpusDirInSkipWarning proves a directory name cannot forge a
// warning line: the sink quotes it, so a newline stays escaped.
func TestTrendQuotesCorpusDirInSkipWarning(t *testing.T) {
	st := &state{req: &Request{}}
	st.skipCorpus("corpus-a\nwarn: INJECTED", "missing full.diff")
	if len(st.warnings) != 1 {
		t.Fatalf("warnings = %q, want one skip", st.warnings)
	}
	got := st.warnings[0]
	if strings.Contains(got, "\n") {
		t.Fatalf("warning contains a newline: %q", got)
	}
	if !strings.Contains(got, `\n`) {
		t.Fatalf("warning did not escape the newline: %q", got)
	}
}
