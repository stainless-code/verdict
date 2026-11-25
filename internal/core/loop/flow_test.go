package loopcore

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/core/reviewflow"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/spend"
	"github.com/stainless-code/verdict/internal/testenv"
	"github.com/stainless-code/verdict/internal/testkit"
)

// agentBinary is the catalog row the integration loop launches. No binary by
// that name runs: agentCommander answers its spawns in-process.
const agentBinary = "claude"

// agentCommander is the AgentCommander of an integration loop: it plays the
// agent for the review and fix spawns and hands every other process (the
// verify command, git) to the production watchdog.
type agentCommander struct {
	t              *testing.T
	costAt         string
	spawns         []string
	argvs          [][]string
	fixExit        int
	reviews        int
	datedCost      bool
	fixNeverStarts bool
	repairFirst    bool
	repairNoCost   bool
	measured       bool
	selfReportToo  bool
	brokenOutput   bool
	reportFixCost  bool
}

// measuredResult is the json result a measured spawn prints.
func measuredResult(usd string) string {
	return `{"type":"result","subtype":"success","is_error":false,"num_turns":4,"result":"done",` +
		`"session_id":"s","total_cost_usd":` + usd + `}`
}

// flowNow is the integration clock: one fixed instant inside one month, so
// the receipts and the month-to-date scans can never straddle a boundary.
func flowNow() time.Time { return time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC) }

// errNeverStarted is the spawn failure of a fix process that never started.
var errNeverStarted = errors.New("exec: no such file")

var (
	reviewPath = regexp.MustCompile(`(?:Write (\S+review\.json) FIRST with \{|Update (\S+review\.json) in place:)`)
	hunkHeader = regexp.MustCompile(`^@@ [^+]*\+(\d+)(?:,(\d+))? @@`)
)

func (a *agentCommander) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	if len(cmd.Argv) == 0 || cmd.Argv[0] != agentBinary {
		return coreexec.Watchdog{}.Run(ctx, cmd)
	}
	a.argvs = append(a.argvs, cmd.Argv)
	directive := cmd.Argv[len(cmd.Argv)-1]
	if m := reviewPath.FindStringSubmatch(directive); m != nil {
		a.spawns = append(a.spawns, "review")
		a.review(cmd.Dir, m[1]+m[2])
		return core.Result{Stdout: a.stdout("0.1")}, nil
	}
	if a.fixNeverStarts {
		return core.Result{}, errNeverStarted
	}
	a.spawns = append(a.spawns, "fix")
	a.fix(cmd.Dir, directive)
	return core.Result{ExitCode: a.fixExit, Stdout: a.stdout("0.3")}, nil
}

// stdout is a spawn's captured output: the measured json result, or nothing.
func (a *agentCommander) stdout(usd string) string {
	switch {
	case a.brokenOutput:
		return measuredResult(usd)[:40]
	case !a.measured:
		return ""
	}
	return measuredResult(usd)
}

func (a *agentCommander) LookPath(string) error { return nil }

// review writes the review of the fetched diff: the changed line is a finding
// until it is fixed, then a clean review with one receipt per hunk. It prices
// itself in cost.json in the prompt's own schema-2 shape — undated, the total
// at the root — unless the leg asks for the legacy dated record.
func (a *agentCommander) review(checkout, artifact string) {
	if !filepath.IsAbs(artifact) {
		artifact = filepath.Join(checkout, artifact)
	}
	dir := filepath.Dir(artifact)
	a.reviews++
	findings, receipts := a.findings(checkout), hunkReceipts(a.t, dir)
	if findings != "[]" {
		receipts = "[]"
	}
	write(a.t, artifact, `{"schema_version":"1","summary":"Reviewed the notes change.","findings":`+findings+
		`,"coverage":[{"file":"notes.txt","ranges":"1-3"}],"receipts":`+receipts+`}`)
	if (a.measured && !a.selfReportToo) || (a.repairNoCost && a.reviews == 2) {
		return
	}
	write(a.t, filepath.Join(dir, "cost.json"), a.costRecord())
}

