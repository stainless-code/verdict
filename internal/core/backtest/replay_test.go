package backtest

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/testenv"
)

// replayTestRun is the mined run the replay tests replay.
const replayTestRun = "ra"

// replayFixture is one mined two-PR run plus the log the fake agent appends to
// and the recorder the human lines land in. No agent is planted here: planting
// is a POSIX PATH entry, so only the launch tests do it.
//
// Nothing here touches the process environment, so replay tests run in
// parallel: the environment the agent inherits is [replayFixture.env], and
// the PATH the launcher resolves the agent on is the [replayFixture.look] seam.
type replayFixture struct {
	look func(string) (string, error)
	repo *repo
	rec  *recorder
	log  string
	mine MineDoc
	env  []string
}

// newReplayFixture hands the test its own copy of the mined twoCandidateRepo
// run: its repository, and its candidate list landed in
// .verdict/backtest/ra/mine.json, the run the replay reads.
func newReplayFixture(t *testing.T) *replayFixture {
	t.Helper()
	tpl := replayTemplateFor(t)
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(tpl.dir)); err != nil {
		t.Fatalf("copy the mined fixture: %v", err)
	}
	var doc MineDoc
	if err := json.Unmarshal(tpl.mine, &doc); err != nil {
		t.Fatalf("decode the mined fixture: %v", err)
	}
	f := &replayFixture{repo: &repo{t: t, dir: dir, day: tpl.day}, mine: doc, rec: &recorder{}}
	f.log = filepath.Join(t.TempDir(), "agent.log")
	f.setEnv("HOME", t.TempDir())
	f.setEnv("XDG_CONFIG_HOME", t.TempDir())
	return f
}

// setEnv sets one variable in the environment the replayed agent inherits,
// replacing an earlier value. The first call snapshots the process
// environment, so a test never mutates it.
func (f *replayFixture) setEnv(key, value string) {
	if f.env == nil {
		f.env = os.Environ()
	}
	prefix := key + "="
	f.env = slices.DeleteFunc(slices.Clone(f.env), func(kv string) bool { return strings.HasPrefix(kv, prefix) })
	f.env = append(f.env, prefix+value)
}

// lookPath is the request's agent lookup: the host PATH until a test plants
// its own.
func (f *replayFixture) lookPath(name string) (string, error) {
	if f.look != nil {
		return f.look(name)
	}
	return exec.LookPath(name)
}

// runner is the production commander, except that a bare name a test planted
// resolves in the planted directory. The launcher spawns the agent by its bare
// name, so the process PATH would decide which binary runs; resolving here
// keeps that choice per test, where the process PATH is shared by all of them.
func (f *replayFixture) runner() core.Commander {
	return plantedRunner{inner: coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true}, fixture: f}
}

// plantedRunner implements [core.Commander] for [replayFixture.runner].
type plantedRunner struct {
	inner   core.Commander
	fixture *replayFixture
}

// toolGate is the optional check [coreexec.Runner] offers before a first spawn.
type toolGate interface {
	LookPath(tool string) error
}

// LookPath forwards the tool gate fetch calls before its first spawn, like the
// production runner it wraps.
func (p plantedRunner) LookPath(tool string) error {
	if gate, ok := p.inner.(toolGate); ok {
		return gate.LookPath(tool)
	}
	return nil
}

// Run resolves a bare argv[0] through the fixture's planted lookup, then
// delegates.
func (p plantedRunner) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	if p.fixture.look != nil && len(cmd.Argv) > 0 && !strings.ContainsRune(cmd.Argv[0], filepath.Separator) {
		if bin, err := p.fixture.look(cmd.Argv[0]); err == nil {
			pinned := *cmd
			pinned.Argv = append([]string{bin}, cmd.Argv[1:]...)
			cmd = &pinned
		}
	}
	return p.inner.Run(ctx, cmd)
}

// environ is the environment the replayed agent inherits.
func (f *replayFixture) environ() []string {
	if f.env == nil {
		return os.Environ()
	}
	return f.env
}

