package fetchcore

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// scriptCommander is a hermetic Commander: canned stdout/stderr per exact
// argv, every call logged for argv/stdin/stream assertions. Unmatched argv
// fails the test (no network, no real forge).
type scriptCommander struct {
	out   map[string]scriptResult
	tools map[string]bool
	t     *testing.T
	calls [][]string
	stdin []bool
	env   [][]string
}

// scriptResult is one canned spawn: the streams plus the failure flag.
type scriptResult struct {
	stdout string
	stderr string
	fail   bool
}

// Run records argv/stdin/env and replays the canned result.
func (s *scriptCommander) Run(_ context.Context, cmd core.Command) (core.Result, error) {
	s.t.Helper()
	s.calls = append(s.calls, append([]string{}, cmd.Argv...))
	s.stdin = append(s.stdin, cmd.Stdin != nil)
	s.env = append(s.env, cmd.Env)
	key := strings.Join(cmd.Argv, "\x00")
	res, ok := s.out[key]
	if !ok {
		s.t.Errorf("unexpected argv: %q", cmd.Argv)
		return core.Result{}, errors.New("unexpected argv")
	}
	out := core.Result{Stdout: res.stdout, Stderr: res.stderr}
	if res.fail {
		return out, errors.New("exit 1: " + key)
	}
	return out, nil
}

// LookPath reports scripted tool presence (missing reads MissingToolError).
func (s *scriptCommander) LookPath(tool string) error {
	if s.tools != nil && !s.tools[tool] {
		return &core.MissingToolError{Tool: tool}
	}
	return nil
}

// assertScriptCalls proves the exact argv fired with no stdin and an
// inherited env.
func assertScriptCalls(t *testing.T, cmdr *scriptCommander, want [][]string) {
	t.Helper()
	if len(cmdr.calls) != len(want) {
		t.Fatalf("argv calls = %q, want %d calls", cmdr.calls, len(want))
	}
	for i, wantArgv := range want {
		assertOneCall(t, cmdr, i, wantArgv)
	}
}

// assertOneCall proves one scripted call fired with no stdin and an
// inherited env.
func assertOneCall(t *testing.T, cmdr *scriptCommander, i int, want []string) {
	t.Helper()
	if strings.Join(cmdr.calls[i], "\x00") != strings.Join(want, "\x00") {
		t.Errorf("argv %d = %q, want %q", i, cmdr.calls[i], want)
	}
	if cmdr.stdin[i] {
		t.Errorf("argv %d sent stdin, want none", i)
	}
	if cmdr.env[i] != nil {
		t.Errorf("argv %d set env, want inherit", i)
	}
}

// assertArgvContains proves every wanted argv fired (order-free, for the
// local mode whose probe order is an implementation detail).
func assertArgvContains(t *testing.T, cmdr *scriptCommander, want []string) {
	t.Helper()
	var argv []string
	for _, c := range cmdr.calls {
		argv = append(argv, strings.Join(c, " "))
	}
	for _, wantArgv := range want {
		if !slices.Contains(argv, wantArgv) {
			t.Errorf("missing argv %q in %q", wantArgv, argv)
		}
	}
}

// assertBundleFiles proves every expected bundle file landed.
func assertBundleFiles(t *testing.T, dir string, names []string) {
	t.Helper()
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("missing bundle file %s: %v", name, err)
		}
	}
}

// testProgress collects streamed warnings.
type testProgress struct {
	warnings *[]string
}

// Human discards human lines (asserted on the request writer instead).
func (testProgress) Human(string) {}

// Warn records one warning.
func (p testProgress) Warn(msg string) { *p.warnings = append(*p.warnings, msg) }

// Diag discards raw diagnostics (asserted on the request writer instead).
func (testProgress) Diag(string) {}

var fetchTestTime = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

// fetchTestReq builds a fetch request over scripted streams.
func fetchTestReq(cmdr Commander, human, diag *bytes.Buffer, warnings *[]string) *core.FetchRequest {
	root := ""
	read := core.ReadRequest{
		Runtime: &core.Runtime{
			Env:       map[string]string{},
			Now:       func() time.Time { return fetchTestTime },
			Progress:  testProgress{warnings: warnings},
			Commander: cmdr,
		},
		Repo: "o/r",
		Pr:   "7",
		Root: &root,
	}
	return &core.FetchRequest{
		ReadRequest: read,
		Flags:       core.FetchFlags{},
		Effective:   core.Defaults(),
		Warnings:    []string{},
		Human:       human,
		Diag:        diag,
	}
}