// findings is the review's finding list: the changed line until it is
// fixed, quoting a line the diff lacks on a first attempt the leg repairs.
func (a *agentCommander) findings(checkout string) string {
	notes, err := os.ReadFile(filepath.Join(checkout, "notes.txt"))
	if err != nil {
		a.t.Errorf("read notes: %v", err)
	}
	if !strings.Contains(string(notes), "new line") {
		return "[]"
	}
	quote := "new line"
	if a.repairFirst && a.reviews == 1 {
		quote = "a line the diff never had"
	}
	return `[{"path":"notes.txt","line":2,"side":"RIGHT","claim":"Incorrect line.","quote":"` + quote + `",` +
		`"confidence":"high","severity":"important","rigor":"walked"}]`
}

// costRecord is the review's cost.json: the prompt's undated schema-2
// record, or the dated record the leg asks for.
func (a *agentCommander) costRecord() string {
	if !a.datedCost {
		return `{"schema_version":"2","effort":"standard","run":"loop","models":[{"name":"m","input_tokens":1,` +
			`"output_tokens":1}],"lenses":[],"group_costs":[],"est_usd":0.1}`
	}
	at := core.NowUTC(flowNow())
	if a.costAt != "" {
		at = a.costAt
	}
	return `{"at":"` + at + `","est_usd":0.1}`
}

// fix rewrites the line, declares the verify command, and optionally reports
// its spend at the paths the directive names.
func (a *agentCommander) fix(checkout, directive string) {
	write(a.t, filepath.Join(checkout, "notes.txt"), "top\nfixed line\nbottom\n")
	write(a.t, directiveLine(a.t, directive, "patch.json: "), `{"verifyCmd":"grep -q fixed notes.txt"}`)
	if a.reportFixCost {
		write(a.t, directiveLine(a.t, directive, "fix-cost.json: "), `{"est_usd":0.3}`)
	}
}

// directiveLine reads the path the directive's context block names.
func directiveLine(t *testing.T, directive, prefix string) string {
	t.Helper()
	for line := range strings.SplitSeq(directive, "\n") {
		if path, ok := strings.CutPrefix(line, prefix); ok {
			return path
		}
	}
	t.Errorf("directive names no %q line", prefix)
	return filepath.Join(t.TempDir(), "missing")
}

// hunkReceipts is one reading receipt per changed hunk the bundle lists,
// quoting the first line the hunk carries on its new side — what an agent
// reading the fetched diff attests.
func hunkReceipts(t *testing.T, dir string) string {
	t.Helper()
	want := map[string]bool{}
	raw, err := os.ReadFile(filepath.Join(dir, "hunks.tsv"))
	if err != nil {
		t.Errorf("read hunks.tsv: %v", err)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		want[line] = true
	}
	body, err := json.Marshal(diffReceipts(t, filepath.Join(dir, "full.diff"), want))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// receipt is one reading receipt: the hunk key and its quoted line.
type receipt struct {
	Hunk  string `json:"hunk"`
	Quote string `json:"quote"`
}

// diffReceipts walks the diff and quotes the first new-side line of every
// wanted hunk.
func diffReceipts(t *testing.T, path string, want map[string]bool) []receipt {
	t.Helper()
	diff, err := os.Open(path)
	if err != nil {
		t.Fatalf("open full.diff: %v", err)
	}
	defer func() { _ = diff.Close() }()
	w := &diffWalk{t: t, want: want}
	scan := bufio.NewScanner(diff)
	for scan.Scan() {
		w.step(scan.Text())
	}
	return w.out
}

// diffWalk is the per-line state of one diff walk.
type diffWalk struct {
	t    *testing.T
	want map[string]bool
	file string
	key  string
	out  []receipt
}

// step reads one diff line: a file header, a hunk header, or a hunk line.
func (w *diffWalk) step(line string) {
	if p, ok := strings.CutPrefix(line, "+++ b/"); ok {
		w.file = p
		return
	}
	if m := hunkHeader.FindStringSubmatch(line); m != nil {
		w.key = hunkKey(w.t, w.file, m[1], m[2])
		return
	}
	if w.want[w.key] && (strings.HasPrefix(line, "+") || strings.HasPrefix(line, " ")) {
		w.out = append(w.out, receipt{Hunk: w.key, Quote: strings.ReplaceAll(line[1:], "\t", " ")})
		w.key = ""
	}
}

// hunkKey is hunks.tsv's key for one hunk header's new side.
func hunkKey(t *testing.T, file, startRaw, spanRaw string) string {
	t.Helper()
	start, err := strconv.Atoi(startRaw)
	if err != nil {
		t.Fatalf("hunk start %q: %v", startRaw, err)
	}
	span := 1
	if spanRaw != "" {
		if span, err = strconv.Atoi(spanRaw); err != nil {
			t.Fatalf("hunk span %q: %v", spanRaw, err)
		}
	}
	return file + "\tNEW\t" + strconv.Itoa(start) + "\t" + strconv.Itoa(start+span-1)
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Errorf("write %s: %v", path, err)
	}
}

