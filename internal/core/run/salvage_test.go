package run

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
)

const salvagedFinding = `{"schema_version":"1","findings":[{"path":"a.go","line":1,"severity":"important",` +
	`"claim":"a real defect","quote":"broken","confidence":"high"}]}`

const earlierFinding = `{"schema_version":"1","findings":[{"path":"old.go","line":9,"severity":"informational",` +
	`"claim":"earlier","quote":"old","confidence":"low"}]}`

const missingConfidence = `{"schema_version":"1","findings":[{"path":"a.go","line":1,"severity":"important",` +
	`"claim":"a real defect","quote":"broken"}]}`

const extraRootKey = `{"schema_version":"1","approved":true,"findings":[],` +
	`"summary":"Reviewed the diff; no defects found."}`

const placeholderReview = `{"schema_version":"1","findings":[]}`

const cleanReview = `{"schema_version":"1","summary":"Reviewed the diff; no defects found.","findings":[]}`

func fenced(doc string) string {
	return "prose before\n```json\n" + doc + "\n```\nprose after\n"
}

func runWithStdout(t *testing.T, stdout string) (*core.RunResponse, *core.RefusalError) {
	t.Helper()
	cmdr := &scripted{res: core.Result{ExitCode: 0, Stdout: stdout}}
	return Run(t.Context(), request(fixtureRoot(t), cmdr))
}

// runWithArtifact plants the initial file from inside the child callback, so
// its mtime follows the launch record and the freshness assertion sees it.
func runWithArtifact(t *testing.T, initial, stdout string) (string, *core.RunResponse, *core.RefusalError) {
	t.Helper()
	root := fixtureRoot(t)
	dir := bundleDir(root)
	cmdr := &scripted{res: core.Result{ExitCode: 0, Stdout: stdout}}
	if initial != "" {
		path := filepath.Join(dir, reviewFile)
		cmdr.write = func() {
			if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
				t.Error(err)
			}
		}
	}
	resp, ref := Run(t.Context(), request(root, cmdr))
	return dir, resp, ref
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

func assertArtifact(t *testing.T, dir, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, reviewFile))
	if err != nil {
		t.Fatalf("read %s: %v", filepath.ToSlash(filepath.Join(dir, reviewFile)), err)
	}
	if string(got) != want {
		t.Fatalf("review.json = %q, want %q", got, want)
	}
}

func hasProblemIn(ref *core.RefusalError, path, code string) bool {
	if ref == nil {
		return false
	}
	for _, p := range ref.Problems {
		if p.Path == path && p.Code == code {
			return true
		}
	}
	return false
}

func assertSalvageFlag(t *testing.T, dir string, want bool) {
	t.Helper()
	got, ok := readLauncher(t, dir)["salvaged"]
	if want && (!ok || got != true) {
		t.Fatalf("launcher salvaged = %v (present %v), want true", got, ok)
	}
	if !want && ok && got == true {
		t.Fatalf("launcher salvaged = true, want no salvage flag")
	}
}

// TestLastFindingsBlockPicksTheLastParsingBlock pins the scan order and the
// deliberately shallow shape gate: only a root object with a findings array
// qualifies, and a missing schema_version is the single byte this scanner adds.
func TestLastFindingsBlockPicksTheLastParsingBlock(t *testing.T) {
	t.Parallel()
	assertFindingsBlock(t, fenced(earlierFinding)+fenced(salvagedFinding), salvagedFinding)
	assertFindingsBlock(t, fenced(earlierFinding)+fenced(`{not json}`), earlierFinding)
	assertFindingsBlock(t, fenced(salvagedFinding)+fenced(`{"approved":true}`), salvagedFinding)
	assertFindingsBlock(t, fenced(salvagedFinding)+fenced(`{"findings":"no"}`), salvagedFinding)
	assertFindingsBlock(t, fenced(`{"findings":[]}`), `{"findings":[],"schema_version":"1"}`)
	assertFindingsBlock(t, "review.json is attached mentally\n", "")
	assertFindingsBlock(t, "```\n"+salvagedFinding+"\n```\n", "")
}

// TestLastFindingsBlockRestartsAfterUnclosedDraft proves a never-closed draft
// cannot swallow the valid block that follows it.
func TestLastFindingsBlockRestartsAfterUnclosedDraft(t *testing.T) {
	t.Parallel()
	draft := "draft:\n```json\n{\"findings\":[{\"path\":\"old.go\"\n"
	assertFindingsBlock(t, draft+fenced(salvagedFinding), salvagedFinding)
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
	assertArtifact(t, dir, salvagedFinding)
	if !resp.Data.Fresh {
		t.Fatal("salvaged review is not fresh")
	}
	if !slices.Contains(resp.Warnings, salvageWarning) {
		t.Errorf("warnings = %v, want %q", resp.Warnings, salvageWarning)
	}
	assertSalvageFlag(t, dir, true)
}

// TestRunSalvagesThenRefusesMissingConfidence proves lenient salvage and strict
// validation agree: the block is written, but the shared checker's problem
// reaches the refusal instead of a bare no-review-artifact.
func TestRunSalvagesThenRefusesMissingConfidence(t *testing.T) {
	dir, _, ref := runWithArtifact(t, "", fenced(missingConfidence))
	if ref == nil || ref.Code != recordcore.CodeBadReviewInput {
		t.Fatalf("refusal = %+v, want %s", ref, recordcore.CodeBadReviewInput)
	}
	if !hasProblemIn(ref, "$.findings[0].confidence", core.ProblemCodeMissingField) {
		t.Fatalf("problems = %+v, want findings[0].confidence", ref.Problems)
	}
	assertArtifact(t, dir, missingConfidence)
}

