//go:build unix

package backtest

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/testenv"
)

// The launch tests stay unix-only: the fake agent is a POSIX PATH entry — a
// symlink to this test binary named `claude`, which Windows' LookPath never
// resolves. Every test that spawns an agent lives in this file.

// The refusal codes a replay row carries verbatim.
const (
	agentFailedCode  = "agent-failed"
	agentMissingCode = "agent-missing"
	badRangeCode     = "bad-range"
	agentTimeoutCode = "agent-timeout"
	checkoutFailCode = "backtest-failed"
	runMismatchCode  = "run-mismatch"
	canceledCode     = "canceled"
	statusOKCode     = "ok"
	stopCompleteCode = "complete"
	modeReplayCode   = "replay"
)

// replayRowPRs is the newest-first PR order twoCandidateRepo mines: #2 merged
// last, so it is the first candidate replayed.
var replayRowPRs = []int{2, 1}

// rewriteMine lands the fixture's candidate list after mutate changed it.
func (f *replayFixture) rewriteMine(t *testing.T, mutate func(*MineDoc)) {
	t.Helper()
	doc := f.mine
	doc.Candidates = slices.Clone(f.mine.Candidates)
	mutate(&doc)
	f.mine = doc
	f.writeMine(t, replayTestRun, &doc)
}

// editCandidate applies mutate to the mined candidate that names pr.
func editCandidate(t *testing.T, doc *MineDoc, pr int, mutate func(*Candidate)) {
	t.Helper()
	for i := range doc.Candidates {
		if doc.Candidates[i].PR != nil && *doc.Candidates[i].PR == pr {
			mutate(&doc.Candidates[i])
			return
		}
	}
	t.Fatalf("the fixture mined no PR #%d", pr)
}

// repeatCandidates pads the mined list to n rows by repeating the first.
func repeatCandidates(doc *MineDoc, n int) {
	for len(doc.Candidates) < n {
		doc.Candidates = append(doc.Candidates, doc.Candidates[0])
	}
}

// runReplayOK expects a completed run.
func runReplayOK(t *testing.T, req *ReplayRequest) *ReplayResponse {
	t.Helper()
	resp, ref := RunReplay(t.Context(), req)
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	return resp
}

// readTestFile reads one artifact a run wrote.
func readTestFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// bundlePath is one replay bundle under the store.
func (f *replayFixture) bundlePath(n int) string {
	return filepath.Join(f.repo.dir, ".verdict", "state", "local-bt-"+strconv.Itoa(n)+"-"+replayTestRun)
}

// prPath is one candidate's resume record.
func (f *replayFixture) prPath(n int) string {
	return filepath.Join(f.repo.dir, ".verdict", "backtest", replayTestRun, "prs",
		fmt.Sprintf("%03d", n), "replay.json")
}

// plant makes the fake agent the claude the launcher resolves, in mode, with
// price as the cost it reports (empty writes no cost.json; a comma-separated
// list prices the launches in order).
func (f *replayFixture) plant(t *testing.T, mode, price string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve the test binary: %v", err)
	}
	bin := t.TempDir()
	if err := os.Symlink(exe, filepath.Join(bin, "claude")); err != nil {
		t.Fatalf("link the fake agent: %v", err)
	}
	t.Setenv(fakeAgentEnv, mode)
	t.Setenv(fakeAgentCostEnv, price)
	t.Setenv(fakeAgentLogEnv, f.log)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// pathWithoutAgents leaves git resolvable and every agent absent.
func (f *replayFixture) pathWithoutAgents(t *testing.T) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is not installed: %v", err)
	}
	dir := t.TempDir()
	if err := os.Symlink(git, filepath.Join(dir, "git")); err != nil {
		t.Fatalf("link git: %v", err)
	}
	t.Setenv("PATH", dir)
}

// launches counts the agent invocations the fake logged.
func (f *replayFixture) launches(t *testing.T) int {
	t.Helper()
	raw, err := os.ReadFile(f.log)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.FieldsFunc(string(raw), func(r rune) bool { return r == '\n' }))
}

// launchLines splits the fake agent's log into its per-launch rows.
func (f *replayFixture) launchLines(t *testing.T) [][]string {
	t.Helper()
	raw, err := os.ReadFile(f.log)
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	out := make([][]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, strings.Split(row, "\t"))
	}
	return out
}

// candidateHead is the head the miner recorded for one PR: the resolution the
// replay must check out.
func (f *replayFixture) candidateHead(t *testing.T, pr int) string {
	t.Helper()
	for i := range f.mine.Candidates {
		if f.mine.Candidates[i].PR != nil && *f.mine.Candidates[i].PR == pr {
			return f.mine.Candidates[i].Head
		}
	}
	t.Fatalf("the fixture mined no PR #%d", pr)
	return ""
}

// storedPR reads one candidate's resume record.
func storedPR(t *testing.T, f *replayFixture, n int) ReplayPRDoc {
	t.Helper()
	var doc ReplayPRDoc
	if err := json.Unmarshal([]byte(readTestFile(t, f.prPath(n))), &doc); err != nil {
		t.Fatalf("stored row %d: %v", n, err)
	}
	return doc
}