// git runs one hermetic git command in dir.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	// A descendant holding the captured pipe open must not hold the helper.
	cmd.WaitDelay = 10 * time.Second
	cmd.Dir = dir
	cmd.Env = testenv.Base(nil)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// flowRepo is a checkout with one committed file and one tracked change.
func flowRepo(t *testing.T) string {
	t.Helper()
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "-q")
	write(t, filepath.Join(repo, ".gitignore"), ".verdict/\n")
	write(t, filepath.Join(repo, "notes.txt"), "top\nold line\nbottom\n")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "base")
	write(t, filepath.Join(repo, "notes.txt"), "top\nnew line\nbottom\n")
	return repo
}

// flowRequest wires a loop through the shipped services: real fetch-local,
// run, record, gate, and verify, with only the agent played in-process.
func flowRequest(t *testing.T, repo string, agent *agentCommander) *Request {
	t.Helper()
	root := t.TempDir()
	home := t.TempDir()
	return &Request{
		Flow: &reviewflow.Flow{
			Runtime: &core.Runtime{
				Env: map[string]string{"HOME": home}, Now: flowNow, Progress: &testkit.Collector{},
			},
			Root:           &root,
			Effective:      core.Defaults(),
			Commander:      coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true},
			AgentCommander: agent,
			LookPath: func(bin string) (string, error) {
				if bin == agentBinary {
					return "/fake/bin/" + bin, nil
				}
				return exec.LookPath(bin)
			},
			Environ: testenv.Base(nil),
		},
		RepoRoot:  repo,
		Repo:      "acme/app",
		Agent:     agentBinary,
		MaxRounds: 3,
		Budget:    5 * time.Minute,
		Output:    filepath.Join(t.TempDir(), "receipts"),
	}
}

// TestLoopThroughTheShippedServices proves the production wiring end to end:
// round 1 reviews, records, fails the gate, fixes, and verifies; round 2
// re-fetches the fixed tree and passes. Both spawns of round 1 reach the
// month-to-date scan priced: the review from its prompt-shaped (undated,
// schema-2) cost.json, the fix from its fix-cost.json report — so a loop
// with --max-cost-usd proceeds past round 1 with the review counted.
//
// fetch-local reads the process working directory, so the test chdirs and
// cannot run in parallel.
func TestLoopThroughTheShippedServices(t *testing.T) {
	repo := flowRepo(t)
	t.Chdir(repo)
	agent := &agentCommander{t: t, reportFixCost: true}
	req := flowRequest(t, repo, agent)
	req.MaxCostUSD = "5"
	resp, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("loop refused: %v", ref)
	}
	if resp.Data.Stop != StopPass || len(resp.Data.Rounds) != 2 {
		t.Fatalf("stop=%s rounds=%+v refusal=%+v, want a pass in round 2",
			resp.Data.Stop, resp.Data.Rounds, resp.Data.Refusal)
	}
	if got := strings.Join(agent.spawns, ","); got != "review,fix,review" {
		t.Errorf("spawns = %s, want review,fix,review", got)
	}
	assertFixedRound(t, &resp.Data.Rounds[0])
	mtd := spend.AggregateMTD(readcommon.StateDir(repo), flowNow())
	if mtd.KnownUSD != "0.5" || mtd.UnknownCount != 0 {
		t.Errorf("month-to-date = %+v, want 0.5 known (two reviews and the priced fix)", mtd)
	}
}

// assertFixedRound proves round 1 failed its gate on the one finding, ran a
// verified fix, and priced both spawns.
func assertFixedRound(t *testing.T, rd *Round) {
	t.Helper()
	if rd.Open == nil || *rd.Open != 1 || rd.Gate == nil || *rd.Gate {
		t.Errorf("round 1 = open %v gate %v, want one open finding and a failed gate", rd.Open, rd.Gate)
	}
	assertVerifyExit(t, rd.Fix, 0)
	if rd.CostUSD == nil || *rd.CostUSD != 0.4 {
		t.Errorf("round 1 cost = %v, want the review's 0.1 plus the fix's reported 0.3", rd.CostUSD)
	}
}

