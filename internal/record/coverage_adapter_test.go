package record_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core/coverage"
	"github.com/stainless-code/verdict/internal/golden"
	"github.com/stainless-code/verdict/internal/hunks"
)

// coverageSlug names the local bundle the adapter cases record into.
const coverageSlug = "local-cv"

// coverageDiff changes notes.txt across one hunk on both sides, so the
// evaluator unions OLD 1-3 with NEW 1-3 into three changed lines: `all`
// attests 3/3 and `1-2` leaves exactly `notes.txt:3` unattested.
const coverageDiff = "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n" +
	"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"

// coverageReview builds the one-comment review the adapter cases record, with
// extra as the top-level fragment carrying (or omitting) coverage[].
func coverageReview(extra string) string {
	return `{"schema_version":"1","body":"> [!IMPORTANT]\n> Adapter fixture body with enough length.","approved":false,` +
		extra + `"comments":[{"path":"notes.txt","line":2,"side":"RIGHT",` +
		`"body":"Cited finding here with enough substance.","rigor":"walked","severity":"important"}]}`
}

// coverageBundleDir is the bundle every adapter case reads and writes.
const coverageBundleDir = ".verdict/state/" + coverageSlug

// coverageFixture writes the local bundle the adapter cases run over and
// returns the sandbox dir. An empty review writes no review.json — the
// pre-review state `next --coverage` must tolerate — and a non-empty prior is
// the findings.json already recorded before the case runs.
func coverageFixture(t *testing.T, review, prior string) string {
	t.Helper()
	dir := t.TempDir()
	base := coverageBundleDir
	files := map[string]string{
		base + "/source.json": `{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",` +
			`"base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`,
		base + "/full.diff": coverageDiff,
		base + "/hunks.tsv": hunks.Format(hunks.Parse(coverageDiff)),
	}
	if review != "" {
		files[base+"/review.json"] = review
	}
	if prior != "" {
		files[base+"/findings.json"] = prior
	}
	writeFiles(t, dir, files)
	return dir
}

// runCoverage drives the built binary the way an operator does and returns its
// exit code, streams, and written tree.
func runCoverage(t *testing.T, review, prior string, args ...string) golden.Outputs {
	t.Helper()
	return runOne(t, coverageFixture(t, review, prior), t.TempDir(), args...)
}

// coverageEnvelope is the subset of a --json envelope the adapter cases grade:
// the refusal, the coverage payload next carries, and the warnings.
type coverageEnvelope struct {
	Error    *coverageEnvelopeError `json:"error"`
	Data     coverageEnvelopeData   `json:"data"`
	Warnings []string               `json:"warnings"`
}

// coverageEnvelopeError is the typed refusal an envelope carries.
type coverageEnvelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// coverageEnvelopeData is the payload the covered verb returns.
type coverageEnvelopeData struct {
	Coverage *coverageReceipt `json:"coverage"`
}

// coverageReceipt is the stored or returned coverage summary.
type coverageReceipt struct {
	Percent  *int `json:"percent"`
	Attested int  `json:"attested"`
	Total    int  `json:"total"`
	Files    int  `json:"files"`
	Exempt   int  `json:"exempt"`
	Gaps     int  `json:"gaps"`
}

// decodeEnvelope decodes one --json envelope from stdout.
func decodeEnvelope(t *testing.T, stdout string) coverageEnvelope {
	t.Helper()
	var env coverageEnvelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout is not one JSON envelope: %v\n%s", err, stdout)
	}
	return env
}

// writtenFile returns one file the run wrote, keyed by sandbox-relative path.
func writtenFile(t *testing.T, out golden.Outputs, path string) (string, bool) {
	t.Helper()
	raw, ok := out.Files["C/"+path]
	return raw, ok
}

// coverageWarnings keeps the envelope's coverage warnings, so a case grades
// the gate's own voice instead of every warning a sandbox run emits.
func coverageWarnings(warns []string) []string {
	var out []string
	for _, w := range warns {
		if strings.HasPrefix(w, "coverage:") {
			out = append(out, w)
		}
	}
	return out
}

// TestRecordAdapterRefusesCoverageIncomplete pins criteria 1 and 2 at the
// operator's surface: `record --require-coverage` exits 1 with the typed
// coverage-incomplete refusal and writes neither store.
func TestRecordAdapterRefusesCoverageIncomplete(t *testing.T) {
	cases := []struct {
		name    string
		review  string
		wantMsg string
	}{
		{name: "absent", review: coverageReview(""), wantMsg: "no coverage[] attestation"},
		{name: "partial", review: coverageReview(`"coverage":[{"file":"notes.txt","ranges":"1-2"}],`), wantMsg: "notes.txt:3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertCoverageRefusal(t, tc.review, tc.wantMsg)
		})
	}
}

