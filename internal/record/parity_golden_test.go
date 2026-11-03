// Package record_test holds the golden contract for the Go record core: the
// shared harness runs the Go binary on hermetic fixture bundles and pins exit
// codes, stdout envelopes, findings.json, record.json, and every other
// store side effect against checked-in goldens (go-port 08: the bash
// comparison retired with the shell).
package record_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/parity"
)

var harness *parity.Harness

// TestMain builds the Go binary once and runs the record parity suite.
func TestMain(m *testing.M) {
	os.Exit(parity.RunMain(m, &harness))
}

// runOne runs the Go binary on fixtures already written under dir and returns
// the run's streams, exit code, and file tree. Controls are seeded first, and
// TMPDIR stays inside the sandbox so the store's atomic-write temp files
// never escape the snapshot.
func runOne(t *testing.T, dir, home string, args ...string) parity.Outputs {
	t.Helper()
	seedControls(t, dir)
	return harness.Run(t, dir, home, map[string]string{"TMPDIR": dir}, args...)
}

// goldenBoth runs one record case on hermetic fixtures and pins exit codes,
// streams, and every written file against testdata/<name>.golden. setup
// writes the bundle into dir; fixtures must be deterministic. slug is the
// bundle the case records into.
func goldenBoth(t *testing.T, name, slug string, setup func(t *testing.T, dir string), args ...string) {
	t.Helper()
	goldenBothEnv(t, name, slug, setup, nil, args...)
}

// goldenBothEnv is goldenBoth with extra environment variables (policy knobs).
func goldenBothEnv(
	t *testing.T, name, slug string, setup func(t *testing.T, dir string),
	extraEnv map[string]string, args ...string,
) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		t.Helper()
		(&parity.Case{
			Name: name,
			Setup: func(t *testing.T, dir, _ string) {
				t.Helper()
				setup(t, dir)
				seedControls(t, dir)
			},
			Env:          extraEnv,
			Args:         append([]string{"record", "acme/app", slug}, args...),
			TmpInSandbox: true,
			Omit:         seededControls,
		}).Run(t, harness)
	})
}

// pinGolden pins a run already executed by runOne against testdata/<name>.
// The determinism legs run twice and pin one side, so they bypass Case's
// own run and render the captured outputs here.
func pinGolden(t *testing.T, name, dir string, out parity.Outputs) {
	t.Helper()
	parity.Check(t, name, parity.Render(parity.Outputs{
		Status: out.Status, Stdout: out.Stdout, Stderr: out.Stderr,
		Before: graded(out.Before), Files: graded(out.Files),
	}, []string{dir}))
}

// graded drops the seeded-controls snapshots from a file map, keeping both
// sides of the comparison filtered alike: filtering only the after side would
// render every dropped key as a deletion.
func graded(files map[string]string) map[string]string {
	kept := make(map[string]string, len(files))
	for k, v := range files {
		if !seededControls(k) {
			kept[k] = v
		}
	}
	return kept
}

// seededControls reports the seeded controls snapshots (bundle controls.json
// and bundle controls/**) the harness plants from the real checkout, so they
// are machine-dependent and never graded.
func seededControls(key string) bool {
	rel, ok := strings.CutPrefix(key, "C/")
	return ok && isControlsArtifact(rel)
}