// TestLoopUnreportedFixSpendStopsACostCeiling proves the fail-closed rule
// through the shipped scan: the fix agent reports nothing, so its ledger row
// counts as unknown spend, and a loop with --max-cost-usd stops before round
// 2's review instead of spending blind.
func TestLoopUnreportedFixSpendStopsACostCeiling(t *testing.T) {
	repo := flowRepo(t)
	t.Chdir(repo)
	agent := &agentCommander{t: t}
	req := flowRequest(t, repo, agent)
	req.MaxCostUSD = "5"
	resp, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("loop refused: %v", ref)
	}
	if resp.Data.Stop != StopCostBudget || resp.Data.Refusal == nil || resp.Data.Refusal.Code != codeBudgetUnknown {
		t.Fatalf("stop=%s refusal=%+v, want cost-budget carrying budget-unknown", resp.Data.Stop, resp.Data.Refusal)
	}
	if got := strings.Join(agent.spawns, ","); got != "review,fix" {
		t.Errorf("spawns = %s, want review,fix and nothing after the unmeasured fix", got)
	}
	mtd := spend.AggregateMTD(readcommon.StateDir(repo), flowNow())
	if mtd.UnknownCount != 1 || mtd.KnownUSD != "0.1" {
		t.Errorf("month-to-date = %+v, want the review's 0.1 known and the fix spawn unknown", mtd)
	}
}

// flowLoop runs one integration loop to completion.
func flowLoop(t *testing.T, agent *agentCommander, maxCost string) (*Response, string) {
	t.Helper()
	repo := flowRepo(t)
	t.Chdir(repo)
	req := flowRequest(t, repo, agent)
	req.MaxCostUSD = maxCost
	resp, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("loop refused: %v", ref)
	}
	return resp, repo
}

// TestLoopDatedCostIsNotCountedTwice proves the legacy dated cost record,
// which the month-to-date scan counts itself, prices the review's receipt at
// zero beside it instead of adding the amount twice.
func TestLoopDatedCostIsNotCountedTwice(t *testing.T) {
	resp, repo := flowLoop(t, &agentCommander{t: t, reportFixCost: true, datedCost: true}, "5")
	if resp.Data.Stop != StopPass {
		t.Fatalf("stop=%s refusal=%+v, want a pass", resp.Data.Stop, resp.Data.Refusal)
	}
	mtd := spend.AggregateMTD(readcommon.StateDir(repo), flowNow())
	if mtd.KnownUSD != "0.5" || mtd.UnknownCount != 0 {
		t.Errorf("month-to-date = %+v, want 0.5 known: each review once and the fix", mtd)
	}
}

// TestLoopFixThatNeverStartedIsFree proves a fix whose process never started
// is priced at zero rather than left as unknown spend forever.
func TestLoopFixThatNeverStartedIsFree(t *testing.T) {
	resp, repo := flowLoop(t, &agentCommander{t: t, fixNeverStarts: true}, "")
	if resp.Data.Stop != StopRefused || resp.Data.Refusal == nil || resp.Data.Refusal.Code != codeSpawnFailed {
		t.Fatalf("stop=%s refusal=%+v, want refused carrying agent-spawn-failed", resp.Data.Stop, resp.Data.Refusal)
	}
	mtd := spend.AggregateMTD(readcommon.StateDir(repo), flowNow())
	if mtd.KnownUSD != "0.1" || mtd.UnknownCount != 0 {
		t.Errorf("month-to-date = %+v, want the review's 0.1 and the unstarted fix free", mtd)
	}
}

// TestLoopFailedFixKeepsItsReportedSpend proves a fix that exits non-zero
// after reporting its spend is still priced: it ran, so it spent.
func TestLoopFailedFixKeepsItsReportedSpend(t *testing.T) {
	resp, repo := flowLoop(t, &agentCommander{t: t, reportFixCost: true, fixExit: 3}, "")
	if resp.Data.Stop != StopAgentFailed {
		t.Fatalf("stop=%s refusal=%+v, want agent-failed", resp.Data.Stop, resp.Data.Refusal)
	}
	mtd := spend.AggregateMTD(readcommon.StateDir(repo), flowNow())
	if mtd.KnownUSD != "0.4" || mtd.UnknownCount != 0 {
		t.Errorf("month-to-date = %+v, want the review's 0.1 plus the failed fix's reported 0.3", mtd)
	}
}

