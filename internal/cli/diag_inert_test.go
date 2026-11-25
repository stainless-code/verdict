package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// TestDiagnosticsAreInert proves a warning or refusal message carrying
// terminal controls reaches stderr as plain text: a directory named after an
// OSC 52 clipboard write (a layer warning quotes the path) cannot write the
// operator's clipboard or repaint their terminal. The message's own line
// breaks and tabs (the evidence-gap warnings are tab-separated) survive.
func TestDiagnosticsAreInert(t *testing.T) {
	t.Parallel()
	const hostile = "cannot read /work/evil\x1b]52;c;cGF5bG9hZA==\x07dir\u009b2J\rx\tok"
	var diag bytes.Buffer
	r := &Runner{Out: &bytes.Buffer{}, Diag: &diag, Now: time.Now, Effective: Defaults()}
	r.Warn(hostile)
	r.Refuse(&RefusalError{Code: "bad-root", Message: hostile + "\nsecond line"})
	got := diag.String()
	if strings.ContainsAny(got, "\x1b\x07\u009b\r") {
		t.Fatalf("stderr carries a control byte:\n%q", got)
	}
	want := "warn: cannot read /work/evil ]52;c;cGF5bG9hZA== dir 2J x\tok\n" +
		"error: cannot read /work/evil ]52;c;cGF5bG9hZA== dir 2J x\tok\nsecond line\n"
	if !strings.HasPrefix(got, want) {
		t.Fatalf("stderr = %q, want it to start %q", got, want)
	}
}