const (
	fetchTestHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fetchTestBase = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// fetchTestPR is a minimal GitHub PR record: one file, open state.
func fetchTestPR() string {
	return `{"number":7,"title":"T","body":"B","baseRefName":"main","headRefName":"feat",` +
		`"headRefOid":"` + fetchTestHead + `","baseRefOid":"` + fetchTestBase + `",` +
		`"author":{"login":"me"},"url":"https://example/o/r/pull/7","commits":[],` +
		`"files":[{"path":"a.txt","additions":1,"deletions":1}],` +
		`"additions":1,"deletions":1,"isDraft":false,"reviewDecision":"","state":"OPEN"}`
}

// fetchTestDiff is a one-hunk diff for a.txt.
func fetchTestDiff() string {
	return "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-a\n+b\n"
}

// fetchHappyCommander scripts the GitHub happy path: view, diff, the stat
// re-view, and the two thread reads.
func fetchHappyCommander(t *testing.T) *scriptCommander {
	t.Helper()
	view := "pr\x00view\x007\x00--repo\x00o/r\x00--json\x00" +
		"number,title,body,baseRefName,headRefName,headRefOid,baseRefOid,author,url,commits," +
		"files,additions,deletions,isDraft,reviewDecision,state"
	stat := "pr\x00view\x007\x00--repo\x00o/r\x00--json\x00files\x00--jq\x00" +
		".files[] | \"\\(.path) (+\\(.additions)/-\\(.deletions))\""
	return &scriptCommander{
		t: t,
		out: map[string]scriptResult{
			"gh\x00" + view:                          {stdout: fetchTestPR()},
			"gh\x00pr\x00diff\x007\x00--repo\x00o/r": {stdout: fetchTestDiff()},
			"gh\x00" + stat:                          {stdout: "a.txt (+1/-1)\n"},
			"gh\x00api\x00repos/o/r/pulls/7/reviews\x00--paginate":  {stdout: "[]\n"},
			"gh\x00api\x00repos/o/r/pulls/7/comments\x00--paginate": {stdout: "[]\n"},
		},
	}
}

// TestRunGitHubHappyPath proves the fetch service builds the bundle behind
// the commander seam: the exact gh argv fires with no stdin, the bundle
// files land, and the typed response carries the envelope inputs.
func TestRunGitHubHappyPath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cmdr := fetchHappyCommander(t)
	var human, diag bytes.Buffer
	var streamed []string
	req := fetchTestReq(cmdr, &human, &diag, &streamed)
	resp, err := Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertScriptCalls(t, cmdr, fetchHappyArgv())
	assertBundleFiles(t, filepath.Join(".review", "7"), []string{
		"PR.json", "full.diff",
		"toc.txt", "roles.txt", "hunks.tsv", "store.json", "reviews.json", "inline.json", ".difffiles",
	})
	assertFetchFiles(t)
	assertFetchData(t, resp, &human)
}

// fetchHappyArgv is the exact gh argv the happy path fires, in order.
func fetchHappyArgv() [][]string {
	return [][]string{
		{
			"gh", "pr", "view", "7", "--repo", "o/r", "--json",
			"number,title,body,baseRefName,headRefName,headRefOid,baseRefOid,author,url,commits," +
				"files,additions,deletions,isDraft,reviewDecision,state",
		},
		{"gh", "pr", "diff", "7", "--repo", "o/r"},
		{
			"gh", "pr", "view", "7", "--repo", "o/r", "--json", "files", "--jq",
			`.files[] | "\(.path) (+\(.additions)/-\(.deletions))"`,
		},
		{"gh", "api", "repos/o/r/pulls/7/reviews", "--paginate"},
		{"gh", "api", "repos/o/r/pulls/7/comments", "--paginate"},
	}
}

// assertFetchFiles proves the canned bundle bodies landed verbatim.
func assertFetchFiles(t *testing.T) {
	t.Helper()
	if raw, err := os.ReadFile(".review/7/PR.json"); err != nil || string(raw) != fetchTestPR() {
		t.Errorf("PR.json = %q, %v", raw, err)
	}
	if raw, err := os.ReadFile(".review/7/full.diff"); err != nil || string(raw) != fetchTestDiff() {
		t.Errorf("full.diff = %q, %v", raw, err)
	}
}

// assertFetchData proves the typed response carries the envelope inputs.
func assertFetchData(t *testing.T, resp *core.FetchResponse, human *bytes.Buffer) {
	t.Helper()
	if resp.Data == nil {
		t.Fatal("no response data")
	}
	assertFetchIdentity(t, resp.Data)
	assertFetchEnvelope(t, resp, human)
}