// TestLoopPricesEachReviewAttemptFromItsOwnRecord proves a repaired review
// counts both attempts: each is a fresh process that rewrites cost.json for
// its own run, so the first attempt's record is captured before the repair
// overwrites it. Two $0.1 attempts, the $0.3 fix, and round 2's $0.1 review
// make $0.6.
func TestLoopPricesEachReviewAttemptFromItsOwnRecord(t *testing.T) {
	agent := &agentCommander{t: t, reportFixCost: true, repairFirst: true}
	resp, repo := flowLoop(t, agent, "5")
	if resp.Data.Stop != StopPass {
		t.Fatalf("stop=%s refusal=%+v, want a pass", resp.Data.Stop, resp.Data.Refusal)
	}
	if got := strings.Join(agent.spawns, ","); got != "review,review,fix,review" {
		t.Fatalf("spawns = %s, want a repaired round-1 review", got)
	}
	mtd := spend.AggregateMTD(readcommon.StateDir(repo), flowNow())
	if mtd.KnownUSD != "0.6" || mtd.UnknownCount != 0 {
		t.Errorf("month-to-date = %+v, want 0.6 known: both review attempts, the fix, and round 2", mtd)
	}
}

// TestLoopAttemptWithoutItsOwnRecordStaysUnknown proves a repair attempt that
// wrote no cost record is unknown, never priced from the earlier attempt's
// file and never free, so a cost-ceilinged loop stops on it.
func TestLoopAttemptWithoutItsOwnRecordStaysUnknown(t *testing.T) {
	agent := &agentCommander{t: t, reportFixCost: true, repairFirst: true, repairNoCost: true}
	resp, repo := flowLoop(t, agent, "5")
	if resp.Data.Stop != StopCostBudget || resp.Data.Refusal == nil || resp.Data.Refusal.Code != codeBudgetUnknown {
		t.Fatalf("stop=%s refusal=%+v, want budget-unknown", resp.Data.Stop, resp.Data.Refusal)
	}
	mtd := spend.AggregateMTD(readcommon.StateDir(repo), flowNow())
	if mtd.KnownUSD != "0.1" || mtd.UnknownCount != 1 {
		t.Errorf("month-to-date = %+v, want attempt 1's 0.1 known and attempt 2 unknown", mtd)
	}
}

// TestLoopUncountedDatedCostIsPriced proves a record whose `at` the monthly
// scan cannot count (here malformed) prices the review from its est_usd
// instead of vanishing from every total.
func TestLoopUncountedDatedCostIsPriced(t *testing.T) {
	agent := &agentCommander{t: t, reportFixCost: true, datedCost: true, costAt: "yesterday"}
	resp, repo := flowLoop(t, agent, "5")
	if resp.Data.Stop != StopPass {
		t.Fatalf("stop=%s refusal=%+v, want a pass", resp.Data.Stop, resp.Data.Refusal)
	}
	mtd := spend.AggregateMTD(readcommon.StateDir(repo), flowNow())
	if mtd.KnownUSD != "0.5" || mtd.UnknownCount != 0 {
		t.Errorf("month-to-date = %+v, want 0.5 known: two reviews and the fix", mtd)
	}
}

// TestLoopMeasuresSpendFromTheCLIsOwnOutput proves the measured source: the
// agent writes no cost record and no fix-cost.json, yet every spawn is priced
// from the json result the CLI prints — requested on every loop spawn — so a
// cost-ceilinged loop proceeds past round 1 and the monthly scan counts it.
func TestLoopMeasuresSpendFromTheCLIsOwnOutput(t *testing.T) {
	agent := &agentCommander{t: t, measured: true}
	resp, repo := flowLoop(t, agent, "5")
	if resp.Data.Stop != StopPass || len(resp.Data.Rounds) != 2 {
		t.Fatalf("stop=%s refusal=%+v, want a pass in round 2", resp.Data.Stop, resp.Data.Refusal)
	}
	for _, argv := range agent.argvs {
		if !slices.Equal(argv[1:4], []string{"-p", "--output-format", "json"}) {
			t.Errorf("argv = %q, want the json output requested", argv)
		}
	}
	mtd := spend.AggregateMTD(readcommon.StateDir(repo), flowNow())
	if mtd.KnownUSD != "0.5" || mtd.UnknownCount != 0 {
		t.Errorf("month-to-date = %+v, want 0.5 measured: two reviews and the fix", mtd)
	}
	assertPricedBy(t, repo, spend.PricedMeasured)
}