// TestReplayRunsEachCandidateAtItsHistoricalHead proves the replay builds each
// candidate's own base..head bundle, launches the agent in a detached checkout
// of that head, records the review, and removes the checkout again.
func TestReplayRunsEachCandidateAtItsHistoricalHead(t *testing.T) {
	f := newReplayFixture(t)
	f.plant(t, "write", "0.6")
	resp := runReplayOK(t, f.request(t, nil))
	if len(resp.Warnings) != 0 {
		t.Errorf("warnings = %q; a clean replay warns about nothing", resp.Warnings)
	}
	checkReplayData(t, resp.Data)
	checkHistoricalBundles(t, f)
	checkReplayCheckouts(t, f)
}

// checkReplayData compares the envelope rows: newest merge first, priced, ok.
func checkReplayData(t *testing.T, data *ReplayData) {
	t.Helper()
	if data.Mode != modeReplayCode || data.Run != replayTestRun || data.Agent != "claude" {
		t.Fatalf("data = mode %q run %q agent %q", data.Mode, data.Run, data.Agent)
	}
	if data.StopReason != stopCompleteCode {
		t.Fatalf("stop reason = %q, want %q", data.StopReason, stopCompleteCode)
	}
	if len(data.PRs) != len(replayRowPRs) {
		t.Fatalf("rows = %d, want the two mined candidates", len(data.PRs))
	}
	for i, wantPR := range replayRowPRs {
		checkReplayRow(t, data.PRs[i], i+1, wantPR)
	}
	if data.Budgets.SpentUSD == nil || *data.Budgets.SpentUSD != 1.2 || data.Budgets.Unpriced != 0 {
		t.Errorf("budgets = %+v; want 1.2 spent over two priced reviews", data.Budgets)
	}
}

// checkReplayRow compares one row's position, PR number, bundle, and price.
func checkReplayRow(t *testing.T, row ReplayPR, n, wantPR int) {
	t.Helper()
	if row.N != n || row.PR == nil || *row.PR != wantPR || row.Status != statusOKCode {
		t.Errorf("row %d = %+v; want n=%d pr=%d status=ok", n, row, n, wantPR)
	}
	if want := "local-bt-" + strconv.Itoa(n) + "-" + replayTestRun; row.Bundle != want {
		t.Errorf("row %d bundle = %q, want %q", n, row.Bundle, want)
	}
	if row.CostUSD == nil || *row.CostUSD != 0.6 {
		t.Errorf("row %d cost = %v, want the reported 0.6", n, row.CostUSD)
	}
}

// checkHistoricalBundles proves each bundle carries the candidate's own diff,
// is marked as replay input, and holds the review the record path merged.
func checkHistoricalBundles(t *testing.T, f *replayFixture) {
	t.Helper()
	checkBundleDiff(t, f.bundlePath(1), "b/b.txt", "a/a.txt")
	checkBundleDiff(t, f.bundlePath(2), "a/a.txt", "+fixed")
	checkMarker(t, f)
	checkFindings(t, f.bundlePath(1), "b.txt")
	checkFindings(t, f.bundlePath(2), "a.txt")
}

// checkBundleDiff proves one bundle holds want and not unwanted: candidate 1 is
// the b.txt PR, candidate 2 the a.txt one, and the later fix is in neither.
func checkBundleDiff(t *testing.T, dir, want, unwanted string) {
	t.Helper()
	diff := readTestFile(t, filepath.Join(dir, "full.diff"))
	if !strings.Contains(diff, want) || strings.Contains(diff, unwanted) {
		t.Errorf("%s diff does not hold %q alone:\n%s", dir, want, diff)
	}
}

// checkMarker proves the marker names this replay and its candidate.
func checkMarker(t *testing.T, f *replayFixture) {
	t.Helper()
	var marker MarkerDoc
	if err := json.Unmarshal([]byte(readTestFile(t, filepath.Join(f.bundlePath(1), "backtest.json"))), &marker); err != nil {
		t.Fatalf("marker: %v", err)
	}
	if marker.Mode != modeReplayCode || marker.Run != replayTestRun || marker.N != 1 || marker.PR == nil || *marker.PR != 2 {
		t.Errorf("marker = %+v", marker)
	}
}

// checkFindings proves the recorded review anchored on the bundle's own path.
func checkFindings(t *testing.T, dir, path string) {
	t.Helper()
	findings := readTestFile(t, filepath.Join(dir, "findings.json"))
	if !strings.Contains(findings, `"path": "`+path+`"`) {
		t.Errorf("%s findings do not anchor %s:\n%s", dir, path, findings)
	}
}