// replayTemplate is the mined run every replay fixture starts from. Building
// it costs about two dozen git processes, and every replay test needs the same
// bytes (the commits are pinned), so it is built once per test binary and
// copied per test: the copy is a private repository the test may mutate.
type replayTemplate struct {
	dir  string
	mine []byte
	day  int
}

var (
	replayTemplateMu    sync.Mutex
	replayTemplateBase  *replayTemplate
	replayTemplateTries int
	// replayTemplateRoot outlives every test, so it is [TestMain]'s, not a
	// test's TempDir, which would vanish with the first test that asked.
	replayTemplateRoot string
)

// replayTemplateFor returns the shared template, building it on first use. A
// build that failed is not remembered: the next test builds again and reports
// its own failure.
func replayTemplateFor(t *testing.T) *replayTemplate {
	t.Helper()
	replayTemplateMu.Lock()
	defer replayTemplateMu.Unlock()
	if replayTemplateBase != nil {
		return replayTemplateBase
	}
	// A fresh directory per attempt: a build that failed halfway leaves its
	// repository behind, and the next attempt must not commit on top of it.
	replayTemplateTries++
	dir := filepath.Join(replayTemplateRoot, "mined-"+strconv.Itoa(replayTemplateTries))
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r, base := buildTwoCandidateRepo(newRepoAt(t, dir))
	mined := runMine(t, r, base).Mine
	doc := MineDoc{SchemaVersion: schemaVersion, Method: methodSZZ, Candidates: mined.Candidates, Skipped: mined.Skipped}
	(&replayFixture{repo: r}).writeMine(t, replayTestRun, &doc)
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	replayTemplateBase = &replayTemplate{dir: dir, mine: raw, day: r.day}
	return replayTemplateBase
}

// request builds the replay request the case mutates, with a pinned clock.
func (f *replayFixture) request(t *testing.T, mutate func(*ReplayRequest)) *ReplayRequest {
	t.Helper()
	req := &ReplayRequest{
		Runtime: &core.Runtime{
			Commander: f.runner(),
			Env:       userEnv(t, nil),
			Progress:  f.rec,
			Now:       func() time.Time { return time.Date(2024, 2, 1, 12, 0, 0, 0, time.UTC) },
		},
		LookPath:  f.lookPath,
		Effective: core.Defaults(),
		Environ:   f.environ(),
		RepoRoot:  f.repo.dir,
		Run:       replayTestRun,
		Agent:     "claude",
	}
	if mutate != nil {
		mutate(req)
	}
	return req
}

// writeMine lands one candidate list under one run id.
func (f *replayFixture) writeMine(t *testing.T, run string, doc *MineDoc) {
	t.Helper()
	dir := filepath.Join(f.repo.dir, ".verdict", "backtest", run)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, "mine.json"), jq.Pretty(doc))
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
	if err := os.WriteFile(path, []byte(testenv.MaybeFindingsReview(path, body)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestReplayRefusesBadInputs proves the run refuses an unknown run, a bad run
// id, a bad budget, and an unknown agent before it spends anything.
func TestReplayRefusesBadInputs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*ReplayRequest)
		code   string
		part   string
	}{
		{name: "missing mine", mutate: func(r *ReplayRequest) { r.Run = "absent" }, code: "missing-bundle", part: "no mine.json"},
		{name: "empty run", mutate: func(r *ReplayRequest) { r.Run = "" }, code: "bad-flags", part: "--run <id>"},
		{name: "path run", mutate: func(r *ReplayRequest) { r.Run = "../ra" }, code: "bad-slug", part: "lowercase letters"},
		{name: "overlong run", mutate: func(r *ReplayRequest) { r.Run = strings.Repeat("a", 43) }, code: "bad-slug", part: "at most 42 bytes"},
		{name: "zero budget", mutate: func(r *ReplayRequest) { r.BudgetRaw = "0" }, code: "bad-flags", part: "--budget-usd"},
		{name: "word budget", mutate: func(r *ReplayRequest) { r.BudgetRaw = "five" }, code: "bad-flags", part: "--budget-usd"},
		{name: "zero reviews", mutate: func(r *ReplayRequest) { r.MaxReviewsRaw = "0" }, code: "bad-flags", part: "--max-reviews"},
		{name: "too many reviews", mutate: func(r *ReplayRequest) { r.MaxReviewsRaw = "100" }, code: "bad-flags", part: "at most 99"},
		{name: "zero timeout", mutate: func(r *ReplayRequest) { r.TimeoutRaw = "0" }, code: "bad-timeout", part: "timeout"},
		{name: "zero rounds", mutate: func(r *ReplayRequest) { r.RoundsRaw = "0" }, code: "bad-flags", part: "--rounds must be 1-5"},
		{name: "six rounds", mutate: func(r *ReplayRequest) { r.RoundsRaw = "6" }, code: "bad-flags", part: "--rounds must be 1-5"},
		{name: "word rounds", mutate: func(r *ReplayRequest) { r.RoundsRaw = "two" }, code: "bad-flags", part: "--rounds must be 1-5"},
		{name: "unknown agent", mutate: func(r *ReplayRequest) { r.Agent = "nope" }, code: "unknown-agent", part: "known: claude, codex"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { checkBadInput(t, tc.mutate, tc.code, tc.part) })
	}
}

