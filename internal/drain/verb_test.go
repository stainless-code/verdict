package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	draincore "github.com/stainless-code/verdict/internal/core/drain"
)

type stubCommander struct{}

func (*stubCommander) Run(context.Context, *core.Command) (core.Result, error) {
	return core.Result{}, nil
}

func TestNextAfterAgentRecommendsPost(t *testing.T) {
	t.Parallel()
	got := next(Request{Repo: "acme/app", Pr: "7"}, &flags{agent: "claude"})
	const wantCmd = "verdict drain acme/app 7 --post"
	if !strings.Contains(got, wantCmd) {
		t.Fatalf("next = %q, want %q", got, wantCmd)
	}
	_, after, ok := strings.Cut(got, "verdict drain ")
	if !ok {
		t.Fatalf("next %q did not start command with verdict drain", got)
	}
	fields := strings.Fields(after)
	if len(fields) < 3 || fields[0] != "acme/app" || fields[1] != "7" {
		t.Fatalf("recommended args = %v, want repo pr --post", fields)
	}
	fl, code, done := parseFlags(&cli.Runner{}, fields[2:])
	if done || code != 0 || !fl.post {
		t.Fatalf("recommended flags did not parse: fl=%+v code=%d done=%v", fl, code, done)
	}
}

func TestParseFlagsAcceptsPost(t *testing.T) {
	t.Parallel()
	fl, code, done := parseFlags(&cli.Runner{}, []string{"--agent", "claude", "--post"})
	if done || code != 0 || !fl.post || fl.agent != "claude" {
		t.Fatalf("parse = %+v, %d, %v", fl, code, done)
	}
	if !strings.Contains(wantFlags, "--post") {
		t.Errorf("usage %q does not offer --post", wantFlags)
	}
}

func TestRefreshRequestKeepsInjectedCommander(t *testing.T) {
	t.Parallel()
	cmdr := &stubCommander{}
	req := Request{Runtime: &core.Runtime{Commander: cmdr}}
	r := &cli.Runner{Now: time.Now}
	got := refreshRequest(r, req, &flags{provider: "github"})
	if got.Commander != cmdr {
		t.Errorf("refresh commander = %T, want the injected commander", got.Commander)
	}
}

func TestRunAgentAndPostComposesOneRun(t *testing.T) {
	root, dir := setupDrainBundle(t)
	installStubClaude(t)
	cmdr := &drainHostStub{pageJSON: onePendingPageJSON(), self: "verdict-bot"}
	out, diag, code := runDrainTest(t, root, cmdr, []string{"--agent", "claude", "--post"})
	if code != cli.ExitOK {
		t.Fatalf("Run exit = %d, out=%s, diag=%s", code, out, diag)
	}
	assertOneRunPosted(t, out, dir, cmdr)
}

func installStubClaude(t *testing.T) {
	t.Helper()
	binDir := t.TempDir()
	script := `#!/bin/sh
set -eu
prompt=""
for arg in "$@"; do prompt="$arg"; done
target="${prompt#*Write exactly one file, }"
target="${target%%,*}"
plan_dir="$(dirname "$target")"
digest="$(sed -n 's/.*"plan_digest": *"\([^"]*\)".*/\1/p' "$plan_dir/drain.json" | head -n 1)"
printf '{"schema_version":"1","plan_digest":"%s","replies":[{"thread_id":"PRRT_1","waiting_comment_id":"102","disposition":"answer","body":"Answer one."}]}\n' "$digest" >"$target"
`
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func assertOneRunPosted(t *testing.T, out, dir string, cmdr *drainHostStub) {
	t.Helper()
	var env struct {
		Data  *draincore.File `json:"data"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, out)
	}
	if env.Error != nil || env.Data == nil || env.Data.Drafts == nil || env.Data.Drafts.Count != 1 {
		t.Fatalf("envelope = %+v", env)
	}
	if len(env.Data.Posted) != 1 || env.Data.Posted[0].ThreadID != "PRRT_1" || env.Data.Posted[0].ReplyID != "501" {
		t.Fatalf("posted = %+v, want 1 posted reply 501", env.Data.Posted)
	}
	assertOneRunLedgerAndCalls(t, dir, cmdr)
}

func assertOneRunLedgerAndCalls(t *testing.T, dir string, cmdr *drainHostStub) {
	t.Helper()
	if cmdr.postCount("PRRT_1") != 1 {
		t.Errorf("host post calls = %d, want 1 (calls=%v)", cmdr.postCount("PRRT_1"), cmdr.calls)
	}
	entries, err := ReadLedger(filepath.Join(dir, draincore.KeyLedgerJSON))
	if err != nil || len(entries) != 1 || entries[0].ReplyID != "501" {
		t.Errorf("ledger = %+v, err=%v", entries, err)
	}
}

func TestRunAgentAndPostZeroPendingSkipsLaunchAndPost(t *testing.T) {
	t.Parallel()
	root, _ := setupDrainBundle(t)
	cmdr := &drainHostStub{
		pageJSON: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]}}}}}`,
		self:     "verdict-bot",
	}
	var out, diag bytes.Buffer
	r := &cli.Runner{
		Out: &out, Diag: &diag, Human: &diag,
		Now: func() time.Time { return time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC) },
		Cmd: "drain", JSONMode: true, Effective: cli.Defaults(),
	}
	req := Request{
		Runtime:  &core.Runtime{Env: map[string]string{}, Commander: cmdr},
		Repo:     "acme/app",
		Pr:       "7",
		RepoRoot: root,
	}
	code := Run(context.Background(), r, []string{"--agent", "nonexistent-agent", "--post"}, req)
	if code != cli.ExitOK {
		t.Fatalf("Run exit = %d, out=%s, diag=%s", code, out.String(), diag.String())
	}
	var env struct {
		Data  *draincore.File `json:"data"`
		Error any             `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, out.String())
	}
	if env.Error != nil || env.Data == nil || len(env.Data.Pending) != 0 || len(env.Data.Posted) != 0 {
		t.Errorf("envelope = %+v, want 0 pending, 0 posted, nil error", env)
	}
}