// assertFetchIdentity proves the data identity and stats.
func assertFetchIdentity(t *testing.T, data *core.FetchData) {
	t.Helper()
	if data.Dir != ".review/7" || data.HeadSHA != fetchTestHead || data.BaseSHA != fetchTestBase {
		t.Errorf("identity = %+v", data)
	}
	if data.Files != 1 || data.Hunks != 1 || data.DiffBytes != int64(len(fetchTestDiff())) {
		t.Errorf("stats = %+v", data)
	}
	if data.IncrementalBase != nil {
		t.Errorf("incremental base = %v, want null", *data.IncrementalBase)
	}
	if len(data.Omitted) != 0 {
		t.Errorf("omitted = %v", data.Omitted)
	}
}

// assertFetchEnvelope proves the wire envelope and human lines.
func assertFetchEnvelope(t *testing.T, resp *core.FetchResponse, human *bytes.Buffer) {
	t.Helper()
	env := core.RenderEnvelope("fetch", resp.Repo, resp.Pr, resp.At, resp.Next,
		resp.Warnings, resp.Data.Wire(), nil)
	raw := jq.PrettyOrdered(env)
	if !strings.Contains(raw, `"dir": ".review/7"`) || !strings.Contains(raw, `"verb": "fetch"`) {
		t.Errorf("envelope missing identity:\n%s", raw)
	}
	if got := human.String(); !strings.Contains(got, "done: .review/7/PR.json") ||
		!strings.Contains(got, "next: feed SKILL.md") {
		t.Errorf("human lines:\n%s", got)
	}
}

// TestRunMissingTool proves a missing binary surfaces as the distinct
// missing-tool kind the adapter renders with no envelope.
func TestRunMissingTool(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{}, tools: map[string]bool{"gh": false}}
	var human, diag bytes.Buffer
	var streamed []string
	_, err := Run(context.Background(), fetchTestReq(cmdr, &human, &diag, &streamed))
	var mt *core.MissingToolError
	if !errors.As(err, &mt) || mt.Tool != "gh" {
		t.Fatalf("err = %v, want missing-tool gh", err)
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("calls fired before the need gate: %q", cmdr.calls)
	}
	// The bundle dir lands before the need gate (like the old preamble),
	// but nothing is written into it and no progress streams.
	if entries, err := os.ReadDir(".review/7"); err != nil || len(entries) != 0 {
		t.Errorf("bundle files written on missing tool: %v %v", entries, err)
	}
	if human.String() != "" || diag.String() != "" {
		t.Errorf("streams = %q %q, want silence", human.String(), diag.String())
	}
}

// TestRunCanceled proves a dead context refuses before any spawn.
func TestRunCanceled(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cmdr := fetchHappyCommander(t)
	var human, diag bytes.Buffer
	var streamed []string
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, fetchTestReq(cmdr, &human, &diag, &streamed))
	var ref *core.RefusalError
	if !errors.As(err, &ref) || ref.Code != "canceled" {
		t.Fatalf("err = %v, want canceled", err)
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("calls fired after cancel: %q", cmdr.calls)
	}
}

// TestRunBadProvider proves an unknown provider refuses typed.
func TestRunBadProvider(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cmdr := fetchHappyCommander(t)
	var human, diag bytes.Buffer
	var streamed []string
	req := fetchTestReq(cmdr, &human, &diag, &streamed)
	req.Flags.Provider = "bogus"
	_, err := Run(context.Background(), req)
	var ref *core.RefusalError
	if !errors.As(err, &ref) || ref.Code != "bad-provider" {
		t.Fatalf("err = %v, want bad-provider", err)
	}
}