// TestRecordAdapterRefusalKeepsThePriorStore pins criterion 2's write half at
// the operator's surface: the refusal leaves the recorded findings untouched.
func TestRecordAdapterRefusalKeepsThePriorStore(t *testing.T) {
	prior := "[]\n"
	out := runCoverage(t, coverageReview(`"coverage":[{"file":"notes.txt","ranges":"1-2"}],`), prior,
		"record", "acme/app", coverageSlug, "--require-coverage")
	if out.Status != 1 {
		t.Fatalf("exit = %d, want 1\nstderr:\n%s", out.Status, out.Stderr)
	}
	if !strings.Contains(out.Stderr, "error: coverage-incomplete:") {
		t.Errorf("stderr misses the typed refusal:\n%s", out.Stderr)
	}
	if got, _ := writtenFile(t, out, coverageBundleDir+"/findings.json"); got != prior {
		t.Errorf("prior findings.json = %q, want it unchanged", got)
	}
}

// TestRecordAdapterCompleteUnderGate pins criterion 3 at the operator's
// surface: a 100% attestation exits 0 under the gate and stores the receipt.
func TestRecordAdapterCompleteUnderGate(t *testing.T) {
	out := runCoverage(t, coverageReview(`"coverage":[{"file":"notes.txt","ranges":"all"}],`), "",
		"record", "acme/app", coverageSlug, "--require-coverage", "--json")
	if out.Status != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", out.Status, out.Stderr)
	}
	if env := decodeEnvelope(t, out.Stdout); env.Error != nil {
		t.Fatalf("unexpected refusal: %+v", env.Error)
	}
	receipt := storedReceipt(t, out)
	if receipt.Percent == nil || *receipt.Percent != 100 {
		t.Errorf("stored percent = %v, want 100", receipt.Percent)
	}
}

// TestRecordAdapterDefaultPostureWarnsOnce pins criterion 6 at the operator's
// surface: without the flag a partial attestation exits 0 and warns once.
func TestRecordAdapterDefaultPostureWarnsOnce(t *testing.T) {
	out := runCoverage(t, coverageReview(`"coverage":[{"file":"notes.txt","ranges":"1-2"}],`), "",
		"record", "acme/app", coverageSlug, "--json")
	if out.Status != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", out.Status, out.Stderr)
	}
	env := decodeEnvelope(t, out.Stdout)
	want := "coverage: 66% of " + coverage.Basis + " attested (2/3; 1 file(s) with gaps)"
	if got := coverageWarnings(env.Warnings); len(got) != 1 || got[0] != want {
		t.Errorf("coverage warnings = %v, want exactly [%q]", got, want)
	}
	receipt := storedReceipt(t, out)
	if receipt.Percent == nil || *receipt.Percent != 66 || receipt.Gaps != 1 {
		t.Errorf("stored coverage = %+v, want percent 66 with one gap", receipt)
	}
}

// storedReceipt decodes the coverage block record.json stores.
func storedReceipt(t *testing.T, out golden.Outputs) coverageReceipt {
	t.Helper()
	raw, ok := writtenFile(t, out, coverageBundleDir+"/record.json")
	if !ok {
		t.Fatal("the run wrote no record.json")
	}
	var record struct {
		Coverage coverageReceipt `json:"coverage"`
	}
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		t.Fatalf("decode record.json: %v\n%s", err, raw)
	}
	return record.Coverage
}

// TestNextAdapterCoverageReadsBeforeRecord pins criterion 9 at the operator's
// surface: `next --coverage` answers over a bundle with no findings.json, in
// both human and JSON mode.
func TestNextAdapterCoverageReadsBeforeRecord(t *testing.T) {
	review := coverageReview(`"coverage":[{"file":"notes.txt","ranges":"1-2"}],`)
	assertCoverageHuman(t, review)
	assertCoverageMachine(t, review)
}

// TestNextAdapterCoverageMissingReview pins criterion 9's pre-review leg: with
// no review.json the read reports 0% and still exits 0.
func TestNextAdapterCoverageMissingReview(t *testing.T) {
	out := runCoverage(t, "", "", "next", "acme/app", coverageSlug, "--coverage")
	if out.Status != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", out.Status, out.Stderr)
	}
	want := "coverage: 0% of full hunk spans, both sides and context — no review.json yet"
	if !strings.Contains(out.Stdout, want) {
		t.Errorf("stdout misses %q:\n%s", want, out.Stdout)
	}
}