// TestRunRefusesUnknownReviewField pins the other half of F2: an extra root
// key is salvaged but never accepted, because record would refuse it too.
func TestRunRefusesUnknownReviewField(t *testing.T) {
	_, ref := runWithStdout(t, fenced(extraRootKey))
	if ref == nil || ref.Code != recordcore.CodeBadReviewInput {
		t.Fatalf("refusal = %+v, want %s", ref, recordcore.CodeBadReviewInput)
	}
	if !hasProblemIn(ref, "$.approved", core.ProblemCodeUnknownField) {
		t.Fatalf("problems = %+v, want $.approved", ref.Problems)
	}
}

// TestRunValidatesAgentWrittenReview proves validation is not a salvage-only
// branch: a direct write with the same extra key refuses identically.
func TestRunValidatesAgentWrittenReview(t *testing.T) {
	_, _, ref := runWithArtifact(t, extraRootKey, "")
	if ref == nil || ref.Code != recordcore.CodeBadReviewInput {
		t.Fatalf("refusal = %+v, want %s", ref, recordcore.CodeBadReviewInput)
	}
	if !hasProblemIn(ref, "$.approved", core.ProblemCodeUnknownField) {
		t.Fatalf("problems = %+v, want $.approved", ref.Problems)
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
	assertNoArtifactRefusal(t, fenced(`{"findings":"no"}`))
}

// TestFailedAgentNeverSalvages pins F1 in the one-shot path: a non-zero exit
// refuses on its own and leaves stdout's findings unrecorded.
func TestFailedAgentNeverSalvages(t *testing.T) {
	root := fixtureRoot(t)
	dir := bundleDir(root)
	cmdr := &scripted{res: core.Result{ExitCode: 1, Stdout: fenced(salvagedFinding)}}
	_, ref := Run(t.Context(), request(root, cmdr))
	if ref == nil || ref.Code != codeAgentFailed {
		t.Fatalf("refusal = %+v, want %s", ref, codeAgentFailed)
	}
	if _, err := os.Stat(filepath.Join(dir, reviewFile)); !os.IsNotExist(err) {
		t.Fatalf("failed agent left review.json: %v", err)
	}
	if _, ok := readLauncher(t, dir)["salvaged"]; ok {
		t.Error("launcher claims a salvage after a non-zero exit")
	}
}

// TestTimedOutAgentNeverSalvages pins F1's watchdog branch.
func TestTimedOutAgentNeverSalvages(t *testing.T) {
	root := fixtureRoot(t)
	dir := bundleDir(root)
	cmdr := &scripted{res: core.Result{TimedOut: true, Stdout: fenced(salvagedFinding)}}
	_, ref := Run(t.Context(), request(root, cmdr))
	if ref == nil || ref.Code != codeAgentTimeout {
		t.Fatalf("refusal = %+v, want %s", ref, codeAgentTimeout)
	}
	if _, err := os.Stat(filepath.Join(dir, reviewFile)); !os.IsNotExist(err) {
		t.Fatalf("timed-out agent left review.json: %v", err)
	}
}

// TestPlaceholderWithoutFindingsIsRefused proves the first-write document is
// not a finished clean review: the shared checker names $.summary.
func TestPlaceholderWithoutFindingsIsRefused(t *testing.T) {
	dir, _, ref := runWithArtifact(t, placeholderReview, "")
	if ref == nil || ref.Code != recordcore.CodeBadReviewInput {
		t.Fatalf("refusal = %+v, want %s", ref, recordcore.CodeBadReviewInput)
	}
	if !hasProblemIn(ref, "$.summary", core.ProblemCodeEmptyFindingsNeedsSummary) {
		t.Fatalf("problems = %+v, want the empty-findings summary problem", ref.Problems)
	}
	assertArtifact(t, dir, placeholderReview)
	assertSalvageFlag(t, dir, false)
}

// TestPlaceholderYieldsToPrintedFindings proves the one document salvage may
// replace: the exact placeholder, when stdout carries a findings object.
func TestPlaceholderYieldsToPrintedFindings(t *testing.T) {
	dir, resp, ref := runWithArtifact(t, placeholderReview, fenced(salvagedFinding))
	if ref != nil {
		t.Fatalf("Run refused: %v", ref)
	}
	assertArtifact(t, dir, salvagedFinding)
	assertSalvageFlag(t, dir, true)
	if !slices.Contains(resp.Warnings, salvageWarning) {
		t.Fatalf("warnings = %v, want %q", resp.Warnings, salvageWarning)
	}
}

// TestCleanReviewWithSummaryIsNeverReplaced keeps a finished clean review:
// adding a summary takes the file out of the placeholder exception.
func TestCleanReviewWithSummaryIsNeverReplaced(t *testing.T) {
	dir, resp, ref := runWithArtifact(t, cleanReview, fenced(salvagedFinding))
	if ref != nil {
		t.Fatalf("Run refused: %v", ref)
	}
	assertArtifact(t, dir, cleanReview)
	assertSalvageFlag(t, dir, false)
	if slices.Contains(resp.Warnings, salvageWarning) {
		t.Errorf("warnings = %v, want no salvage warning", resp.Warnings)
	}
}

// TestStaleArtifactIsNotSalvaged keeps the artifact the agent left alone:
// salvage only fills an absent file or the exact placeholder.
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
	assertArtifact(t, dir, reviewDoc)
	assertSalvageFlag(t, dir, false)
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
