// Package triage_test holds the golden contract for the Go triage verb:
// the shared harness runs the Go binary on hermetic fixtures and pins
// exit codes, stdout/stderr, and every written file against checked-in
// goldens,
// including the kb credit joins.
package triage_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/golden"
)

var harness *golden.Harness

// TestMain builds the Go binary once and runs the golden suite.
func TestMain(m *testing.M) {
	os.Exit(golden.RunMain(m, &harness))
}

// mkBundle crafts a GitHub-shaped PR bundle with canned findings.
func mkBundle(t *testing.T, dir, findings string) {
	t.Helper()
	bdir := filepath.Join(dir, ".verdict", "state", "4251")
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	pr := `{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","author":{"login":"teammate"}}`
	if err := os.WriteFile(filepath.Join(bdir, "PR.json"), []byte(pr), 0o644); err != nil {
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

// kbSeed writes a kb store with the cited rule.
func kbSeed(t *testing.T, dir string) {
	t.Helper()
	row := `{"id":"await-err","rule":"Check error paths on every await","scope":"src/*.ts","provenance":"manual","rigor":"walked","hits":0,"false_hits":0,"judged":0,"status":"active"}` + "\n"
	if err := os.MkdirAll(filepath.Join(dir, ".verdict"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".verdict", "kb.jsonl"), []byte(row), 0o644); err != nil {
		t.Fatal(err)
	}
}

const (
	findingCited = `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Unhandled await error path here.","suggestion":null,"inBounds":true,"rigor":"walked","severity":"important","kb":["await-err"],"sig":"0123456789abcdef0123456789abcdef01234567","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}`
	findingPlain = `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,"anchored":true,"body":"Second comment, no cites.","suggestion":null,"inBounds":true,"rigor":"pointed","severity":"informational","kb":[],"sig":"123456789abcdef0123456789abcdef012345678","sig_version":"1","firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}`
)

// setupKB builds the two-finding bundle plus the kb store.
func setupKB(t *testing.T, dir, _ string) {
	t.Helper()
	mkBundle(t, dir, "["+findingCited+","+findingPlain+"]")
	kbSeed(t, dir)
}

// setupPlain builds the two-finding bundle without kb state.
func setupPlain(t *testing.T, dir, _ string) {
	t.Helper()
	mkBundle(t, dir, "["+findingCited+","+findingPlain+"]")
}

// setupAmbiguous builds two findings whose sigs share the "1234" prefix,
// so a prefix resolve must report MANY instead of picking one.
func setupAmbiguous(t *testing.T, dir, _ string) {
	t.Helper()
	sigA := strings.Replace(findingCited,
		"0123456789abcdef0123456789abcdef01234567",
		"1234567890abcdef0123456789abcdef01234567", 1)
	mkBundle(t, dir, "["+sigA+","+findingPlain+"]")
}

// setupCorrupt builds a bundle with a truncated findings store.
func setupCorrupt(t *testing.T, dir, _ string) {
	t.Helper()
	mkBundle(t, dir, findingCited[:40])
}

// setupMissing builds an empty bundle dir with no findings store.
func setupMissing(t *testing.T, dir, _ string) {
	t.Helper()
	bdir := filepath.Join(dir, ".verdict", "state", "4251")
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
}

// setupLocalBundle builds a local-* bundle with one cited finding plus kb.
func setupLocalBundle(t *testing.T, dir, _ string) {
	t.Helper()
	bdir := filepath.Join(dir, ".verdict", "state", "local-k6")
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bdir, "source.json"), []byte(`{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	diff := "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"
	if err := os.WriteFile(filepath.Join(bdir, "full.diff"), []byte(diff), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bdir, "findings.json"), []byte("["+findingCited+"]"), 0o644); err != nil {
		t.Fatal(err)
	}
	kbSeed(t, dir)
}

// TestTriageGolden proves byte parity across triage flags, refusals, and
// edge cases (credit, idempotency, dry-run, corrupt stores).
func TestTriageGolden(t *testing.T) {
	sig1 := "0123456789abcdef0123456789abcdef01234567"
	sig2 := "123456789abcdef0123456789abcdef012345678"

	runTriageCreditCases(t, sig1, sig2)
	runTriageRefusalCases(t, sig1)
	runTriageLocalCase(t, sig1)
}

// runTriageCreditCases covers successful flips, credit, and dry-run output.
func runTriageCreditCases(t *testing.T, sig1, sig2 string) {
	t.Helper()
	golden.Pin(t, harness, "addressed-credits", setupKB, nil,
		"triage", "acme/app", "4251", "--sig", sig1, "--status", "addressed", "--rationale", "verified the await path now handles errors, fix confirmed", "--json")
	golden.Pin(t, harness, "false-positive-credits", setupKB, nil,
		"triage", "acme/app", "4251", "--sig", sig1, "--status", "false-positive", "--rationale", "re-read twice, this rule misfired completely here now okay", "--json")
	golden.Pin(t, harness, "dry-run-writes-nothing", setupKB, nil,
		"triage", "acme/app", "4251", "--sig", sig2, "--status", "false-positive", "--rationale", "dry run should not touch any counters at all here", "--dry-run", "--json")
	golden.Pin(t, harness, "human-mode", setupKB, nil,
		"triage", "acme/app", "4251", "--sig", sig1, "--status", "deferred", "--rationale", "deferring this until the next pass for more evidence here")
	golden.Pin(t, harness, "force-short-rationale", setupKB, nil,
		"triage", "acme/app", "4251", "--sig", sig1, "--status", "wont-fix", "--force", "--json")
}

// runTriageRefusalCases covers every typed refusal and the unknown flag.
func runTriageRefusalCases(t *testing.T, sig1 string) {
	t.Helper()
	golden.Pin(t, harness, "unknown-sig", setupPlain, nil,
		"triage", "acme/app", "4251", "--sig", "deadbeef", "--status", "addressed", "--rationale", "verified the await path now handles errors, fix confirmed", "--json")
	golden.Pin(t, harness, "ambiguous-sig", setupAmbiguous, nil,
		"triage", "acme/app", "4251", "--sig", "1234", "--status", "addressed", "--rationale", "verified the await path now handles errors, fix confirmed", "--json")
	golden.Pin(t, harness, "bad-triage-status", setupPlain, nil,
		"triage", "acme/app", "4251", "--sig", sig1, "--status", "bogus", "--rationale", "verified the await path now handles errors, fix confirmed", "--json")
	golden.Pin(t, harness, "rationale-required", setupPlain, nil,
		"triage", "acme/app", "4251", "--sig", sig1, "--status", "addressed", "--rationale", "short", "--json")
	golden.Pin(t, harness, "missing-findings", setupMissing, nil,
		"triage", "acme/app", "4251", "--sig", sig1, "--status", "addressed", "--rationale", "verified the await path now handles errors, fix confirmed", "--json")
	golden.Pin(t, harness, "corrupt-store", setupCorrupt, nil,
		"triage", "acme/app", "4251", "--sig", sig1, "--status", "addressed", "--rationale", "verified the await path now handles errors, fix confirmed", "--json")
	golden.Pin(t, harness, "unknown-flag", setupPlain, nil,
		"triage", "acme/app", "4251", "--bogus", "--json")
}

// runTriageLocalCase covers the local-bundle flip with kb credit.
func runTriageLocalCase(t *testing.T, sig1 string) {
	t.Helper()
	golden.Pin(t, harness, "local-bundle", setupLocalBundle, nil,
		"triage", "acme/app", "local-k6", "--sig", sig1, "--status", "addressed", "--rationale", "human confirms the finding on the local bundle here sure", "--json")
}
