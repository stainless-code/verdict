//go:build unix

package backtest

import (
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
)

// replayTestRun is the mined run the replay tests replay.
const replayTestRun = "ra"

// The nested services' refusal codes the replay rows carry verbatim.
const (
	agentFailedCode  = "agent-failed"
	agentMissingCode = "agent-missing"
	badRangeCode     = "bad-range"
)

// replayFixture is one mined two-PR run plus the PATH directory the fake agent
// lives in and the recorder the human lines land in.
type replayFixture struct { //nolint:govet // fields stay grouped by role: history, agent, output
	repo *repo
	mine MineDoc
	rec  *recorder
	bin  string
	log  string
}

// newReplayFixture mines twoCandidateRepo and lands its candidate list in
// .verdict/backtest/ra/mine.json, the run the replay reads.
func newReplayFixture(t *testing.T) *replayFixture {
	t.Helper()
	r, base := twoCandidateRepo(t)
	mined := runMine(t, r, base).Mine
	doc := MineDoc{SchemaVersion: schemaVersion, Method: methodSZZ, Candidates: mined.Candidates, Skipped: mined.Skipped}
	f := &replayFixture{repo: r, mine: doc, rec: &recorder{}}
	dir := filepath.Join(r.dir, ".verdict", "backtest", replayTestRun)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, "mine.json"), jq.Pretty(f.mine))
	f.bin = t.TempDir()
	f.log = filepath.Join(t.TempDir(), "agent.log")
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve the test binary: %v", err)
	}
	if err := os.Symlink(exe, filepath.Join(f.bin, "claude")); err != nil {
		t.Fatalf("link the fake agent: %v", err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return f
}

// request builds the replay request the case mutates, with the fake agent on
// PATH and a pinned clock.
func (f *replayFixture) request(t *testing.T, mutate func(*ReplayRequest)) *ReplayRequest {
	t.Helper()
	req := &ReplayRequest{
		Runtime: &core.Runtime{
			Commander: coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true},
			Env:       userEnv(t, nil),
			Progress:  f.rec,
			Now:       func() time.Time { return time.Date(2024, 2, 1, 12, 0, 0, 0, time.UTC) },
		},
		LookPath:  exec.LookPath,
		Effective: core.Defaults(),
		Environ:   os.Environ(),
		RepoRoot:  f.repo.dir,
		Run:       replayTestRun,
		Agent:     "claude",
	}
	if mutate != nil {
		mutate(req)
	}
	return req
}