// checkReplayCheckouts proves the clean run left no checkout behind and that
// each launch ran inside the candidate's own checkout with the bundle as its
// target.
func checkReplayCheckouts(t *testing.T, f *replayFixture) {
	t.Helper()
	checkNoLeftoverCheckouts(t, f)
	lines := f.launchLines(t)
	if len(lines) != len(replayRowPRs) {
		t.Fatalf("launches = %d, want one per candidate", len(lines))
	}
	for i, line := range lines {
		checkLaunch(t, f, i, line)
	}
}

// checkNoLeftoverCheckouts proves git kept no replay worktree and no checkout
// directory survives under the state directory.
func checkNoLeftoverCheckouts(t *testing.T, f *replayFixture) {
	t.Helper()
	out := f.repo.git("worktree", "list", "--porcelain")
	if got := strings.Count(out, "worktree "); got != 1 {
		t.Errorf("git still knows %d worktrees; want only the fixture:\n%s", got, out)
	}
	entries, err := os.ReadDir(filepath.Join(f.repo.dir, ".verdict", "state"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".worktree-bt-") {
			t.Errorf("leftover checkout %s", entry.Name())
		}
	}
}

// checkLaunch compares one launch's cwd, artifact, and checkout head against
// the candidate it replayed.
func checkLaunch(t *testing.T, f *replayFixture, i int, line []string) {
	t.Helper()
	wantPR := replayRowPRs[i]
	if want := ".worktree-bt-" + replayTestRun + "-" + strconv.Itoa(i+1); filepath.Base(line[0]) != want {
		t.Errorf("launch %d cwd = %q, want the detached checkout %q", i, line[0], want)
	}
	wantArtifact, err := filepath.EvalSymlinks(filepath.Join(f.bundlePath(i+1), "review.json"))
	if err != nil {
		t.Fatalf("resolve the planted artifact: %v", err)
	}
	if line[1] != wantArtifact {
		t.Errorf("launch %d artifact = %q, want %q", i, line[1], wantArtifact)
	}
	if want := f.candidateHead(t, wantPR); line[3] != want {
		t.Errorf("launch %d checked out %q; PR #%d must be read at its historical head %q", i, line[3], wantPR, want)
	}
}

// TestReplayLongRunIDsKeepDistinctBundles proves the two longest ids that
// differ only in their last byte build distinct bundles: at the cap the store
// truncates nothing, so neither run can land on the other's diff.
func TestReplayLongRunIDsKeepDistinctBundles(t *testing.T) {
	f := newReplayFixture(t)
	f.plant(t, "write", "0.6")
	stem := strings.Repeat("a", 41)
	seen := map[string]string{}
	for _, id := range []string{stem + "x", stem + "y"} {
		f.writeMine(t, id, &f.mine)
		data := runReplayOK(t, f.request(t, func(r *ReplayRequest) { r.Run = id })).Data
		want := "local-bt-1-" + id
		if got := data.PRs[0].Bundle; got != want {
			t.Errorf("run %q bundle = %q, want %q", id, got, want)
		}
		if other, ok := seen[data.PRs[0].Bundle]; ok {
			t.Errorf("runs %q and %q share bundle %q", other, id, data.PRs[0].Bundle)
		}
		seen[data.PRs[0].Bundle] = id
	}
}

// TestReplayRefusesTheRunIDsThatWouldShareABundle proves the pair whose bundle
// names differ only past the store's cap is refused before either is built:
// accepting both would let the second overwrite the first's diff, and resuming
// the first would report ok against the second's.
func TestReplayRefusesTheRunIDsThatWouldShareABundle(t *testing.T) {
	prefix := strings.Repeat("a", 43)
	for _, id := range []string{prefix + "x", prefix + "y"} {
		f := newReplayFixture(t)
		f.writeMine(t, id, &f.mine)
		ref := runReplayRefusal(t, f.request(t, func(r *ReplayRequest) { r.Run = id }))
		if ref.Code != "bad-slug" || !strings.Contains(ref.Message, "at most 42 bytes") {
			t.Errorf("run %q refused %s: %s; want bad-slug naming the 42-byte cap", id, ref.Code, ref.Message)
		}
		checkNothingBuilt(t, f)
	}
}

// TestReplayResumeSkipsRecordedPRs proves a rerun replays nothing that already
// recorded a review and reports the same rows.
func TestReplayResumeSkipsRecordedPRs(t *testing.T) {
	f := newReplayFixture(t)
	f.plant(t, "write", "0.6")
	first := runReplayOK(t, f.request(t, nil)).Data
	if got := f.launches(t); got != 2 {
		t.Fatalf("first run launches = %d, want 2", got)
	}
	second := runReplayOK(t, f.request(t, nil)).Data
	if got := f.launches(t); got != 2 {
		t.Errorf("rerun launches = %d; a recorded PR must not replay", got)
	}
	if jq.Pretty(first.PRs) != jq.Pretty(second.PRs) {
		t.Errorf("rerun rows = %s, want the recorded %s", jq.Pretty(second.PRs), jq.Pretty(first.PRs))
	}
	if second.Budgets.SpentUSD == nil || math.Abs(*second.Budgets.SpentUSD-1.2) > 1e-9 {
		t.Errorf("rerun spend = %v, want the recorded 1.2", second.Budgets.SpentUSD)
	}
}

