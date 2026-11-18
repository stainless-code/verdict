package backtest

import "testing"

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
