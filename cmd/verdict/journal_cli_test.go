package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	journalcore "github.com/stainless-code/verdict/internal/core/journal"
)

func seedCLIBundleRepo(t *testing.T) (repoDir, bundleDir, stateDir string) {
	t.Helper()
	repoDir = t.TempDir()
	stateDir = t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)
	gitIn(t, repoDir, "init", "-q", "-b", "main")
	writeIn(t, filepath.Join(repoDir, "a.go"), "package a\n\nfunc f() int { return 1 }\n")
	gitIn(t, repoDir, "add", "-A")
	gitIn(t, repoDir, "commit", "-qm", "init")
	writeIn(t, filepath.Join(repoDir, "a.go"), "package a\n\nfunc f() int { return 2 }\n")
	t.Chdir(repoDir)
	code, _, stderr := runCLIForJournalTest(t, []string{"fetch-local", "acme/app", "cli-test", "--json"})
	if code != 0 {
		t.Fatalf("fetch-local exit=%d stderr=%s", code, stderr)
	}
	bundleDir = filepath.Join(repoDir, ".verdict", "state", "local-cli-test")
	writeIn(t, filepath.Join(bundleDir, "review.json"),
		`{"schema_version":"1","findings":[],"spec_checks":[],"receipts":[],"summary":"clean review"}`+"\n")
	return repoDir, bundleDir, stateDir
}

func runCLIForJournalTest(t *testing.T, args []string) (code int, stdout, stderr string) {
	t.Helper()
	outF, err := os.CreateTemp(t.TempDir(), "stdout-*")
	if err != nil {
		t.Fatal(err)
	}
	errF, err := os.CreateTemp(t.TempDir(), "stderr-*")
	if err != nil {
		_ = outF.Close()
		t.Fatal(err)
	}
	code = run(args, outF, errF, os.Getenv, os.ReadFile)
	_ = outF.Close()
	_ = errF.Close()
	outBytes, err := os.ReadFile(outF.Name())
	if err != nil {
		t.Fatal(err)
	}
	errBytes, err := os.ReadFile(errF.Name())
	if err != nil {
		t.Fatal(err)
	}
	return code, string(outBytes), string(errBytes)
}

func TestCLIRecordDryRunDeterminismAndReadOnlySilence(t *testing.T) {
	repoDir, bundleDir, stateDir := seedCLIBundleRepo(t)
	provBefore := mustReadBytes(t, filepath.Join(bundleDir, "provenance.json"))
	runBefore := mustReadBytes(t, filepath.Join(bundleDir, "run.json"))

	code1, out1, err1 := runCLIForJournalTest(t, []string{"record", "acme/app", "local-cli-test", "--dry-run", "--json"})
	if code1 != 0 {
		t.Fatalf("record --dry-run (1) exit=%d stderr=%s out=%s", code1, err1, out1)
	}
	provAfter1 := mustReadBytes(t, filepath.Join(bundleDir, "provenance.json"))
	runAfter1 := mustReadBytes(t, filepath.Join(bundleDir, "run.json"))

	code2, out2, err2 := runCLIForJournalTest(t, []string{"record", "acme/app", "local-cli-test", "--dry-run", "--json"})
	if code2 != 0 {
		t.Fatalf("record --dry-run (2) exit=%d stderr=%s out=%s", code2, err2, out2)
	}
	provAfter2 := mustReadBytes(t, filepath.Join(bundleDir, "provenance.json"))
	runAfter2 := mustReadBytes(t, filepath.Join(bundleDir, "run.json"))

	assertBytesEqual(t, "provenance.json (before vs after1)", provBefore, provAfter1)
	assertBytesEqual(t, "provenance.json (after1 vs after2)", provAfter1, provAfter2)
	assertBytesEqual(t, "run.json (before vs after1)", runBefore, runAfter1)
	assertBytesEqual(t, "run.json (after1 vs after2)", runAfter1, runAfter2)
	assertNoBundleJournalArtifacts(t, bundleDir)
	jpath := mustExternalJournalPath(t, repoDir, stateDir, "local-cli-test")
	assertCLIStepsRecorded(t, jpath, out2)
	assertGateAndShowWriteNoJournal(t, jpath)
}

func assertNoBundleJournalArtifacts(t *testing.T, bundleDir string) {
	t.Helper()
	for _, name := range []string{"journal.jsonl", "journal.jsonl.lock"} {
		if _, err := os.Stat(filepath.Join(bundleDir, name)); err == nil {
			t.Fatalf("found unexpected %s in bundle dir %s", name, bundleDir)
		}
	}
}

