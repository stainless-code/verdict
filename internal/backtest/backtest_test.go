package backtest

import (
	"slices"
	"strings"
	"testing"
)

// TestParseFlagsMineSurface proves the mined mode reads its own flags and
// leaves the replay defaults alone.
func TestParseFlagsMineSurface(t *testing.T) {
	f, uerr := parseFlags([]string{"mine", "--since", "90d", "--run", "sweep"})
	if uerr != nil {
		t.Fatalf("parse: %v", uerr)
	}
	if f.mode != modeMine || f.since != "90d" || f.run != "sweep" {
		t.Errorf("flags = %+v", f)
	}
	if f.until != "HEAD" || f.agent != "" || len(f.pass) != 0 {
		t.Errorf("mine picked up replay state: %+v", f)
	}
}

// TestParseFlagsReplaySurface proves the replay mode reads its flags and the
// pass-through tail after a bare `--`.
func TestParseFlagsReplaySurface(t *testing.T) {
	args := []string{
		"replay", "--run", "sweep-1", "--agent", "claude", "--max-reviews", "3",
		"--budget-usd", "2.5", "--timeout", "60", "--rounds", "2",
		"--", "--model", "fast", "--", "trailing",
	}
	f, uerr := parseFlags(args)
	if uerr != nil {
		t.Fatalf("parse: %v", uerr)
	}
	if f.mode != modeReplay || f.run != "sweep-1" || f.agent != "claude" {
		t.Errorf("flags = %+v", f)
	}
	if f.maxReviews != "3" || f.budgetUSD != "2.5" || f.timeout != "60" || f.rounds != "2" {
		t.Errorf("budgets = %+v", f)
	}
	want := []string{"--model", "fast", "--", "trailing"}
	if !slices.Equal(f.pass, want) {
		t.Errorf("pass-through = %q, want %q", f.pass, want)
	}
}

// TestParseFlagsPerModeRejectsTheOtherModesFlags proves a flag from the other
// mode is a usage error naming what the mode accepts, not a silent no-op.
func TestParseFlagsPerModeRejectsTheOtherModesFlags(t *testing.T) {
	tests := []struct { //nolint:govet // the table reads name, case, and expectation in that order
		name string
		args []string
		part string
	}{
		{"mine with agent", []string{"mine", "--agent", "claude"}, "want --since <90d|12w|18mo|2y|ref>, --until <ref>, --run <slug>, --json"},
		{"mine with budget", []string{"mine", "--budget-usd", "5"}, "want --since <90d"},
		{"mine with pass-through", []string{"mine", "--"}, "want --since <90d"},
		{"replay with since", []string{"replay", "--since", "90d"}, "want --run <id>, --agent <name>"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { checkModeRejection(t, tc.args, tc.part) })
	}
}

// checkModeRejection proves the other mode's flag is a usage error naming the
// surface the active mode accepts.
func checkModeRejection(t *testing.T, args []string, part string) {
	t.Helper()
	_, uerr := parseFlags(args)
	if uerr == nil {
		t.Fatal("accepted a flag from the other mode")
	}
	if !strings.Contains(uerr.Message, part) {
		t.Errorf("usage %q does not name the accepted surface (%q)", uerr.Message, part)
	}
}

// TestParseFlagsModeErrors proves an absent mode and an unknown one are usage
// errors.
func TestParseFlagsModeErrors(t *testing.T) {
	for _, args := range [][]string{{}, {"nope"}} {
		if _, uerr := parseFlags(args); uerr == nil {
			t.Errorf("parseFlags(%q) accepted an unsupported mode", args)
		}
	}
}

// TestParseFlagsMissingValueIsUsage proves a value flag without its operand
// names the flag instead of consuming the next token.
func TestParseFlagsMissingValueIsUsage(t *testing.T) {
	_, uerr := parseFlags([]string{"replay", "--agent"})
	if uerr == nil || !strings.Contains(uerr.Message, "--agent") {
		t.Fatalf("usage = %v, want the missing --agent operand", uerr)
	}
}

// TestRentryRebuildsEachMode proves the re-run line reproduces the invocation
// the operator made, without the pass-through tail.
func TestRentryRebuildsEachMode(t *testing.T) {
	tests := []struct { //nolint:govet // the table reads the case before its expectation
		name string
		args []string
		json bool
		want string
	}{
		{
			"mine",
			[]string{"mine", "--since", "90d", "--run", "sweep"},
			false,
			"verdict backtest mine --since 90d --run sweep",
		},
		{
			"mine with until",
			[]string{"mine", "--since", "90d", "--until", "main"},
			true,
			"verdict backtest mine --since 90d --until main --json",
		},
		{
			"replay defaults",
			[]string{"replay", "--run", "sweep-1", "--agent", "claude"},
			false,
			"verdict backtest replay --run sweep-1 --agent claude",
		},
		{"replay budgets", []string{
			"replay", "--run", "sweep-1", "--agent", "codex", "--max-reviews", "4",
			"--budget-usd", "3", "--timeout", "90", "--rounds", "2", "--", "--model", "x",
		}, false, "verdict backtest replay --run sweep-1 --agent codex --max-reviews 4 --budget-usd 3 --timeout 90 --rounds 2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { checkRentry(t, tc.args, tc.json, tc.want) })
	}
}

// checkRentry proves one invocation rebuilds to its literal re-run line.
func checkRentry(t *testing.T, args []string, jsonMode bool, want string) {
	t.Helper()
	f, uerr := parseFlags(args)
	if uerr != nil {
		t.Fatalf("parse: %v", uerr)
	}
	if got := rentry(f, jsonMode); got != want {
		t.Fatalf("rentry = %q, want %q", got, want)
	}
}