// TestRunIDCapFillsTheBundleNameCap proves the shared cap is derived from the
// store's name cap and the review ceiling rather than picked: the widest bundle
// a replay can build is exactly the store's 48 bytes, and the index that names
// it is [replayMaxReviews]'s own.
func TestRunIDCapFillsTheBundleNameCap(t *testing.T) {
	t.Parallel()
	if got := strconv.Itoa(replayMaxReviews); got != "99" {
		t.Fatalf("replayMaxReviews = %s; the frame budgets for a two-digit index", got)
	}
	longest := strings.Repeat("a", runIDMax)
	if got := len(longest); got != 42 {
		t.Fatalf("runIDMax = %d; want 42 bytes", got)
	}
	name := replayBundleName(longest, replayMaxReviews)
	if want := "bt-99-" + longest; name != want {
		t.Errorf("widest bundle name = %q, want %q", name, want)
	}
	if got := len(name); got != 48 {
		t.Errorf("widest bundle name = %d bytes; the store's cap is 48, so one more byte truncates", got)
	}
}

// TestReplayRefusesAnUncreatableStateDir proves a state directory replay cannot
// create ends the walk: no candidate can check out, so no row and no agent run
// would tell the operator anything new.
func TestReplayRefusesAnUncreatableStateDir(t *testing.T) {
	t.Parallel()
	f := newReplayFixture(t)
	state := filepath.Join(f.repo.dir, ".verdict", "state")
	writeTestFile(t, state, "not a directory")
	ref := runReplayRefusal(t, f.request(t, nil))
	if ref.Code != codeBacktestFail || !strings.Contains(ref.Message, "cannot create") {
		t.Fatalf("refusal = %s: %s; want %s naming the directory it cannot create", ref.Code, ref.Message, codeBacktestFail)
	}
	if _, err := os.Stat(filepath.Join(f.repo.dir, ".verdict", "backtest", replayTestRun, "prs")); !os.IsNotExist(err) {
		t.Errorf("a refused run wrote resume records: %v", err)
	}
}

// checkBadInput proves the refusal code, the named cause, and that no work ran.
func checkBadInput(t *testing.T, mutate func(*ReplayRequest), code, part string) {
	t.Helper()
	f := newReplayFixture(t)
	ref := runReplayRefusal(t, f.request(t, mutate))
	if ref.Code != code {
		t.Fatalf("code = %q, want %q (%s)", ref.Code, code, ref.Message)
	}
	if !strings.Contains(ref.Message, part) {
		t.Errorf("message %q does not mention %q", ref.Message, part)
	}
	checkNothingBuilt(t, f)
}

// checkNothingBuilt proves a refused invocation built no bundle and wrote no
// resume record.
func checkNothingBuilt(t *testing.T, f *replayFixture) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(f.repo.dir, ".verdict", "state"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "local-bt-") {
			t.Errorf("a refused run built bundle %s", entry.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(f.repo.dir, ".verdict", "backtest", replayTestRun, "prs")); !os.IsNotExist(err) {
		t.Errorf("a refused run wrote resume records: %v", err)
	}
}
