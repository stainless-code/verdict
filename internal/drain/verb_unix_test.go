//go:build unix

package drain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
	draincore "github.com/stainless-code/verdict/internal/core/drain"
)

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

func onePendingPageJSON() string {
	return `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[
		{"id":"PRRT_1","isResolved":false,"isOutdated":false,"path":"a.go","line":10,"startLine":null,"diffSide":"RIGHT","comments":{"totalCount":2,"nodes":[
			{"id":"101","databaseId":101,"author":{"__typename":"Bot","login":"verdict-bot"},"body":"finding\n\n<!-- verdict-finding sig=6c3ee749e3d1f97870c9e8b5c1171fae2cb9ff4e -->","createdAt":"2026-11-01T00:00:00Z","url":"https://example.invalid/101"},
			{"id":"102","databaseId":102,"author":{"__typename":"User","login":"alice"},"body":"why?","createdAt":"2026-11-02T00:00:00Z","url":"https://example.invalid/102"}
		]}}
	]}}}}}`
}