// TestReplayResumeRefusesAnotherAgentOrPassThrough proves a recorded PR is not
// silently rewritten when the operator changes what would produce it.
func TestReplayResumeRefusesAnotherAgentOrPassThrough(t *testing.T) {
	tests := []struct {
		mutate func(*ReplayRequest)
		name   string
	}{
		{mutate: func(r *ReplayRequest) { r.Agent = "codex" }, name: "agent"},
		{mutate: func(r *ReplayRequest) { r.PassThrough = []string{"--model", "other"} }, name: "pass-through"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { checkResumeMismatch(t, tc.mutate) })
	}
}

// checkResumeMismatch proves the rerun refuses and names the recorded agent.
func checkResumeMismatch(t *testing.T, mutate func(*ReplayRequest)) {
	t.Helper()
	f := newReplayFixture(t)
	f.plant(t, "write", "0.6")
	runReplayOK(t, f.request(t, nil))
	ref := runReplayRefusal(t, f.request(t, mutate))
	if ref.Code != runMismatchCode {
		t.Fatalf("code = %q, want %q", ref.Code, runMismatchCode)
	}
	if !strings.Contains(ref.Message, "with agent 'claude'") {
		t.Errorf("refusal does not name the recorded agent: %q", ref.Message)
	}
}

// TestReplayResumeRefusesAReminedCandidate proves a stored row is bound to the
// candidate's resolution: a re-mined head or base refuses run-mismatch instead
// of resuming the earlier price.
func TestReplayResumeRefusesAReminedCandidate(t *testing.T) {
	tests := []struct {
		mutate func(*Candidate)
		name   string
	}{
		{name: "head", mutate: func(c *Candidate) { c.Head = c.Base }},
		{name: "base", mutate: func(c *Candidate) { c.Base = c.Head }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { checkReminedMismatch(t, tc.mutate) })
	}
}

// checkReminedMismatch proves the re-mined resolution refuses and launches
// nothing.
func checkReminedMismatch(t *testing.T, mutate func(*Candidate)) {
	t.Helper()
	f := newReplayFixture(t)
	f.plant(t, "write", "0.6")
	runReplayOK(t, f.request(t, nil))
	f.rewriteMine(t, func(doc *MineDoc) { editCandidate(t, doc, replayRowPRs[0], mutate) })
	ref := runReplayRefusal(t, f.request(t, nil))
	if ref.Code != runMismatchCode {
		t.Fatalf("code = %q, want %q (%s)", ref.Code, runMismatchCode, ref.Message)
	}
	if got := f.launches(t); got != 2 {
		t.Errorf("launches = %d; a refused resume must not launch again", got)
	}
}

