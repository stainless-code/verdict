package cli

import (
	"bytes"
	"fmt"
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

// TestNewRunnerDiagStreamIsInert proves the production runner's diagnostic
// stream folds controls for every writer, not only Warn and Refuse: a gate
// check line or split candidate printed straight to r.Diag, and r.Human once
// --json routes it to stderr, come out inert. verdict's own color codes on a
// warning still reach the terminal.
func TestNewRunnerDiagStreamIsInert(t *testing.T) {
	t.Parallel()
	const hostile = "violated spec check: evil\x1b]52;c;cGF5bG9hZA==\x07\u009b2J\tend"
	const want = "violated spec check: evil ]52;c;cGF5bG9hZA==  2J\tend\n"
	var out, diag bytes.Buffer
	r := NewRunner("report", "acme/app", "7", &out, &diag, func(string) string { return "" })
	r.Effective = Defaults()
	fmt.Fprintln(r.Diag, hostile)
	r.JSONMode = true
	r.RouteHuman()
	fmt.Fprintln(r.Human, hostile)
	if got := diag.String(); got != want+want {
		t.Fatalf("stderr = %q, want both lines folded: %q", got, want+want)
	}
	diag.Reset()
	r.Effective.Color = "always"
	r.Warn("x\x1b[2Jy")
	if got := diag.String(); got != "\x1b[33mwarn: x [2Jy\x1b[0m\n" {
		t.Fatalf("colored warning = %q, want verdict's SGR kept and the message folded", got)
	}
}

// TestNewRunnerStdoutIsInertUnlessJSON proves the production runner's stdout
// folds controls in human mode and passes an envelope through untouched under
// --json, and that the choice is read at write time: a mode that settles
// after the runner is built still routes right.
func TestNewRunnerStdoutIsInertUnlessJSON(t *testing.T) {
	t.Parallel()
	const hostile = "1. [open] app/x\x1b]52;c;cGF5bG9hZA==\x07\u009b2J\ry.go:3\tok\n"
	var out, diag bytes.Buffer
	r := NewRunner("report", "acme/app", "7", &out, &diag, func(string) string { return "" })
	r.RouteHuman()
	fmt.Fprint(r.Human, hostile)
	fmt.Fprint(r.Out, hostile)
	const folded = "1. [open] app/x ]52;c;cGF5bG9hZA==  2J y.go:3\tok\n"
	if got := out.String(); got != folded+folded {
		t.Fatalf("human stdout = %q; want both writes folded", got)
	}
	out.Reset()
	r.JSONMode = true
	fmt.Fprint(r.Out, hostile)
	if got := out.String(); got != hostile {
		t.Fatalf("--json stdout = %q; want the bytes untouched", got)
	}
}

// TestWarnFoldsANewlineInTheMessage proves a warning stays one line: no
// verdict warning carries a break of its own, so a newline came from an
// interpolated path and must not start a forged line.
func TestWarnFoldsANewlineInTheMessage(t *testing.T) {
	t.Parallel()
	var diag bytes.Buffer
	r := &Runner{Out: &bytes.Buffer{}, Diag: &diag, Now: time.Now, Effective: Defaults()}
	r.Warn("finding at app/x\nverdict: gate passes — 0 open findings:3 cannot be checked")
	const want = "warn: finding at app/x verdict: gate passes — 0 open findings:3 cannot be checked\n"
	if got := diag.String(); got != want {
		t.Fatalf("stderr = %q; want one line %q", got, want)
	}
}
