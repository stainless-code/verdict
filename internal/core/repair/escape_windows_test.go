//go:build windows

package repair

import (
	"strings"
	"syscall"
	"testing"
)

// TestWindowsEscapedLenMatchesEscapeArg holds the mirror against the real
// syscall.EscapeArg on the only platform that runs it: a disagreement would
// mean the budget measures a command line Windows does not build.
func TestWindowsEscapedLenMatchesEscapeArg(t *testing.T) {
	t.Parallel()
	for _, s := range []string{
		"", "abc", "a b", `a"b`, `a\\"b`, `a\`, `a\ b`, `a b\`, "a\tb", `"`, `\`,
		strings.Repeat(`"`, 64),
		strings.Repeat(`\`, 64),
		strings.Repeat(`a b\`, 64),
		strings.Repeat(`<`, 64),
		`{"body":"x y","fix":"a\\b"}`,
	} {
		if got, want := windowsEscapedLen(s), len(syscall.EscapeArg(s)); got != want {
			t.Errorf("windowsEscapedLen(%q) = %d, want %d", s, got, want)
		}
	}
}