// TestReplayResumeRedoesALostBundle proves a stored `ok` is not trusted when
// the recorded bundle or its findings.json is gone: the row is replayed.
func TestReplayResumeRedoesALostBundle(t *testing.T) {
	tests := []struct {
		lose func(t *testing.T, bundle string)
		name string
	}{
		{name: "bundle", lose: func(t *testing.T, bundle string) {
			t.Helper()
			if err := os.RemoveAll(bundle); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "findings", lose: func(t *testing.T, bundle string) {
			t.Helper()
			if err := os.Remove(filepath.Join(bundle, "findings.json")); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { checkRedone(t, tc.lose) })
	}
}

// checkRedone proves the row whose artifact vanished launches again and lands a
// fresh findings.json.
func checkRedone(t *testing.T, lose func(*testing.T, string)) {
	t.Helper()
	f := newReplayFixture(t)
	f.plant(t, "write", "0.6")
	runReplayOK(t, f.request(t, nil))
	lose(t, f.bundlePath(1))
	data := runReplayOK(t, f.request(t, nil)).Data
	if got := f.launches(t); got != 3 {
		t.Errorf("launches = %d; a lost bundle must be replayed again", got)
	}
	if data.PRs[0].Status != statusOKCode || data.PRs[0].CostUSD == nil {
		t.Errorf("redone row = %+v, want a recorded priced review", data.PRs[0])
	}
	if _, err := os.Stat(filepath.Join(f.bundlePath(1), "findings.json")); err != nil {
		t.Errorf("the redone review recorded nothing: %v", err)
	}
}

// TestReplayStopsAtMaxReviews proves the cap bounds the number of agent runs
// and that the stop reason says so.
func TestReplayStopsAtMaxReviews(t *testing.T) {
	f := newReplayFixture(t)
	f.plant(t, "write", "0.6")
	data := runReplayOK(t, f.request(t, func(r *ReplayRequest) { r.MaxReviewsRaw = "1" })).Data
	if data.StopReason != stopMaxReviews {
		t.Errorf("stop reason = %q, want %q", data.StopReason, stopMaxReviews)
	}
	if len(data.PRs) != 1 || f.launches(t) != 1 {
		t.Errorf("rows = %d launches = %d; want exactly the capped one", len(data.PRs), f.launches(t))
	}
}

// TestReplayStopsWhenTheNextReviewWouldCrossTheBudget proves the projection
// stops the run before a review that would pass the ceiling, and reports the
// spend that already happened.
func TestReplayStopsWhenTheNextReviewWouldCrossTheBudget(t *testing.T) {
	f := newReplayFixture(t)
	f.plant(t, "write", "0.6")
	data := runReplayOK(t, f.request(t, func(r *ReplayRequest) { r.BudgetRaw = "1" })).Data
	if data.StopReason != stopBudgetUSD {
		t.Errorf("stop reason = %q, want %q", data.StopReason, stopBudgetUSD)
	}
	if len(data.PRs) != 1 || f.launches(t) != 1 {
		t.Errorf("rows = %d launches = %d; want one review then the stop", len(data.PRs), f.launches(t))
	}
	if data.Budgets.SpentUSD == nil || *data.Budgets.SpentUSD != 0.6 {
		t.Errorf("spent = %v, want the 0.6 the one review reported", data.Budgets.SpentUSD)
	}
}

// TestReplayStopsOnTheMedianProjection proves the budget projects the median of
// the priced reviews, not the largest: 0.1 and 0.5 project 0.3 and let the next
// review run, while the maximum would stop the walk a row early.
func TestReplayStopsOnTheMedianProjection(t *testing.T) {
	f := newReplayFixture(t)
	f.rewriteMine(t, func(doc *MineDoc) { repeatCandidates(doc, 4) })
	f.plant(t, "write", "0.1,0.5,1.0")
	data := runReplayOK(t, f.request(t, func(r *ReplayRequest) { r.BudgetRaw = "1" })).Data
	if data.StopReason != stopBudgetUSD {
		t.Errorf("stop reason = %q, want %q", data.StopReason, stopBudgetUSD)
	}
	if len(data.PRs) != 3 || f.launches(t) != 3 {
		t.Errorf("rows = %d launches = %d; the median must let the third review run", len(data.PRs), f.launches(t))
	}
	if data.Budgets.SpentUSD == nil || *data.Budgets.SpentUSD != 1.6 {
		t.Errorf("spent = %v, want the 1.6 the three reviews reported", data.Budgets.SpentUSD)
	}
}

// TestReplayWatchdogTimeoutIsAgentTimeout proves --timeout is run's watchdog: a
// review that outlives it is that candidate's agent-timeout row, and the walk
// continues.
func TestReplayWatchdogTimeoutIsAgentTimeout(t *testing.T) {
	f := newReplayFixture(t)
	f.plant(t, "sleep", "")
	data := runReplayOK(t, f.request(t, func(r *ReplayRequest) { r.TimeoutRaw = "1" })).Data
	if data.StopReason != stopCompleteCode || len(data.PRs) != len(replayRowPRs) {
		t.Fatalf("stop reason = %q rows = %d; want a timeout row per candidate", data.StopReason, len(data.PRs))
	}
	for i, row := range data.PRs {
		if row.Status != agentTimeoutCode {
			t.Errorf("row %d status = %q, want %q", i+1, row.Status, agentTimeoutCode)
		}
	}
	if detail := storedPR(t, f, 1).Detail; !strings.Contains(detail, "ran past the 1s watchdog and was killed") {
		t.Errorf("row 1 detail = %q, want the watchdog's kill message", detail)
	}
}

// TestReplayRecordsPerPRFailureAndRetriesIt proves one agent failure is that
// PR's row, not the run's, that the next invocation retries it, and that both
// attempts remove their checkout.
func TestReplayRecordsPerPRFailureAndRetriesIt(t *testing.T) {
	f := newReplayFixture(t)
	f.plant(t, "exit", "")
	failed := runReplayOK(t, f.request(t, nil)).Data
	checkFailedRun(t, f, failed)
	checkNoLeftoverCheckouts(t, f)
	f.plant(t, "write", "0.6")
	checkRetriedRun(t, f, runReplayOK(t, f.request(t, nil)).Data)
}

// checkFailedRun proves the failed reviews are rows with unknown spend, and
// that each one's resume record carries the failure for the retry.
func checkFailedRun(t *testing.T, f *replayFixture, failed *ReplayData) {
	t.Helper()
	if failed.StopReason != stopCompleteCode {
		t.Errorf("stop reason = %q; a failed review is not the run's refusal", failed.StopReason)
	}
	if len(failed.PRs) != len(replayRowPRs) {
		t.Fatalf("rows = %d, want one per candidate", len(failed.PRs))
	}
	for i, row := range failed.PRs {
		checkFailedRow(t, i, row)
	}
	if failed.Budgets.SpentUSD != nil || failed.Budgets.Unpriced != 2 {
		t.Errorf("budgets = %+v; two unpriced failures are unknown spend, not zero", failed.Budgets)
	}
	checkStoredFailure(t, f)
}

// checkStoredFailure proves the resume record carries the failure and its
// detail, so the retry knows what to redo.
func checkStoredFailure(t *testing.T, f *replayFixture) {
	t.Helper()
	if stored := storedPR(t, f, 1); stored.Status != agentFailedCode || stored.Detail == "" {
		t.Errorf("stored row = %+v; want the failure and its detail", stored)
	}
}

// checkFailedRow proves one failure kept its bundle and reported no price.
func checkFailedRow(t *testing.T, i int, row ReplayPR) {
	t.Helper()
	if row.Status != agentFailedCode {
		t.Errorf("row %d status = %q, want %q", i, row.Status, agentFailedCode)
	}
	if row.CostUSD != nil || row.Bundle == "" {
		t.Errorf("row %d = %+v; want its bundle kept and no price", i, row)
	}
}

// checkRetriedRun proves the second invocation re-ran the failed reviews.
func checkRetriedRun(t *testing.T, f *replayFixture, retried *ReplayData) {
	t.Helper()
	if got := f.launches(t); got != 4 {
		t.Errorf("launches = %d; want the two failures and the two retries", got)
	}
	for i, row := range retried.PRs {
		if row.Status != statusOKCode || row.CostUSD == nil {
			t.Errorf("retried row %d = %+v; want a recorded priced review", i, row)
		}
	}
}

// TestReplayPerPRCheckoutFailureContinues proves a candidate whose head does
// not resolve is one failed row, with no bundle and no checkout left behind,
// and the run goes on.
func TestReplayPerPRCheckoutFailureContinues(t *testing.T) {
	f := newReplayFixture(t)
	f.rewriteMine(t, func(doc *MineDoc) {
		editCandidate(t, doc, replayRowPRs[0], func(c *Candidate) {
			c.Head = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
		})
	})
	f.plant(t, "write", "0.6")
	data := runReplayOK(t, f.request(t, nil)).Data
	if data.StopReason != stopCompleteCode {
		t.Errorf("stop reason = %q, want the run to continue past one bad candidate", data.StopReason)
	}
	if len(data.PRs) != len(replayRowPRs) {
		t.Fatalf("rows = %d, want both candidates", len(data.PRs))
	}
	if data.PRs[0].Status != checkoutFailCode || data.PRs[0].Bundle != "" {
		t.Errorf("row 1 = %+v; want a %s row with no bundle", data.PRs[0], checkoutFailCode)
	}
	if data.PRs[1].Status != statusOKCode {
		t.Errorf("row 2 = %+v; want the second candidate to replay", data.PRs[1])
	}
	checkNoLeftoverCheckouts(t, f)
}

// TestReplayPerPRBundleFailureContinues proves a candidate whose range cannot
// be resolved is one failed row, that its checkout is removed, and the run goes
// on.
func TestReplayPerPRBundleFailureContinues(t *testing.T) {
	f := newReplayFixture(t)
	f.rewriteMine(t, func(doc *MineDoc) {
		editCandidate(t, doc, replayRowPRs[0], func(c *Candidate) {
			c.Base = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
		})
	})
	f.plant(t, "write", "0.6")
	data := runReplayOK(t, f.request(t, nil)).Data
	if data.StopReason != stopCompleteCode {
		t.Errorf("stop reason = %q, want the run to continue past one bad candidate", data.StopReason)
	}
	if len(data.PRs) != len(replayRowPRs) {
		t.Fatalf("rows = %d, want both candidates", len(data.PRs))
	}
	if data.PRs[0].Status != badRangeCode || data.PRs[0].Bundle != "" {
		t.Errorf("row 1 = %+v; want a %s row with no bundle", data.PRs[0], badRangeCode)
	}
	if data.PRs[1].Status != statusOKCode {
		t.Errorf("row 2 = %+v; want the second candidate to replay", data.PRs[1])
	}
	checkNoLeftoverCheckouts(t, f)
}

// gitGone answers every command until the replay reaches a `git worktree add`,
// then reports git missing. A real PATH cannot reach that branch: the root
// probe already resolved git, so only a git that vanishes mid-run does.
type gitGone struct {
	inner core.Commander
	fired bool
}

// Run implements core.Commander.
func (g *gitGone) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	if slices.Contains(cmd.Argv, gitWorktree) && slices.Contains(cmd.Argv, gitWorktreeAdd) {
		g.fired = true
		return core.Result{}, &core.MissingToolError{Tool: "git"}
	}
	return g.inner.Run(ctx, cmd)
}

// TestReplayMissingGitStopsTheWalk proves a git that disappears mid-run ends
// the run with the install hint, not a failed row per candidate.
func TestReplayMissingGitStopsTheWalk(t *testing.T) {
	f := newReplayFixture(t)
	f.plant(t, "write", "0.6")
	cmdr := &gitGone{inner: coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true}}
	ref := runReplayRefusal(t, f.request(t, func(r *ReplayRequest) { r.Runtime.Commander = cmdr }))
	if !cmdr.fired {
		t.Fatal("the replay never reached the checkout")
	}
	if ref.Code != checkoutFailCode || !strings.Contains(ref.Message, "needs git on PATH") {
		t.Fatalf("refusal = %s: %s; want the missing-git stop", ref.Code, ref.Message)
	}
	if got := f.launches(t); got != 0 {
		t.Errorf("launches = %d; a run without git must not start an agent", got)
	}
}

