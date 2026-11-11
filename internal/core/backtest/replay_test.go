package backtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
)

// replayTestRun is the mined run the replay tests replay.
const replayTestRun = "ra"

// replayFixture is one mined two-PR run plus the log the fake agent appends to
// and the recorder the human lines land in. No agent is planted here: planting
// is a POSIX PATH entry, so only the launch tests do it.
type replayFixture struct {
	repo *repo
	rec  *recorder
	log  string
	mine MineDoc
}

// newReplayFixture mines twoCandidateRepo and lands its candidate list in
// .verdict/backtest/ra/mine.json, the run the replay reads.
func newReplayFixture(t *testing.T) *replayFixture {
	t.Helper()
	r, base := twoCandidateRepo(t)
	mined := runMine(t, r, base).Mine
	doc := MineDoc{SchemaVersion: schemaVersion, Method: methodSZZ, Candidates: mined.Candidates, Skipped: mined.Skipped}
	f := &replayFixture{repo: r, mine: doc, rec: &recorder{}}
	f.writeMine(t, replayTestRun, &f.mine)
	f.log = filepath.Join(t.TempDir(), "agent.log")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return f
}

// request builds the replay request the case mutates, with a pinned clock.
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
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
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
		{name: "missing mine", mutate: func(r *ReplayRequest) { r.Run = "absent" }, code: "missing-bundle", part: "no mine.json"},
		{name: "empty run", mutate: func(r *ReplayRequest) { r.Run = "" }, code: "bad-flags", part: "--run <id>"},
		{name: "path run", mutate: func(r *ReplayRequest) { r.Run = "../ra" }, code: "bad-slug", part: "lowercase letters"},
		{name: "overlong run", mutate: func(r *ReplayRequest) { r.Run = strings.Repeat("a", 49) }, code: "bad-slug", part: "at most 48 bytes"},
		{name: "zero budget", mutate: func(r *ReplayRequest) { r.BudgetRaw = "0" }, code: "bad-flags", part: "--budget-usd"},
		{name: "word budget", mutate: func(r *ReplayRequest) { r.BudgetRaw = "five" }, code: "bad-flags", part: "--budget-usd"},
		{name: "zero reviews", mutate: func(r *ReplayRequest) { r.MaxReviewsRaw = "0" }, code: "bad-flags", part: "--max-reviews"},
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