// TestRunDiffTooLarge proves the over-cap fork refuses with the split
// candidates riding the refusal (the adapter renders them into the
// envelope).
func TestRunDiffTooLarge(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cmdr := fetchHappyCommander(t)
	big := "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n" +
		strings.Repeat(" context line padding the diff past the cap\n", 20000)
	view := "pr\x00view\x007\x00--repo\x00o/r\x00--json\x00" +
		"number,title,body,baseRefName,headRefName,headRefOid,baseRefOid,author,url,commits," +
		"files,additions,deletions,isDraft,reviewDecision,state"
	pr := strings.Replace(fetchTestPR(), `"files":[{"path":"a.txt","additions":1,"deletions":1}]`,
		`"files":[{"path":"a.txt","additions":1,"deletions":1},{"path":"b.txt","additions":1,"deletions":1}]`, 1)
	bigDiff := big +
		"diff --git a/b.txt b/b.txt\n--- a/b.txt\n+++ b/b.txt\n@@ -1 +1 @@\n-a\n+b\n"
	cmdr.out["gh\x00"+view] = scriptResult{stdout: pr}
	cmdr.out["gh\x00pr\x00diff\x007\x00--repo\x00o/r"] = scriptResult{stdout: bigDiff}
	var human, diag bytes.Buffer
	var streamed []string
	_, err := Run(context.Background(), fetchTestReq(cmdr, &human, &diag, &streamed))
	var ref *core.RefusalError
	if !errors.As(err, &ref) || ref.Code != "diff-too-large" {
		t.Fatalf("err = %v, want diff-too-large", err)
	}
	if len(ref.Candidates) != 3 {
		t.Errorf("candidates = %d, want 3 (paths, rest, range)", len(ref.Candidates))
	}
	if got := diag.String(); !strings.Contains(got, "candidates (suggested scopes") {
		t.Errorf("diag missing candidates block:\n%s", got)
	}
}

// TestRunLocalWorktree proves fetch-local builds the worktree bundle
// behind the seam: the exact git argv fires and source.json lands.
func TestRunLocalWorktree(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	head := "cccccccccccccccccccccccccccccccccccccccc"
	diff := "diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-a\n+b\n"
	cmdr := &scriptCommander{
		t: t,
		out: map[string]scriptResult{
			"git\x00rev-parse\x00--is-inside-work-tree":          {stdout: "true\n"},
			"git\x00branch\x00--show-current":                    {stdout: "main\n"},
			"git\x00rev-parse\x00HEAD":                           {stdout: head + "\n"},
			"git\x00diff\x00HEAD":                                {stdout: diff},
			"git\x00diff\x00--no-renames\x00--name-only\x00HEAD": {stdout: "a.txt\n"},
			"git\x00diff\x00--numstat\x00HEAD":                   {stdout: "1\t1\ta.txt\n"},
			"git\x00ls-files\x00--others\x00--exclude-standard":  {stdout: ""},
		},
	}
	var human, diag bytes.Buffer
	var streamed []string
	root := ""
	read := core.ReadRequest{
		Runtime: &core.Runtime{
			Env:       map[string]string{},
			Now:       func() time.Time { return fetchTestTime },
			Progress:  testProgress{warnings: &streamed},
			Commander: cmdr,
		},
		Repo: "o/r",
		Pr:   "mine",
		Root: &root,
	}
	req := &core.FetchLocalRequest{
		ReadRequest: read,
		Effective:   core.Defaults(),
		Warnings:    []string{},
		Human:       &human,
		Diag:        &diag,
	}
	resp, err := RunLocal(context.Background(), req)
	if err != nil {
		t.Fatalf("RunLocal: %v", err)
	}
	assertArgvContains(t, cmdr, []string{
		"git rev-parse --is-inside-work-tree",
		"git branch --show-current",
		"git rev-parse HEAD",
		"git diff HEAD",
	})
	assertLocalBundle(t, resp, &human, diff, head)
}

// assertLocalBundle proves the worktree bundle landed with the envelope
// inputs: source.json beside the diff, the worktree kind, and the
// done/next lines on the human stream.
func assertLocalBundle(t *testing.T, resp *core.FetchResponse, human *bytes.Buffer, diff, head string) {
	t.Helper()
	if resp.Data == nil || !resp.Data.Local || resp.Data.Kind != "worktree" {
		t.Fatalf("data = %+v", resp.Data)
	}
	if resp.Data.HeadSHA != "" || resp.Data.BaseSHA != head {
		t.Errorf("heads = %q %q", resp.Data.HeadSHA, resp.Data.BaseSHA)
	}
	assertLocalFiles(t, diff)
	if got := human.String(); !strings.Contains(got, "done: .review/local-mine/source.json") {
		t.Errorf("human lines:\n%s", got)
	}
}

// assertLocalFiles proves the worktree bundle bodies landed.
func assertLocalFiles(t *testing.T, diff string) {
	t.Helper()
	if raw, err := os.ReadFile(".review/local-mine/full.diff"); err != nil || string(raw) != diff {
		t.Errorf("full.diff = %q, %v", raw, err)
	}
	if _, err := os.Stat(".review/local-mine/source.json"); err != nil {
		t.Errorf("missing source.json: %v", err)
	}
}