// TestReplayCancelReportsTheReplayVerb proves every cancel during a replay
// names the replay verb, and that a cancel in flight still removes the
// checkout.
func TestReplayCancelReportsTheReplayVerb(t *testing.T) {
	t.Run("pre-flight", checkCancelBeforeTheWalk)
	t.Run("during the checkout", checkCancelDuringTheCheckout)
	t.Run("during the review", checkCancelDuringTheReview)
	t.Run("before the record", checkCancelBeforeTheRecord)
	t.Run("between reviews", checkCancelBetweenReviews)
}

// checkCancelBeforeTheWalk cancels on the root probe and expects the replay
// cancel text, not the miner's.
func checkCancelBeforeTheWalk(t *testing.T) {
	t.Helper()
	f := newReplayFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmdr := &cancelAfter{
		inner: coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true}, cancel: cancel,
		stop: func(cmd *core.Command) bool { return slices.Contains(cmd.Argv, "--show-toplevel") },
	}
	_, ref := RunReplay(ctx, f.request(t, func(r *ReplayRequest) { r.Runtime.Commander = cmdr }))
	checkReplayCancel(t, cmdr, ref)
}

// checkCancelDuringTheCheckout cancels while git is creating the detached
// checkout: the half-created tree and its administrative entry are dropped
// before the cancel is reported.
func checkCancelDuringTheCheckout(t *testing.T) {
	t.Helper()
	f := newReplayFixture(t)
	f.plant(t, "write", "0.6")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmdr := &cancelAfter{
		inner: coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true}, cancel: cancel,
		stop: func(cmd *core.Command) bool {
			return slices.Contains(cmd.Argv, gitWorktree) && slices.Contains(cmd.Argv, gitWorktreeAdd)
		},
	}
	_, ref := RunReplay(ctx, f.request(t, func(r *ReplayRequest) { r.Runtime.Commander = cmdr }))
	checkReplayCancel(t, cmdr, ref)
	if got := f.launches(t); got != 0 {
		t.Errorf("launches = %d; a canceled checkout must not start an agent", got)
	}
	checkNoLeftoverCheckouts(t, f)
}