// TestNextAdapterCoverageStatusConflict pins criterion 9's usage error: one
// mode picks a finding and the other a coverage read, so combining them is an
// argv error (exit 2), never a silent precedence rule.
func TestNextAdapterCoverageStatusConflict(t *testing.T) {
	out := runCoverage(t, coverageReview(""), "", "next", "acme/app", coverageSlug, "--coverage", "--status", "open")
	if out.Status != 2 {
		t.Fatalf("exit = %d, want 2\nstdout:\n%s\nstderr:\n%s", out.Status, out.Stdout, out.Stderr)
	}
	if !strings.Contains(out.Stderr, "--coverage and --status are mutually exclusive") {
		t.Errorf("stderr misses the usage error:\n%s", out.Stderr)
	}
	if out.Stdout != "" {
		t.Errorf("a usage error wrote stdout: %q", out.Stdout)
	}
}

// TestCoverageAdapterFixturePinsHunkRows pins the fixture's hunk snapshot
// literally: the gate cases grade a percentage, so a hand-edited snapshot that
// drifts from the diff would move the expectation without moving the fixture.
func TestCoverageAdapterFixturePinsHunkRows(t *testing.T) {
	dir := coverageFixture(t, coverageReview(""), "")
	raw, err := os.ReadFile(dir + "/" + coverageBundleDir + "/hunks.tsv")
	if err != nil {
		t.Fatalf("read fixture hunks: %v", err)
	}
	want := "notes.txt\tOLD\t1\t3\nnotes.txt\tNEW\t1\t3\n"
	if string(raw) != want {
		t.Errorf("fixture hunks = %q, want %q", raw, want)
	}
}

// assertCoverageHuman pins the human leg of the pre-record read: the measured
// line and its gap, and no findings store touched on the way.
func assertCoverageHuman(t *testing.T, review string) {
	t.Helper()
	human := runCoverage(t, review, "", "next", "acme/app", coverageSlug, "--coverage")
	if human.Status != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", human.Status, human.Stderr)
	}
	// The basis is graded here too: a reader who takes the number as a share of
	// changed lines under-reports the hunks they actually attested.
	for _, want := range []string{"coverage: 66% of full hunk spans, both sides and context (2/3 lines, 1 files)", "unattested:", "- notes.txt:3"} {
		if !strings.Contains(human.Stdout, want) {
			t.Errorf("stdout misses %q:\n%s", want, human.Stdout)
		}
	}
	for path := range human.Files {
		if strings.HasSuffix(path, "/findings.json") {
			t.Errorf("the read mode wrote or needed a findings store: %s", path)
		}
	}
}

// assertCoverageMachine pins the same read as a --json envelope.
func assertCoverageMachine(t *testing.T, review string) {
	t.Helper()
	machine := runCoverage(t, review, "", "next", "acme/app", coverageSlug, "--coverage", "--json")
	if machine.Status != 0 {
		t.Fatalf("exit = %d, want 0\nstderr:\n%s", machine.Status, machine.Stderr)
	}
	rep := decodeEnvelope(t, machine.Stdout).Data.Coverage
	if rep == nil || rep.Percent == nil || *rep.Percent != 66 {
		t.Fatalf("data.coverage = %+v, want percent 66", rep)
	}
	if rep.Attested != 2 || rep.Total != 3 {
		t.Errorf("attested/total = %d/%d, want 2/3", rep.Attested, rep.Total)
	}
}

// assertCoverageRefusal pins one refused gate: exit 1, the typed code, a
// message naming the gap, and no store written on the way out.
func assertCoverageRefusal(t *testing.T, review, wantMsg string) {
	t.Helper()
	out := runCoverage(t, review, "", "record", "acme/app", coverageSlug, "--require-coverage", "--json")
	if out.Status != 1 {
		t.Fatalf("exit = %d, want 1\nstderr:\n%s", out.Status, out.Stderr)
	}
	env := decodeEnvelope(t, out.Stdout)
	if env.Error == nil || env.Error.Code != "coverage-incomplete" {
		t.Fatalf("error = %+v, want coverage-incomplete\n%s", env.Error, out.Stdout)
	}
	if !strings.Contains(env.Error.Message, wantMsg) {
		t.Errorf("message = %q, want it to carry %q", env.Error.Message, wantMsg)
	}
	for _, path := range []string{coverageBundleDir + "/findings.json", coverageBundleDir + "/record.json"} {
		if _, ok := writtenFile(t, out, path); ok {
			t.Errorf("a refused run wrote %s", path)
		}
	}
}
