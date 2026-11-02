package verify_test

import (
	"os"
	"path/filepath"
	"testing"
)

// edgeSetup writes patch.json with the verifyCmd.
func edgeSetup(cmd string) func(t *testing.T, dir, home string) {
	return func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPatch(t, dir, "5201", cmd)
	}
}

// TestVerifyTimeoutTreeKill proves the timeout kills the whole process
// tree: a grandchild that ignores SIGTERM still dies with the group,
// timed_out records true, and the timeout marker names the seconds.
func TestVerifyTimeoutTreeKill(t *testing.T) {
	harness.RunBoth(t, "timeout-tree-kill",
		edgeSetup(`trap '' TERM; (trap '' TERM; sleep 30) & sleep 30`),
		nil, false,
		"verify", "acme/app", "5201", "--json", "--timeout", "2")
}

// TestVerifyOrphanSweep proves the decoy-exit sweep: the parent exits 0
// while a child outlives it, so the recorded exit stays 0 with timedOut
// true and the orphan marker.
func TestVerifyOrphanSweep(t *testing.T) {
	harness.RunBoth(t, "orphan-sweep",
		edgeSetup(`(sleep 30 &)`),
		nil, false,
		"verify", "acme/app", "5201", "--json", "--timeout", "10")
}

// TestVerifyRedaction proves known secret values are redacted from the
// recorded tail (but the full .verify.out keeps raw output for debugging).
func TestVerifyRedaction(t *testing.T) {
	extra := map[string]string{"EVAL_PROBE_TOKEN": "supersecretvalue123"}
	harness.RunBoth(t, "redaction",
		edgeSetup(`printf '%s' "$EVAL_PROBE_TOKEN"`),
		extra, false,
		"verify", "acme/app", "5201", "--json", "--timeout", "10")
}

// TestVerifyNoColor proves the verify child inherits NO_COLOR=1 under
// --json when the parent environment leaves it unset (F1).
func TestVerifyNoColor(t *testing.T) {
	harness.RunBoth(t, "no-color",
		edgeSetup(`printf '%s' "$NO_COLOR"`),
		nil, false,
		"verify", "acme/app", "5201", "--json", "--timeout", "10")
}

// TestVerifyStdinClosed proves a stdin-consuming verifyCmd completes with
// closed stdin (exit 0, timedOut false).
func TestVerifyStdinClosed(t *testing.T) {
	harness.RunBoth(t, "stdin-closed",
		edgeSetup(`read -r line || true`),
		nil, false,
		"verify", "acme/app", "5201", "--json", "--timeout", "10")
}

// TestVerifyBundleGates proves the bundle-gate refusals and their
// no-write shape.
func TestVerifyBundleGates(t *testing.T) {
	empty := func(*testing.T, string, string) {}
	harness.RunBoth(t, "missing-patch", empty, nil, false,
		"verify", "acme/app", "5201", "--json", "--timeout", "10")
	harness.RunBoth(t, "no-verify-cmd", func(t *testing.T, dir, _ string) {
		t.Helper()
		bdir := filepath.Join(dir, ".review", "5201")
		if err := os.MkdirAll(bdir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bdir, "patch.json"), []byte(`{}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}, nil, false,
		"verify", "acme/app", "5201", "--json", "--timeout", "10")
	harness.RunBoth(t, "empty-verify-cmd", func(t *testing.T, dir, _ string) {
		t.Helper()
		mkPatch(t, dir, "5201", "")
	}, nil, false,
		"verify", "acme/app", "5201", "--json", "--timeout", "10")
	for _, tc := range []struct{ name, timeout string }{
		{"timeout-zero", "0"},
		{"timeout-alpha", "abc"},
		{"timeout-negative", "-5"},
	} {
		harness.RunBoth(t, tc.name, edgeSetup(`printf ok`), nil, false,
			"verify", "acme/app", "5201", "--json", "--timeout", tc.timeout)
	}
	harness.RunBoth(t, "unknown-flag", edgeSetup(`printf ok`), nil, false,
		"verify", "acme/app", "5201", "--bogus")
}