// checkCancelDuringTheReview cancels while the agent is running: the launcher
// reports the cancel, the row never lands, and the checkout is still removed.
func checkCancelDuringTheReview(t *testing.T) {
	t.Helper()
	f := newReplayFixture(t)
	f.plant(t, "sleep", "")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req := f.request(t, nil)
	done := make(chan *core.RefusalError, 1)
	go func() {
		_, ref := RunReplay(ctx, req)
		done <- ref
	}()
	testenv.Until(t, func() bool { return f.launches(t) > 0 })
	cancel()
	var ref *core.RefusalError
	testenv.Until(t, func() bool {
		select {
		case ref = <-done:
			return true
		default:
			return false
		}
	})
	checkReplayCancelText(t, ref)
	if got := f.launches(t); got != 1 {
		t.Errorf("launches = %d; the canceled review is the only one", got)
	}
	checkNoLeftoverCheckouts(t, f)
}

// agentRan notes whether the agent launch reached the commander, so a cancel
// can fire on the first clock read after it.
type agentRan struct {
	inner core.Commander
	ran   bool
}

// Run records the agent invocation, then delegates.
func (a *agentRan) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	if len(cmd.Argv) > 0 && filepath.Base(cmd.Argv[0]) == "claude" {
		a.ran = true
	}
	return a.inner.Run(ctx, cmd)
}

// checkCancelBeforeTheRecord cancels between the agent's exit and the record
// step: the launcher's closing clock read is the last seam before the merge, so
// the cancel lands there. The walk reports the replay's own cancel and no row
// names the record verb.
func checkCancelBeforeTheRecord(t *testing.T) {
	t.Helper()
	f := newReplayFixture(t)
	f.plant(t, "write", "0.6")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmdr := &agentRan{inner: coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true}}
	req := f.request(t, func(r *ReplayRequest) { r.Runtime.Commander = cmdr })
	clock := req.Runtime.Now
	req.Runtime.Now = func() time.Time {
		if cmdr.ran {
			cancel()
		}
		return clock()
	}
	_, ref := RunReplay(ctx, req)
	checkReplayCancelText(t, ref)
	if got := f.launches(t); got != 1 {
		t.Errorf("launches = %d; the cancel lands after the first review", got)
	}
	if _, err := os.Stat(filepath.Join(f.repo.dir, ".verdict", "backtest", replayTestRun, "prs")); !os.IsNotExist(err) {
		t.Errorf("the canceled record left a resume row naming the record verb: %v", err)
	}
	checkNoLeftoverCheckouts(t, f)
}