func mustExternalJournalPath(t *testing.T, repoDir, stateDir, bundleName string) string {
	t.Helper()
	jpath, _, ok := journalcore.Path(repoDir, bundleName, map[string]string{"XDG_STATE_HOME": stateDir})
	if !ok {
		t.Fatalf("journal.Path(%q, %q) returned ok=false", repoDir, bundleName)
	}
	return jpath
}

func assertCLIStepsRecorded(t *testing.T, jpath, recordOut string) {
	t.Helper()
	sum, err := journalcore.Read(jpath, "local-cli-test")
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	assertCLIVerbsAndChain(t, sum)
	var env struct {
		Next string `json:"next"`
	}
	if err := json.Unmarshal([]byte(recordOut), &env); err != nil {
		t.Fatalf("unmarshal record envelope: %v", err)
	}
	if sum.Steps[2].Next != env.Next {
		t.Fatalf("recorded next = %q, want envelope next %q", sum.Steps[2].Next, env.Next)
	}
}

func assertCLIVerbsAndChain(t *testing.T, sum *journalcore.Summary) {
	t.Helper()
	if len(sum.Steps) != 3 {
		t.Fatalf("len(Steps) = %d, want 3 (fetch-local + 2 record --dry-run)", len(sum.Steps))
	}
	if sum.Steps[0].Verb != "fetch-local" || sum.Steps[1].Verb != "record" || sum.Steps[2].Verb != "record" {
		t.Fatalf("verbs = [%s %s %s], want [fetch-local record record]",
			sum.Steps[0].Verb, sum.Steps[1].Verb, sum.Steps[2].Verb)
	}
	// Writing review.json directly between fetch-local and record #1 is an
	// out-of-band edit (1 chain break on step 1), while record #1 -> record #2
	// has no break.
	if len(sum.ChainBreaks) != 1 || sum.ChainBreaks[0].Step != sum.Steps[1].Step || len(sum.Unfinished) != 0 {
		t.Fatalf("breaks=%+v unfinished=%+v, want single break on step %s", sum.ChainBreaks, sum.Unfinished, sum.Steps[1].Step)
	}
}

func assertGateAndShowWriteNoJournal(t *testing.T, jpath string) {
	t.Helper()
	if code, _, stderr := runCLIForJournalTest(t, []string{"record", "acme/app", "local-cli-test", "--json"}); code != 0 {
		t.Fatalf("record exit=%d stderr=%s", code, stderr)
	}
	if err := os.Remove(jpath); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runCLIForJournalTest(t, []string{"gate", "acme/app", "local-cli-test", "--max-severity", "critical", "--json"}); code != 0 && code != 1 {
		t.Fatalf("gate exit=%d stderr=%s, want 0 or 1", code, stderr)
	}
	if _, err := os.Stat(jpath); err == nil {
		t.Fatal("gate wrote journal; read-only verbs must never write a journal")
	}
	_, _, _ = runCLIForJournalTest(t, []string{"show", "acme/app", "local-cli-test", "deadbeef", "--json"})
	if _, err := os.Stat(jpath); err == nil {
		t.Fatal("show wrote journal; read-only verbs must never write a journal")
	}
}

func TestCLIJournalAppendFailureWarnsAndSucceeds(t *testing.T) {
	repoDir, _, stateDir := seedCLIBundleRepo(t)
	jpath := mustExternalJournalPath(t, repoDir, stateDir, "local-cli-test")
	if err := os.Remove(jpath); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeIn(t, outside, "keep")
	if err := os.Symlink(outside, jpath); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runCLIForJournalTest(t, []string{"record", "acme/app", "local-cli-test", "--dry-run", "--json"})
	if code != 0 {
		t.Fatalf("record --dry-run exit=%d stderr=%s, want 0 despite journal failure", code, stderr)
	}
	if !strings.Contains(stderr, "warn: journal: could not append to ") {
		t.Fatalf("stderr = %q, want frozen journal append warning", stderr)
	}
}

func mustReadBytes(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}

func assertBytesEqual(t *testing.T, label string, a, b []byte) {
	t.Helper()
	if !bytes.Equal(a, b) {
		t.Fatalf("%s changed:\nbefore=%s\nafter=%s", label, a, b)
	}
}
