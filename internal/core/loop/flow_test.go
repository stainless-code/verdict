package loopcore

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	t      *testing.T
	spawns []string
	// reportFixCost makes the fix agent report its spend in fix-cost.json.
	reportFixCost bool
}

var (
	reviewPath = regexp.MustCompile(`Write (\S+review\.json) FIRST with \{`)
	hunkHeader = regexp.MustCompile(`^@@ [^+]*\+(\d+)(?:,(\d+))? @@`)
)

func (a *agentCommander) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	if len(cmd.Argv) == 0 || cmd.Argv[0] != agentBinary {
		return coreexec.Watchdog{}.Run(ctx, cmd)
	}
	directive := cmd.Argv[len(cmd.Argv)-1]
	if m := reviewPath.FindStringSubmatch(directive); m != nil {
		a.spawns = append(a.spawns, "review")
		a.review(cmd.Dir, m[1])
		return core.Result{}, nil
	}
	a.spawns = append(a.spawns, "fix")
	a.fix(cmd.Dir, directive)
	return core.Result{}, nil
}

func (a *agentCommander) LookPath(string) error { return nil }

// review writes the review of the fetched diff: the changed line is a finding
// until it is fixed, then a clean review with one receipt per hunk. It prices
// itself in cost.json, as the review prompt asks.
func (a *agentCommander) review(checkout, artifact string) {
	if !filepath.IsAbs(artifact) {
		artifact = filepath.Join(checkout, artifact)
	}
	dir := filepath.Dir(artifact)
	notes, err := os.ReadFile(filepath.Join(checkout, "notes.txt"))
	if err != nil {
		a.t.Errorf("read notes: %v", err)
	}
	findings, receipts := "[]", hunkReceipts(a.t, dir)
	if strings.Contains(string(notes), "new line") {
		findings = `[{"path":"notes.txt","line":2,"side":"RIGHT","claim":"Incorrect line.","quote":"new line",` +
			`"confidence":"high","severity":"important","rigor":"walked"}]`
		receipts = "[]"
	}
	write(a.t, artifact, `{"schema_version":"1","summary":"Reviewed the notes change.","findings":`+findings+
		`,"coverage":[{"file":"notes.txt","ranges":"1-3"}],"receipts":`+receipts+`}`)
	write(a.t, filepath.Join(dir, "cost.json"), `{"at":"`+core.NowUTC(time.Now())+`","est_usd":0.1}`)
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
			Runtime:        &core.Runtime{Env: map[string]string{"HOME": home}, Progress: &testkit.Collector{}},
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
// month-to-date scan: the review through its cost.json, the fix through the
// ledger row its reported spend priced.
//
// fetch-local reads the process working directory, so the test chdirs and
// cannot run in parallel.
func TestLoopThroughTheShippedServices(t *testing.T) {
	repo := flowRepo(t)
	t.Chdir(repo)
	agent := &agentCommander{t: t, reportFixCost: true}
	resp, ref := Run(t.Context(), flowRequest(t, repo, agent))
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
	mtd := spend.AggregateMTD(readcommon.StateDir(repo), time.Now())
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
	mtd := spend.AggregateMTD(readcommon.StateDir(repo), time.Now())
	if mtd.UnknownCount != 1 || mtd.KnownUSD != "0.1" {
		t.Errorf("month-to-date = %+v, want the review's 0.1 known and the fix spawn unknown", mtd)
	}
}