// plant makes the fake agent the claude the launcher resolves, in mode, with
// price as the cost it reports (empty writes no cost.json).
func (f *replayFixture) plant(t *testing.T, mode, price string) {
	t.Helper()
	t.Setenv(fakeAgentEnv, mode)
	t.Setenv(fakeAgentCostEnv, price)
	t.Setenv(fakeAgentLogEnv, f.log)
	t.Setenv("PATH", f.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
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

// runReplayOK expects a completed run.
func runReplayOK(t *testing.T, req *ReplayRequest) *ReplayResponse {
	t.Helper()
	resp, ref := RunReplay(t.Context(), req)
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	return resp
}

// runReplayRefusal expects a refusal of the whole run.
func runReplayRefusal(t *testing.T, req *ReplayRequest) *core.RefusalError {
	t.Helper()
	resp, ref := RunReplay(t.Context(), req)
	if ref == nil {
		t.Fatalf("no refusal; data %+v", resp.Data)
	}
	return ref
}

// writeTestFile plants one fixture file.
func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
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
	return filepath.Join(f.repo.dir, ".verdict", "state", "local-bt-"+replayTestRun+"-"+strconv.Itoa(n))
}

// prPath is one candidate's resume record.
func (f *replayFixture) prPath(n int) string {
	return filepath.Join(f.repo.dir, ".verdict", "backtest", replayTestRun, "prs",
		fmt.Sprintf("%03d", n), "replay.json")
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
	if data.Mode != modeReplay || data.Run != replayTestRun || data.Agent != "claude" {
		t.Fatalf("data = mode %q run %q agent %q", data.Mode, data.Run, data.Agent)
	}
	if data.StopReason != stopComplete {
		t.Fatalf("stop reason = %q, want %q", data.StopReason, stopComplete)
	}
	if len(data.PRs) != 2 {
		t.Fatalf("rows = %d, want the two mined candidates", len(data.PRs))
	}
	for i, wantPR := range []int{2, 1} {
		checkReplayRow(t, data.PRs[i], i+1, wantPR)
	}
	if data.Budgets.SpentUSD == nil || *data.Budgets.SpentUSD != 1.2 || data.Budgets.Unpriced != 0 {
		t.Errorf("budgets = %+v; want 1.2 spent over two priced reviews", data.Budgets)
	}
}

// checkReplayRow compares one row's position, PR number, bundle, and price.
func checkReplayRow(t *testing.T, row ReplayPR, n, wantPR int) {
	t.Helper()
	if row.N != n || row.PR == nil || *row.PR != wantPR || row.Status != statusOK {
		t.Errorf("row %d = %+v; want n=%d pr=%d status=ok", n, row, n, wantPR)
	}
	if want := "local-bt-" + replayTestRun + "-" + strconv.Itoa(n); row.Bundle != want {
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
	if marker.Mode != modeReplay || marker.Run != replayTestRun || marker.N != 1 || marker.PR == nil || *marker.PR != 2 {
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

// checkReplayCheckouts proves git kept no replay worktree, no checkout is left
// on disk, and the child ran inside the checkout with the bundle as its target.
func checkReplayCheckouts(t *testing.T, f *replayFixture) {
	t.Helper()
	out := f.repo.git("worktree", "list", "--porcelain")
	if got := strings.Count(out, "worktree "); got != 1 {
		t.Errorf("git still knows %d worktrees; want only the fixture:\n%s", got, out)
	}
	checkNoLeftoverCheckouts(t, f)
	lines := f.launchLines(t)
	if len(lines) != 2 {
		t.Fatalf("launches = %d, want one per candidate", len(lines))
	}
	for i, line := range lines {
		checkLaunch(t, f, i, line)
	}
}

// checkNoLeftoverCheckouts proves nothing named like a replay checkout stays
// under the state directory.
func checkNoLeftoverCheckouts(t *testing.T, f *replayFixture) {
	t.Helper()
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

// checkLaunch compares one launch's cwd and artifact against the candidate it
// replayed.
func checkLaunch(t *testing.T, f *replayFixture, i int, line []string) {
	t.Helper()
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
	if ref.Code != codeRunMismatch {
		t.Fatalf("code = %q, want %q", ref.Code, codeRunMismatch)
	}
	if !strings.Contains(ref.Message, "with agent 'claude'") {
		t.Errorf("refusal does not name the recorded agent: %q", ref.Message)
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

// TestReplayRecordsPerPRFailureAndRetriesIt proves one agent failure is that
// PR's row, not the run's, and that the next invocation retries it.
func TestReplayRecordsPerPRFailureAndRetriesIt(t *testing.T) {
	f := newReplayFixture(t)
	f.plant(t, "exit", "")
	failed := runReplayOK(t, f.request(t, nil)).Data
	checkFailedRun(t, f, failed)
	f.plant(t, "write", "0.6")
	checkRetriedRun(t, f, runReplayOK(t, f.request(t, nil)).Data)
}

// checkFailedRun proves the failed reviews are rows with unknown spend, and
// that each one's resume record carries the failure for the retry.
func checkFailedRun(t *testing.T, f *replayFixture, failed *ReplayData) {
	t.Helper()
	if failed.StopReason != stopComplete {
		t.Errorf("stop reason = %q; a failed review is not the run's refusal", failed.StopReason)
	}
	if len(failed.PRs) != 2 {
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
	var stored ReplayPRDoc
	if err := json.Unmarshal([]byte(readTestFile(t, f.prPath(1))), &stored); err != nil {
		t.Fatalf("stored row: %v", err)
	}
	if stored.Status != agentFailedCode || stored.Detail == "" {
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
		if row.Status != statusOK || row.CostUSD == nil {
			t.Errorf("retried row %d = %+v; want a recorded priced review", i, row)
		}
	}
}

// TestReplayPerPRBundleFailureContinues proves a candidate whose range cannot
// be resolved is one failed row and the run goes on.
func TestReplayPerPRBundleFailureContinues(t *testing.T) {
	f := newReplayFixture(t)
	f.mine.Candidates[1].Base = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	writeTestFile(t, filepath.Join(f.repo.dir, ".verdict", "backtest", replayTestRun, "mine.json"), jq.Pretty(f.mine))
	f.plant(t, "write", "0.6")
	data := runReplayOK(t, f.request(t, nil)).Data
	if data.StopReason != stopComplete {
		t.Errorf("stop reason = %q, want the run to continue past one bad candidate", data.StopReason)
	}
	if len(data.PRs) != 2 {
		t.Fatalf("rows = %d, want both candidates", len(data.PRs))
	}
	if data.PRs[0].Status != badRangeCode || data.PRs[0].Bundle != "" {
		t.Errorf("row 1 = %+v; want a %s row with no bundle", data.PRs[0], badRangeCode)
	}
	if data.PRs[1].Status != statusOK {
		t.Errorf("row 2 = %+v; want the second candidate to replay", data.PRs[1])
	}
}

// TestReplayRefusesBadInputs proves the run refuses an unknown run, a bad run
// id, a bad budget, and an unknown agent before it spends anything.
func TestReplayRefusesBadInputs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ReplayRequest)
		code   string
		part   string
	}{
		{name: "missing mine", mutate: func(r *ReplayRequest) { r.Run = "absent" }, code: codeMissingBundle, part: "no mine.json"},
		{name: "empty run", mutate: func(r *ReplayRequest) { r.Run = "" }, code: codeBadFlags, part: "--run <id>"},
		{name: "path run", mutate: func(r *ReplayRequest) { r.Run = "../ra" }, code: codeBadSlug, part: "lowercase letters"},
		{name: "zero budget", mutate: func(r *ReplayRequest) { r.BudgetRaw = "0" }, code: codeBadFlags, part: "--budget-usd"},
		{name: "word budget", mutate: func(r *ReplayRequest) { r.BudgetRaw = "five" }, code: codeBadFlags, part: "--budget-usd"},
		{name: "zero reviews", mutate: func(r *ReplayRequest) { r.MaxReviewsRaw = "0" }, code: codeBadFlags, part: "--max-reviews"},
		{name: "zero timeout", mutate: func(r *ReplayRequest) { r.TimeoutRaw = "0" }, code: codeBadTimeout, part: "timeout"},
		{name: "unknown agent", mutate: func(r *ReplayRequest) { r.Agent = "nope" }, code: "unknown-agent", part: "known: claude, codex"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { checkBadInput(t, tc.mutate, tc.code, tc.part) })
	}
}

// checkBadInput proves the refusal code, the named cause, and that no agent
// ran.
func checkBadInput(t *testing.T, mutate func(*ReplayRequest), code, part string) {
	t.Helper()
	f := newReplayFixture(t)
	f.plant(t, "write", "0.6")
	ref := runReplayRefusal(t, f.request(t, mutate))
	if ref.Code != code {
		t.Fatalf("code = %q, want %q (%s)", ref.Code, code, ref.Message)
	}
	if !strings.Contains(ref.Message, part) {
		t.Errorf("message %q does not mention %q", ref.Message, part)
	}
	if got := f.launches(t); got != 0 {
		t.Errorf("launches = %d; a refused run must spend nothing", got)
	}
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
		"  1  #2  local-bt-ra-1  ok  $0.60",
		"  2  #1  local-bt-ra-2  ok  $0.60",
		"stopped: complete",
		"spend: $1.20 over 2 reviews",
		"not citable — SZZ-derived: a replay grades a blame-derived guess, not a verified defect",
	}
	if !slices.Equal(f.rec.lines, want) {
		t.Errorf("human lines =\n%s\nwant\n%s", strings.Join(f.rec.lines, "\n"), strings.Join(want, "\n"))
	}
}
