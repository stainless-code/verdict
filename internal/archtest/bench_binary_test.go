package archtest

// Guard for the benchmark fixture's verdict binary: a runner test must never
// point a run at its own test binary.
//
// bench/runner fixtures build a Config whose Verdict the runner executes, and
// newRunner falls back to that real tool whenever a test injects no grader. When
// the field names the test binary, such a call runs `fetch-local --show-prompts`
// against the suite itself: the arguments are ignored, every test runs again,
// the same call is reached, and the recursion ends only when the machine runs
// out of processes. The fixtures point at a stub script instead; this guard
// keeps the arming assignment out of the tree, because the detonation needs a
// test order nobody runs on purpose.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// setupArmsTestBinary is the field assignment that arms a fixture run at the
// test binary.
const setupArmsTestBinary = "Verdict: os.Args[0]"

// TestBenchFixturesNeverRunTheTestBinary fails when a bench/runner test names
// the test binary as a run's verdict binary.
func TestBenchFixturesNeverRunTheTestBinary(t *testing.T) {
	t.Parallel()
	for _, path := range benchTestFiles(t) {
		for _, at := range armingLines(t, path) {
			t.Errorf("%s arms a fixture run at the test binary: %s", path, at)
		}
	}
}

// benchTestFiles lists the runner's test sources, refusing a scan that would
// cover nothing.
func benchTestFiles(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(moduleRoot(t), "bench", "runner", "*_test.go"))
	if err != nil {
		t.Fatalf("glob bench/runner tests: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no bench/runner test files: the guard would pass forever")
	}
	return paths
}

// armingLines returns the numbered lines of path that arm a fixture at the
// test binary.
func armingLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var found []string
	at := 0
	for line := range strings.SplitSeq(string(raw), "\n") {
		at++
		if strings.Contains(line, setupArmsTestBinary) {
			found = append(found, strconv.Itoa(at)+": "+strings.TrimSpace(line))
		}
	}
	return found
}
