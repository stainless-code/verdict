package run

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
)

// testRunner builds the run verb's runner around buffers the caller can
// inspect. A test that drives another verb, or JSON mode, adjusts the runner.
func testRunner() (*cli.Runner, *strings.Builder, *strings.Builder) {
	var out, diag strings.Builder
	r := &cli.Runner{
		Cmd: "run", Repo: "acme/app", Pr: "42",
		Out: &out, Diag: &diag, Human: &out,
		Now: time.Now, Effective: cli.Defaults(), EnvNoColor: true,
	}
	return r, &out, &diag
}

// TestRenderCommandQuotesForShell pins the --print rendering: safe arguments
// stay bare, everything else is single-quoted, and an embedded single quote
// is closed, escaped, and reopened so the line stays copy-pasteable.
func TestRenderCommandQuotesForShell(t *testing.T) {
	tests := []struct {
		name string
		want string
		argv []string
	}{
		{
			name: "flags and paths stay bare",
			argv: []string{"claude", "-p", "--model", "opus", ".verdict/state/42/review.json"},
			want: "claude -p --model opus .verdict/state/42/review.json",
		},
		{
			name: "spaces and newlines are quoted",
			argv: []string{"claude", "-p", "Review acme/app PR 42\nand write one file."},
			want: "claude -p 'Review acme/app PR 42\nand write one file.'",
		},
		{
			name: "an embedded single quote is escaped",
			argv: []string{"echo", "it's fine"},
			want: `echo 'it'\''s fine'`,
		},
		{
			name: "an empty argument stays an argument",
			argv: []string{"claude", "-p", ""},
			want: "claude -p ''",
		},
		{
			name: "shell metacharacters are quoted",
			argv: []string{"agent", "$HOME;rm -rf *"},
			want: "agent '$HOME;rm -rf *'",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := RenderCommand(tc.argv); got != tc.want {
				t.Fatalf("RenderCommand = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestParseFlagsPassThrough proves everything after a bare `--` reaches the
// agent unchanged, including tokens the verb would otherwise own.
func TestParseFlagsPassThrough(t *testing.T) {
	r, _, _ := testRunner()
	flags, code, done := parseFlags(r, []string{
		"--agent", "claude", "--timeout", "42", "--print",
		"--", "--json", "--agent", "not-ours", "--", "trailing",
	})
	if done {
		t.Fatalf("parse failed with exit %d", code)
	}
	if flags.agent != "claude" || flags.timeout != "42" || !flags.print {
		t.Fatalf("flags = agent %q timeout %q print %v", flags.agent, flags.timeout, flags.print)
	}
	want := []string{"--json", "--agent", "not-ours", "--", "trailing"}
	if !slices.Equal(flags.pass, want) {
		t.Fatalf("pass-through = %q, want %q", flags.pass, want)
	}
}

// TestParseFlagsDefaults proves the absent --timeout keeps verify's default and
// that a bare `--` with nothing after it is no pass-through at all.
func TestParseFlagsDefaults(t *testing.T) {
	r, _, _ := testRunner()
	flags, code, done := parseFlags(r, []string{"--agent", "codex", "--"})
	if done {
		t.Fatalf("parse failed with exit %d", code)
	}
	if flags.agent != "codex" || flags.timeout != "300" || flags.print || len(flags.pass) != 0 {
		t.Fatalf("flags = %+v", flags)
	}
}

// TestParseFlagsUnknownFlagIsUsage proves an unknown pre-`--` token is a usage
// error (exit 2) naming the accepted surface, never a passthrough.
func TestParseFlagsUnknownFlagIsUsage(t *testing.T) {
	r, out, diag := testRunner()
	_, code, done := parseFlags(r, []string{"--agent", "claude", "--model", "opus"})
	if !done || code != cli.ExitUsage {
		t.Fatalf("exit = %d done %v, want the usage exit", code, done)
	}
	if out.Len() != 0 {
		t.Errorf("usage error wrote %q to stdout; want the diagnostic stream only", out.String())
	}
	if !strings.Contains(diag.String(), "--agent <name>") {
		t.Errorf("usage error does not name the accepted flags: %q", diag.String())
	}
}

// TestParseFlagsMissingValueIsUsage proves a value flag without its operand
// is a usage error rather than an empty agent name.
func TestParseFlagsMissingValueIsUsage(t *testing.T) {
	r, _, diag := testRunner()
	_, code, done := parseFlags(r, []string{"--agent"})
	if !done || code != cli.ExitUsage {
		t.Fatalf("exit = %d done %v, want the usage exit", code, done)
	}
	if !strings.Contains(diag.String(), "--agent") {
		t.Errorf("usage error does not name the flag: %q", diag.String())
	}
}

// TestRentryRebuildsTheCommand pins the re-run line: the identity it names
// is the one the operator ran, minus the unquotable pass-through tail.
func TestRentryRebuildsTheCommand(t *testing.T) {
	req := Request{Repo: "acme/app", Pr: "42"}
	got := rentry(req, flags{agent: "claude", timeout: "42", print: true}, true)
	want := "verdict run acme/app 42 --agent claude --timeout 42 --print --json"
	if got != want {
		t.Fatalf("rentry = %q, want %q", got, want)
	}
	if plain := rentry(req, flags{agent: "claude", timeout: defaultTimeoutS}, false); plain != "verdict run acme/app 42 --agent claude" {
		t.Fatalf("rentry default = %q", plain)
	}
}