// assertPricedBy proves every spawn receipt in the repository names source.
func assertPricedBy(t *testing.T, repo, source string) {
	t.Helper()
	ledgers, err := filepath.Glob(filepath.Join(readcommon.StateDir(repo), "*", "budget.json"))
	if err != nil || len(ledgers) == 0 {
		t.Fatalf("no spawn ledgers (err %v)", err)
	}
	for _, path := range ledgers {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(raw), `"priced_by": "`+source+`"`) != strings.Count(string(raw), `"scope": "spawn"`) {
			t.Errorf("%s = %s, want every spawn priced_by %s", path, raw, source)
		}
	}
}

// TestLoopProjectsAnUnreportedSpawnFromPastMeasuredRuns proves the projected
// source: the fix reports nothing and prints no cost, so it is priced at the
// median of past measured spawns of the same agent — the loop proceeds under
// its cost ceiling with the receipt labeled projected.
func TestLoopProjectsAnUnreportedSpawnFromPastMeasuredRuns(t *testing.T) {
	repo := flowRepo(t)
	past := filepath.Join(readcommon.StateDir(repo), "local-loop-earlier-1")
	if err := os.MkdirAll(past, 0o755); err != nil {
		t.Fatal(err)
	}
	ledger := `[{"at":"2026-05-01T09:00:00Z","execution_id":"a","scope":"spawn","agent":"claude","model":"",` +
		`"projected_usd":0.2,"priced_by":"measured"},` +
		`{"at":"2026-05-01T09:05:00Z","execution_id":"b","scope":"spawn","agent":"claude","model":"",` +
		`"projected_usd":0.4,"priced_by":"tokens"},` +
		`{"at":"2026-05-01T09:10:00Z","execution_id":"c","scope":"spawn","agent":"codex","model":"",` +
		`"projected_usd":9,"priced_by":"measured"}]`
	if err := os.WriteFile(filepath.Join(past, "budget.json"), []byte(ledger), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	agent := &agentCommander{t: t}
	req := flowRequest(t, repo, agent)
	req.MaxCostUSD = "5"
	resp, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("loop refused: %v", ref)
	}
	if resp.Data.Stop != StopPass {
		t.Fatalf("stop=%s refusal=%+v, want a pass on the projected fix", resp.Data.Stop, resp.Data.Refusal)
	}
	raw, err := os.ReadFile(filepath.Join(readcommon.StateDir(repo), resp.Data.Rounds[0].Bundle, "budget.json"))
	if err != nil || !strings.Contains(string(raw), `"projected_usd": 0.3,`) ||
		!strings.Contains(string(raw), `"priced_by": "projected"`) {
		t.Errorf("round 1 ledger = %s (err %v), want the fix projected at the claude median 0.3", raw, err)
	}
}

// TestLoopMalformedOutputFallsToTheSelfReport proves a capture that does not
// match the CLI's documented shape measures nothing: the spawns fall to the
// next source, the agent's own cost record and fix-cost.json.
func TestLoopMalformedOutputFallsToTheSelfReport(t *testing.T) {
	agent := &agentCommander{t: t, brokenOutput: true, reportFixCost: true}
	resp, repo := flowLoop(t, agent, "5")
	if resp.Data.Stop != StopPass {
		t.Fatalf("stop=%s refusal=%+v, want a pass", resp.Data.Stop, resp.Data.Refusal)
	}
	mtd := spend.AggregateMTD(readcommon.StateDir(repo), flowNow())
	if mtd.KnownUSD != "0.5" || mtd.UnknownCount != 0 {
		t.Errorf("month-to-date = %+v, want 0.5 from the self-reports", mtd)
	}
	assertPricedBy(t, repo, spend.PricedAgent)
}

// TestLoopMeasuredOutranksTheSelfReport proves the priority order: a spawn
// with both the CLI's own cost and the agent's self-report is priced from the
// measurement.
func TestLoopMeasuredOutranksTheSelfReport(t *testing.T) {
	agent := &agentCommander{t: t, measured: true, selfReportToo: true, reportFixCost: true}
	resp, repo := flowLoop(t, agent, "5")
	if resp.Data.Stop != StopPass {
		t.Fatalf("stop=%s refusal=%+v, want a pass", resp.Data.Stop, resp.Data.Refusal)
	}
	assertPricedBy(t, repo, spend.PricedMeasured)
}