// checkCancelBetweenReviews cancels at the first row's own cleanup: the row
// lands, the walk reports the cancel, and the checkout is gone.
func checkCancelBetweenReviews(t *testing.T) {
	t.Helper()
	f := newReplayFixture(t)
	f.plant(t, "write", "0.6")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmdr := &cancelAfter{
		inner: coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true}, cancel: cancel,
		stop: func(cmd *core.Command) bool {
			return slices.Contains(cmd.Argv, "remove") && pathExists(cmd.Argv[len(cmd.Argv)-1])
		},
	}
	_, ref := RunReplay(ctx, f.request(t, func(r *ReplayRequest) { r.Runtime.Commander = cmdr }))
	checkReplayCancel(t, cmdr, ref)
	if got := f.launches(t); got != 1 {
		t.Errorf("launches = %d; the cancel lands after the first review", got)
	}
	checkNoLeftoverCheckouts(t, f)
}

// checkReplayCancel proves the commander fired and the refusal is the replay
// cancel.
func checkReplayCancel(t *testing.T, cmdr *cancelAfter, ref *core.RefusalError) {
	t.Helper()
	if !cmdr.fired {
		t.Fatal("the cancel never fired")
	}
	checkReplayCancelText(t, ref)
}

// checkReplayCancelText pins the frozen replay cancel message.
func checkReplayCancelText(t *testing.T, ref *core.RefusalError) {
	t.Helper()
	if ref == nil || ref.Code != canceledCode {
		t.Fatalf("refusal = %+v; want canceled", ref)
	}
	if ref.Message != "canceled before backtest replay completed" {
		t.Errorf("cancel message = %q, want the replay verb", ref.Message)
	}
}

// pathExists reports whether path is on disk: the cancel test aims at a
// checkout that exists, so it skips the empty drop before the add.
func pathExists(path string) bool {
	_, err := os.Stat(filepath.FromSlash(path))
	return err == nil
}

// TestReplayMissingAgentStopsTheRun proves an uninstalled agent ends the run
// with the launcher's own code instead of failing every candidate in turn.
func TestReplayMissingAgentStopsTheRun(t *testing.T) {
	f := newReplayFixture(t)
	f.pathWithoutAgents(t)
	ref := runReplayRefusal(t, f.request(t, nil))
	if ref.Code != agentMissingCode {
		t.Fatalf("code = %q, want %q (%s)", ref.Code, agentMissingCode, ref.Message)
	}
	if _, err := os.Stat(filepath.Join(f.repo.dir, ".verdict", "backtest", replayTestRun, "prs")); !os.IsNotExist(err) {
		t.Errorf("a stopped run wrote resume records: %v", err)
	}
}

// TestReplayUnpricedReviewReportsUnknownSpend proves a review that reported no
// price is unknown spend, never zero dollars.
func TestReplayUnpricedReviewReportsUnknownSpend(t *testing.T) {
	f := newReplayFixture(t)
	f.plant(t, "write", "")
	data := runReplayOK(t, f.request(t, nil)).Data
	if data.Budgets.SpentUSD != nil || data.Budgets.Unpriced != 2 {
		t.Errorf("budgets = %+v; want two unpriced reviews and no total", data.Budgets)
	}
	if !slices.Contains(f.rec.lines, "spend: unknown over 2 reviews (all unpriced)") {
		t.Errorf("human lines = %q; want the unknown spend footer", f.rec.lines)
	}
}

// TestReplayPassThroughReachesTheAgent proves the operator's `--` arguments end
// up in the agent's own argv, ahead of the directive.
func TestReplayPassThroughReachesTheAgent(t *testing.T) {
	f := newReplayFixture(t)
	f.plant(t, "write", "0.6")
	runReplayOK(t, f.request(t, func(r *ReplayRequest) { r.PassThrough = []string{"--model", "fast"} }))
	lines := f.launchLines(t)
	if !strings.Contains(lines[0][2], "claude -p --model fast") {
		t.Errorf("agent argv = %q; want the pass-through after the agent's own flags", lines[0][2])
	}
}

// TestReplayHumanOutput pins the human contract: the plan header, one row per
// candidate, the stop reason, the spend, and the not-citable footer.
func TestReplayHumanOutput(t *testing.T) {
	f := newReplayFixture(t)
	f.plant(t, "write", "0.6")
	runReplayOK(t, f.request(t, nil))
	want := []string{
		"backtest replay ra  (agent claude, max 10 reviews, no budget, timeout 300s)",
		"  1  #2  local-bt-1-ra  ok  $0.60",
		"  2  #1  local-bt-2-ra  ok  $0.60",
		"stopped: complete",
		"spend: $1.20 over 2 reviews",
		"not citable — SZZ-derived: a replay grades a blame-derived guess, not a verified defect",
	}
	if !slices.Equal(f.rec.lines, want) {
		t.Errorf("human lines =\n%s\nwant\n%s", strings.Join(f.rec.lines, "\n"), strings.Join(want, "\n"))
	}
}
