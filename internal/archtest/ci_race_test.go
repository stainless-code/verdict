package archtest

// Guard for the Windows test lane. `go test` there repeated what the other two
// lanes had outgrown, so -race and -shuffle were gated on two of the three
// shipped OSes: a data race in code that only builds for Windows could reach a
// release with every lane green. The lane now repeats `make test-race`'s flags,
// which needs cgo on (the detector links against the runner's C toolchain) and a
// larger job budget for the instrumented suite.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// windowsRaceFlags is the flag set `make test-race` runs on macOS and Ubuntu;
// the Windows lane repeats it verbatim. Written as a literal so a lane that
// quietly drops a flag fails even while the Makefile still carries it.
const windowsRaceFlags = "go test -race -shuffle=on -count=1"

// TestCIWindowsLaneRunsRaceAndShuffle fails when the Windows lane stops running
// the race detector or the shuffled order, or loses the cgo switch that lets the
// detector link.
func TestCIWindowsLaneRunsRaceAndShuffle(t *testing.T) {
	workflow := readCIWorkflow(t)
	block, ok := workflowJobs(t, "ci.yml", workflow)["go-windows"]
	if !ok {
		t.Fatal("ci.yml has no go-windows job: Windows would only cross-compile")
	}
	joined := strings.Join(block, "\n")
	if !strings.Contains(joined, windowsRaceFlags) {
		t.Errorf("go-windows lost %q: -race and -shuffle would be gated on two of the three OSes",
			windowsRaceFlags)
	}
	for _, want := range []string{`CGO_ENABLED: "1"`, "gcc --version"} {
		if !strings.Contains(joined, want) {
			t.Errorf("go-windows lost %q: the race detector needs the C toolchain it links against",
				want)
		}
	}
}

// TestTestRaceFlagsMatchTheWindowsLane keeps the two lists from drifting: every
// test flag `make test-race` passes must appear in the Windows lane's own
// command, so a flag added to one lane cannot leave the other two behind.
func TestTestRaceFlagsMatchTheWindowsLane(t *testing.T) {
	recipe := testRaceFlags(t)
	workflow := readCIWorkflow(t)
	joined := strings.Join(workflowJobs(t, "ci.yml", workflow)["go-windows"], "\n")
	for _, flag := range recipe {
		if !strings.Contains(joined, flag) {
			t.Errorf("go-windows never passes %q, which make test-race runs: the third OS would skip it",
				flag)
		}
	}
}

// testRaceFlags returns the flags of the test-race target's `go test` line.
func testRaceFlags(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(moduleRoot(t), "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	lines := strings.Split(string(raw), "\n")
	start := lineIndex(lines, "test-race:")
	if start < 0 {
		t.Fatal("Makefile has no test-race target: the Windows lane pins flags nothing defines")
	}
	for _, line := range lines[start+1:] {
		if !strings.Contains(line, " test ") {
			continue
		}
		var flags []string
		for _, token := range strings.Fields(line) {
			if strings.HasPrefix(token, "-") && token != "-" {
				flags = append(flags, token)
			}
		}
		return flags
	}
	t.Fatal("test-race runs no `go test` line: the guard would pass on an empty flag set")
	return nil
}
