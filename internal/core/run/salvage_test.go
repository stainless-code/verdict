package run

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

const salvagedFinding = `{"schema_version":"1","findings":[{"path":"a.go","line":1,"severity":"important",` +
	`"claim":"a real defect","quote":"broken","confidence":"high"}]}`

const earlierFinding = `{"schema_version":"1","findings":[{"path":"old.go","line":9,"severity":"informational",` +
	`"claim":"earlier","quote":"old","confidence":"low"}]}`

func fenced(doc string) string {
	return "prose before\n```json\n" + doc + "\n```\nprose after\n"
}

func assertFindingsBlock(t *testing.T, stdout, want string) {
	t.Helper()
	got, ok := lastFindingsBlock(stdout)
	if want == "" {
		if ok {
			t.Fatalf("block = %q, want no parsing block", got)
		}
		return
	}
	if !ok || string(got) != want {
		t.Fatalf("block = %q (%v), want %q", got, ok, want)
	}
}

// TestLastFindingsBlockPicksTheLastParsingBlock pins the scan order: a later
// malformed block never hides an earlier document, and two documents pick the
// later one.
func TestLastFindingsBlockPicksTheLastParsingBlock(t *testing.T) {
	t.Parallel()
	assertFindingsBlock(t, fenced(earlierFinding)+fenced(salvagedFinding), salvagedFinding)
	assertFindingsBlock(t, fenced(earlierFinding)+fenced(`{not json}`), earlierFinding)
	assertFindingsBlock(t, fenced(salvagedFinding)+fenced(`{"approved":true}`), salvagedFinding)
	assertFindingsBlock(t, "review.json is attached mentally\n", "")
	assertFindingsBlock(t, "```\n"+salvagedFinding+"\n```\n", "")
}

// TestRunSalvagesStdoutFindings proves the whole path: no artifact, a fenced
// document on stdout, a fresh review.json, a recorded flag, and a warning.
func TestRunSalvagesStdoutFindings(t *testing.T) {
	root := fixtureRoot(t)
	dir := bundleDir(root)
	cmdr := &scripted{res: core.Result{ExitCode: 0, Stdout: fenced(salvagedFinding)}}
	resp, ref := Run(t.Context(), request(root, cmdr))
	if ref != nil {
		t.Fatalf("Run refused: %v", ref)
	}
	artifact := filepath.Join(dir, reviewFile)
	got, err := os.ReadFile(artifact)
	if err != nil {
		t.Fatalf("read %s: %v", filepath.ToSlash(artifact), err)
	}
	if string(got) != salvagedFinding {
		t.Fatalf("salvaged review = %q, want %q", got, salvagedFinding)
	}
	if !resp.Data.Fresh {
		t.Fatal("salvaged review is not fresh")
	}
	if !slices.Contains(resp.Warnings, salvageWarning) {
		t.Errorf("warnings = %v, want %q", resp.Warnings, salvageWarning)
	}
	if saved, ok := readLauncher(t, dir)["salvaged"]; !ok || saved != true {
		t.Errorf("launcher salvaged = %v (present %v), want true", saved, ok)
	}
}

func assertNoArtifactRefusal(t *testing.T, stdout string) {
	t.Helper()
	root := fixtureRoot(t)
	dir := bundleDir(root)
	cmdr := &scripted{res: core.Result{ExitCode: 0, Stdout: stdout}}
	_, ref := Run(t.Context(), request(root, cmdr))
	if ref == nil || ref.Code != codeNoArtifact {
		t.Fatalf("refusal = %+v, want %s", ref, codeNoArtifact)
	}
	if slices.Contains(ref.Warnings, salvageWarning) {
		t.Errorf("warnings = %v, want no salvage warning", ref.Warnings)
	}
	if _, err := os.Stat(filepath.Join(dir, reviewFile)); !os.IsNotExist(err) {
		t.Errorf("review.json exists after this stdout: %v", err)
	}
	if _, ok := readLauncher(t, dir)["salvaged"]; ok {
		t.Error("launcher claims a salvage that did not happen")
	}
}

// TestRunKeepsNoReviewArtifactForBadStdout proves neither a missing nor a
// malformed block becomes an artifact or a salvage claim.
func TestRunKeepsNoReviewArtifactForBadStdout(t *testing.T) {
	assertNoArtifactRefusal(t, "I finished the review in my head.\n")
	assertNoArtifactRefusal(t, "```json\n{not json}\n```\n")
	assertNoArtifactRefusal(t, "```json\n"+`{"approved":true}`+"\n```\n")
	assertNoArtifactRefusal(t, fenced(`{"schema_version":"1","findings":"no"}`))
	assertNoArtifactRefusal(t, fenced(`{"schema_version":"1","findings":[{"path":"a.go"}]}`))
}

// TestStaleArtifactIsNotSalvaged keeps the artifact the agent left alone:
// salvage only fills an absent file, never a stale one.
func TestStaleArtifactIsNotSalvaged(t *testing.T) {
	root := fixtureRoot(t)
	dir := bundleDir(root)
	stale := filepath.Join(dir, reviewFile)
	if err := os.WriteFile(stale, []byte(reviewDoc), 0o600); err != nil {
		t.Fatal(err)
	}
	cmdr := &scripted{res: core.Result{ExitCode: 0, Stdout: fenced(salvagedFinding)}}
	_, ref := Run(t.Context(), request(root, cmdr))
	if ref == nil || ref.Code != codeStaleArtifact {
		t.Fatalf("refusal = %+v, want %s", ref, codeStaleArtifact)
	}
	got, err := os.ReadFile(stale)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != reviewDoc {
		t.Errorf("stale review was overwritten: %q", got)
	}
	if _, ok := readLauncher(t, dir)["salvaged"]; ok {
		t.Error("launcher claims salvage over a stale artifact")
	}
}

// TestDrainNeverSalvagesAReview keeps the drain contract separate: a findings
// block on a drain agent's stdout is not a replies artifact.
func TestDrainNeverSalvagesAReview(t *testing.T) {
	root, dir := drainBundle(t)
	cmdr := &scripted{res: core.Result{ExitCode: 0, Stdout: fenced(salvagedFinding)}}
	_, ref := Drain(t.Context(), drainRequest(root, cmdr), "digest")
	if ref == nil || ref.Code != codeNoArtifact {
		t.Fatalf("drain refusal = %+v, want %s", ref, codeNoArtifact)
	}
	if _, err := os.Stat(filepath.Join(dir, reviewFile)); !os.IsNotExist(err) {
		t.Errorf("drain salvaged a review.json: %v", err)
	}
	if strings.Contains(ref.Message, salvageWarning) {
		t.Errorf("drain refusal carried a review salvage warning: %q", ref.Message)
	}
}
