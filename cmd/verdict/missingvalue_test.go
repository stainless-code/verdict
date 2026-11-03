package main

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
)

// TestVerbMissingValue pins one text for a verb flag given without its value:
// every parser reports it through cli.MissingValue, so it never reads as an
// unknown flag and never drifts per verb.
func TestVerbMissingValue(t *testing.T) {
	hermeticEnv(t)
	cases := []struct {
		name string
		want string
		args []string
	}{
		{"skill show format", "--format needs a value (want md or json)", []string{"skill", "--format"}},
		{"skill install dir", "--dir needs a value", []string{"skill", "install", "--dir"}},
		{"skill install agent", "--agent needs a value", []string{"skill", "install", "--agent"}},
		{"upgrade version", "--version needs a value", []string{"upgrade", "--version"}},
		{"mcp config agent", "--agent needs a value (want claude, cursor, or codex)", []string{"mcp", "config", "--agent"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkUsageMessage(t, tc.args, tc.want)
		})
	}
}

// checkUsageMessage asserts args is a usage error whose stderr is exactly want.
func checkUsageMessage(t *testing.T, args []string, want string) {
	t.Helper()
	code, out, derr := runCapture(t, args)
	if code != cli.ExitUsage {
		t.Errorf("%v exit = %d; want %d (stderr: %s)", args, code, cli.ExitUsage, derr)
	}
	if out != "" {
		t.Errorf("%v wrote stdout %q; a usage error stays on stderr", args, out)
	}
	if got := strings.TrimSpace(derr); got != want {
		t.Errorf("%v stderr = %q; want %q", args, got, want)
	}
}
