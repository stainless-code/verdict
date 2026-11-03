// Package adopt_test holds the golden contract for the Go adopt verb: the
// shared harness runs the Go binary on hermetic fixtures and pins exit
// codes, stdout/stderr, and every written file against checked-in goldens,
// covering the handoff stamp, the dirty-target refusals, and the hunk
// snapshot rebuild.
package adopt_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/parity"
)

var harness *parity.Harness

// TestMain builds the Go binary once and runs the parity suite.
func TestMain(m *testing.M) {
	os.Exit(parity.RunMain(m, &harness))
}

// fixtureDiff is the shared bundle patch both target and source carry.
const fixtureDiff = "diff --git a/notes.txt b/notes.txt\n" +
	"index 1111111..2222222 100644\n" +
	"--- a/notes.txt\n" +
	"+++ b/notes.txt\n" +
	"@@ -1,3 +1,3 @@\n" +
	" top\n" +
	"-old line\n" +
	"+new line\n" +
	" bottom\n"

// fixturePR is the default GitHub-shaped PR record.
const fixturePR = `{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",` +
	`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","author":{"login":"teammate"}}`

// fixtureFindings is the two-entry source store: the first anchor is covered
// by the diff hunk, the second drifts past it.
const fixtureFindings = `[` +
	`{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,` +
	`"body":"Adopt me.","suggestion":null,"inBounds":true,"rigor":"pointed",` +
	`"severity":"important","kb":[],"sig":"0123456789abcdef0123456789abcdef01234567",` +
	`"sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open",` +
	`"rationale":"","history":[]},` +
	`{"kind":"inline","path":"notes.txt","side":"RIGHT","line":99,"anchored":true,` +
	`"body":"Drifted anchor here.","suggestion":null,"inBounds":true,"rigor":"pointed",` +
	`"severity":"informational","kb":[],"sig":"123456789abcdef0123456789abcdef012345678",` +
	`"sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open",` +
	`"rationale":"","history":[]}]`

// writeFile writes one fixture file, creating parents.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mkTarget crafts the target PR bundle (PR record + patch).
func mkTarget(t *testing.T, dir, pr string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, ".review", "4311", "PR.json"), pr)
	writeFile(t, filepath.Join(dir, ".review", "4311", "full.diff"), fixtureDiff)
}

// mkLocal crafts the source local bundle with its findings store.
func mkLocal(t *testing.T, dir, findings string) {
	t.Helper()
	bdir := filepath.Join(dir, ".review", "local-adopt1")
	writeFile(t, filepath.Join(bdir, "source.json"),
		`{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","head_ref":"work"}`)
	writeFile(t, filepath.Join(bdir, "full.diff"), fixtureDiff)
	writeFile(t, filepath.Join(bdir, "findings.json"), findings)
}

// setupHappy builds a fresh target and the standard source bundle.
func setupHappy(t *testing.T, dir, _ string) {
	t.Helper()
	mkTarget(t, dir, fixturePR)
	mkLocal(t, dir, fixtureFindings)
}

// setupPinned is setupHappy plus a fetch-time hunk snapshot (no rebuild warn).
func setupPinned(t *testing.T, dir, _ string) {
	t.Helper()
	setupHappy(t, dir, "")
	writeFile(t, filepath.Join(dir, ".review", "4311", "hunks.tsv"),
		"notes.txt\tNEW\t1\t3\n")
}

// setupMissingDiff builds a target without the patch store.
func setupMissingDiff(t *testing.T, dir, _ string) {
	t.Helper()
	mkTarget(t, dir, fixturePR)
	if err := os.Remove(filepath.Join(dir, ".review", "4311", "full.diff")); err != nil {
		t.Fatal(err)
	}
	mkLocal(t, dir, fixtureFindings)
}

// setupProvider builds a non-GitHub target.
func setupProvider(t *testing.T, dir, _ string) {
	t.Helper()
	mkTarget(t, dir, `{"state":"OPEN","provider":"gitlab",`+
		`"headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
		`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	mkLocal(t, dir, fixtureFindings)
}

// setupAdopted builds a target that already carries a findings store.
func setupAdopted(t *testing.T, dir, _ string) {
	t.Helper()
	setupHappy(t, dir, "")
	writeFile(t, filepath.Join(dir, ".review", "4311", "findings.json"), "[]")
}

// setupMissingFrom points --from at a bundle that does not exist.
func setupMissingFrom(t *testing.T, dir, _ string) {
	t.Helper()
	mkTarget(t, dir, fixturePR)
}

// setupCorruptFrom builds a truncated source findings store.
func setupCorruptFrom(t *testing.T, dir, _ string) {
	t.Helper()
	mkTarget(t, dir, fixturePR)
	mkLocal(t, dir, `[{"kind":"inline","sig":"nope"`)
}

// TestAdoptParity proves byte parity across the happy handoff, human mode,
// every typed refusal, and the hunk-snapshot rebuild warning.
func TestAdoptParity(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dir, home string)
		args  []string
	}{
		{"happy-json", setupHappy, []string{"adopt", "acme/app", "4311", "--from", "adopt1", "--json"}},
		{"happy-human", setupHappy, []string{"adopt", "acme/app", "4311", "--from", "adopt1"}},
		{"hunks-pinned", setupPinned, []string{"adopt", "acme/app", "4311", "--from", "adopt1", "--json"}},
		{"bad-target", setupHappy, []string{"adopt", "acme/app", "local-foo", "--from", "adopt1", "--json"}},
		{"bad-from", setupHappy, []string{"adopt", "acme/app", "4311", "--json"}},
		{"missing-findings", setupMissingFrom, []string{"adopt", "acme/app", "4311", "--from", "nope", "--json"}},
		{"missing-diff", setupMissingDiff, []string{"adopt", "acme/app", "4311", "--from", "adopt1", "--json"}},
		{"provider-unimplemented", setupProvider, []string{"adopt", "acme/app", "4311", "--from", "adopt1", "--json"}},
		{"already-adopted", setupAdopted, []string{"adopt", "acme/app", "4311", "--from", "adopt1", "--json"}},
		{"corrupt-store", setupCorruptFrom, []string{"adopt", "acme/app", "4311", "--from", "adopt1", "--json"}},
		{"unknown-flag", setupHappy, []string{"adopt", "acme/app", "4311", "--bogus", "--json"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parity.Golden(t, harness, tc.name, tc.setup, nil, tc.args...)
		})
	}
}
