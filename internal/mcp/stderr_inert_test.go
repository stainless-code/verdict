package mcp

import (
	"bytes"
	"strings"
	"testing"
)

// TestServerStderrIsInert proves the session's diagnostic stream folds
// controls: a tool's warning quoting a path a stranger named with an OSC 52
// clipboard write reaches the operator's terminal as plain text.
func TestServerStderrIsInert(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	s := New(&Config{Stderr: &stderr})
	stderrProgress{w: s.cfg.Stderr}.Warn("cannot read evil\x1b]52;c;cGF5bG9hZA==\x07dir\u009b2J")
	if got, want := stderr.String(), "warn: cannot read evil ]52;c;cGF5bG9hZA== dir 2J\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if strings.ContainsAny(stderr.String(), "\x1b\x07\u009b") {
		t.Fatal("stderr carries a control byte")
	}
}
