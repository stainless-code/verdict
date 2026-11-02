// Package adopt_test holds the shell-parity proof for the Go adopt verb:
// the shared harness runs BOTH scripts/review.sh adopt and the Go binary
// on identical fixtures and compares exit codes, stdout/stderr
// (timestamps scrubbed only), and every written file byte for byte.
package adopt_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/parity"
)

var harness *parity.Harness

// TestMain builds the Go binary once and runs the parity suite.
func TestMain(m *testing.M) {
	os.Exit(runParityMain(m))
}

// runParityMain builds the harness once and runs the suite, reporting the
// exit code; setup failures clean up and return 2.
func runParityMain(m *testing.M) int {
	h, err := parity.Build()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	harness = h
	defer harness.Teardown()
	return m.Run()
}

// mkTarget crafts a GitHub-shaped PR bundle.
func mkTarget(t *testing.T, dir, pr string, withDiff bool) {
	t.Helper()
	bdir := filepath.Join(dir, ".review", pr)
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	prjson := `{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","author":{"login":"teammate"}}`
	if err := os.WriteFile(filepath.Join(bdir, "PR.json"), []byte(prjson), 0o644); err != nil {
		t.Fatal(err)
	}
	if withDiff {
		diff := "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"
		if err := os.WriteFile(filepath.Join(bdir, "full.diff"), []byte(diff), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// mkLocal crafts a local bundle with the given findings.
func mkLocal(t *testing.T, dir, slug, findings string) {
	t.Helper()
	bdir := filepath.Join(dir, ".review", "local-"+slug)
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bdir, "source.json"), []byte(`{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","head_ref":"work"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	diff := "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"
	if err := os.WriteFile(filepath.Join(bdir, "full.diff"), []byte(diff), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bdir, "findings.json"), []byte(findings), 0o644); err != nil {
		t.Fatal(err)
	}
}

const (
	findingHeld  = `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Adopt me.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"important","kb":[],"sig":"0123456789abcdef0123456789abcdef01234567","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}`
	findingDrift = `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":99,"anchored":true,"body":"Drifted anchor here.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":[],"sig":"123456789abcdef0123456789abcdef012345678","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}`
)

// TestAdoptParity proves byte parity across adopt flags, refusals, and the
// re-anchor edge cases.
func TestAdoptParity(t *testing.T) {
	withPair := func(t *testing.T, dir, _ string) {
		t.Helper()
		mkTarget(t, dir, "4311", true)
		mkLocal(t, dir, "adopt1", "["+findingHeld+","+findingDrift+"]")
	}

	harness.RunBoth(t, "happy-reanchor", withPair, nil, false,
		"adopt", "acme/app", "4311", "--from", "adopt1", "--json")
	harness.RunBoth(t, "happy-human", withPair, nil, false,
		"adopt", "acme/app", "4311", "--from", "local-adopt1")
	harness.RunBoth(t, "bad-target", withPair, nil, false,
		"adopt", "acme/app", "local-foo", "--from", "adopt1", "--json")
	harness.RunBoth(t, "bad-from", withPair, nil, false,
		"adopt", "acme/app", "4312", "--json")
	harness.RunBoth(t, "missing-findings", withPair, nil, false,
		"adopt", "acme/app", "4313", "--from", "nope-missing", "--json")
	harness.RunBoth(t, "missing-diff", func(t *testing.T, dir, _ string) {
		t.Helper()
		mkTarget(t, dir, "4314", false)
		mkLocal(t, dir, "adopt1", "["+findingHeld+"]")
	}, nil, false,
		"adopt", "acme/app", "4314", "--from", "adopt1", "--json")
	harness.RunBoth(t, "provider-unimplemented", func(t *testing.T, dir, _ string) {
		t.Helper()
		mkTarget(t, dir, "4315", true)
		bdir := filepath.Join(dir, ".review", "4315")
		if err := os.WriteFile(filepath.Join(bdir, "PR.json"), []byte(`{"provider":"forgejo","head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		mkLocal(t, dir, "adopt1", "["+findingHeld+"]")
	}, nil, false,
		"adopt", "acme/app", "4315", "--from", "adopt1", "--json")
	harness.RunBoth(t, "corrupt-from-store", func(t *testing.T, dir, _ string) {
		t.Helper()
		mkTarget(t, dir, "4316", true)
		mkLocal(t, dir, "adoptbad", `[{"kind":"inline","sig":"nope"`)
	}, nil, false,
		"adopt", "acme/app", "4316", "--from", "adoptbad", "--json")
	harness.RunBoth(t, "unknown-flag", withPair, nil, false,
		"adopt", "acme/app", "4311", "--bogus", "--json")
}
