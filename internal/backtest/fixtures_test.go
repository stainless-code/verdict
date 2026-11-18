package backtest

import (
	"strings"
	"testing"
)

// TestParseFlagsFixturesSurface proves the mode reads only its own two flags
// and never claims to launch an agent.
func TestParseFlagsFixturesSurface(t *testing.T) {
	f, uerr := parseFlags([]string{"fixtures", "--fixture", "fx2", "--recordings", "run-dir"})
	if uerr != nil {
		t.Fatalf("parse: %v", uerr)
	}
	if f.mode != modeFixtures || f.fixture != "fx2" || f.recordings != "run-dir" {
		t.Errorf("flags = %+v", f)
	}
	if f.run != "" || f.agent != "" || len(f.pass) != 0 {
		t.Errorf("fixtures picked up replay state: %+v", f)
	}
	if f.launches() {
		t.Error("fixtures claims to launch an agent")
	}
	if got, want := rentry(f, false), "verdict backtest fixtures --fixture fx2 --recordings run-dir"; got != want {
		t.Errorf("rentry = %q, want %q", got, want)
	}
}

// TestParseFlagsFixturesRejectsReplayFlags keeps --run, --agent and the
// pass-through tail out of a read-only mode.
func TestParseFlagsFixturesRejectsReplayFlags(t *testing.T) {
	for _, args := range [][]string{
		{"fixtures", "--run", "x"},
		{"fixtures", "--agent", "claude"},
		{"fixtures", "--", "anything"},
	} {
		if _, uerr := parseFlags(args); uerr == nil {
			t.Errorf("parsed %q, want a usage error", args)
		}
	}
}

// TestParseFlagsRejectsFixtureFlagsOutsideFixtures pins both the explicit
// modes and the bare --since form: a fixture operand never silently resolves
// into a composed run.
func TestParseFlagsRejectsFixtureFlagsOutsideFixtures(t *testing.T) {
	for _, flag := range []string{"--fixture", "--recordings"} {
		for _, args := range [][]string{
			{"--since", "90d", flag, "x"},
			{"mine", "--since", "90d", flag, "x"},
			{"reverts", flag, "x"},
			{"replay", flag, "x"},
			{"score", flag, "x"},
		} {
			assertFixtureFlagRefused(t, args, flag)
		}
	}
}

func assertFixtureFlagRefused(t *testing.T, args []string, flag string) {
	t.Helper()
	_, uerr := parseFlags(args)
	if uerr == nil {
		t.Errorf("parsed %q, want a usage error", args)
		return
	}
	if !strings.Contains(uerr.Message, flag) {
		t.Errorf("usage for %q = %q, want it to name %s", args, uerr.Message, flag)
	}
}

func TestFixturesRentryQuotesRecordings(t *testing.T) {
	f, uerr := parseFlags([]string{"fixtures", "--recordings", "recorded runs"})
	if uerr != nil {
		t.Fatalf("parse: %v", uerr)
	}
	if got, want := rentry(f, false), "verdict backtest fixtures --recordings 'recorded runs'"; got != want {
		t.Errorf("rentry = %q, want %q", got, want)
	}
}
