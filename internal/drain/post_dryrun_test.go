package drain

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/cli"
	draincore "github.com/stainless-code/verdict/internal/core/drain"
	threadscore "github.com/stainless-code/verdict/internal/core/threads"
)

const (
	answerOneBody = "Answer one.\n\n<!-- verdict-reply thread=PRRT_1 to=102 -->"
	answerTwoBody = "Answer two.\n\n<!-- verdict-reply thread=PRRT_2 to=202 -->"
)

func twoDryRunReplies() []draincore.Reply {
	return []draincore.Reply{
		{ThreadID: "PRRT_1", WaitingCommentID: "102", Disposition: draincore.DispositionAnswer, Body: "Answer one."},
		{ThreadID: "PRRT_2", WaitingCommentID: "202", Disposition: draincore.DispositionWithdraw, Body: "Answer two."},
	}
}

func writeFeedSentinel(t *testing.T, dir string) []byte {
	t.Helper()
	path := filepath.Join(dir, threadscore.KeyThreadsJSON)
	feed := &threadscore.File{
		SchemaVersion: threadscore.SchemaVersion,
		Provider:      "github",
		PR:            7,
		HeadSHA:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		FetchedAt:     "2026-11-17T11:00:00Z",
		Threads:       []threadscore.Thread{},
	}
	if err := threadscore.WriteFile(path, feed); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func snapshotFiles(t *testing.T, dir string, names ...string) map[string][]byte {
	t.Helper()
	snap := make(map[string][]byte, len(names))
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, fs.ErrNotExist) {
			snap[name] = nil
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		snap[name] = raw
	}
	return snap
}

func assertFilesUnchanged(t *testing.T, dir string, before map[string][]byte) {
	t.Helper()
	for name, want := range before {
		got := snapshotFiles(t, dir, name)[name]
		if !bytes.Equal(got, want) {
			t.Errorf("%s changed: got %q, want %q", name, got, want)
		}
	}
}

func forbidHostWrites(t *testing.T) func([]string) {
	t.Helper()
	return func(argv []string) {
		t.Fatalf("dry run attempted host write: %v", argv)
	}
}

func TestPostDryRunWritesPreviewWithoutHostOrStoreWrites(t *testing.T) {
	t.Parallel()
	root, dir := setupDrainBundle(t)
	writePlanAndDrafts(t, dir, "github", twoDryRunReplies(), "")
	writeFeedSentinel(t, dir)
	before := snapshotFiles(t, dir, draincore.KeyLedgerJSON, draincore.KeyPlanJSON,
		draincore.KeyProcedureMD, threadscore.KeyThreadsJSON)
	cmdr := &drainHostStub{pageJSON: twoPendingPageJSON(), self: "verdict-bot", forbidWrites: forbidHostWrites(t)}
	out, diag, code := runDrainTest(t, root, cmdr, []string{"--post", "--dry-run"})
	if code != cli.ExitOK {
		t.Fatalf("exit = %d, out=%s diag=%s", code, out, diag)
	}
	assertFilesUnchanged(t, dir, before)
	calls := cmdr.callLog()
	if len(calls) != 3 || !isOriginProbe(calls[0]) ||
		!strings.Contains(calls[1], "api graphql") ||
		!strings.Contains(calls[2], "api user --jq .login") {
		t.Errorf("host calls = %v, want one origin probe, one live refresh, one login read", calls)
	}

	env := decodeDryRunEnvelope(t, out)
	preview := readDryRunPreview(t, dir)
	assertDryRunEnvelope(t, env, preview)
	assertDryRunPlanned(t, preview)
	wantPath := filepath.ToSlash(dir) + "/" + draincore.KeyDryRunJSON
	wantHuman := "» 2 planned, 0 skipped (dry run; nothing posted) -> " + wantPath + "\n"
	if !strings.Contains(diag, wantHuman) {
		t.Errorf("human output missing %q:\n%s", wantHuman, diag)
	}
}

type dryRunEnvelope struct {
	Next  string          `json:"next"`
	Error any             `json:"error"`
	Data  json.RawMessage `json:"data"`
}

func decodeDryRunEnvelope(t *testing.T, out string) *dryRunEnvelope {
	t.Helper()
	var env dryRunEnvelope
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, out)
	}
	return &env
}

func readDryRunPreview(t *testing.T, dir string) *draincore.DryRunFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, draincore.KeyDryRunJSON))
	if err != nil {
		t.Fatalf("read preview: %v", err)
	}
	var preview draincore.DryRunFile
	if err := json.Unmarshal(raw, &preview); err != nil {
		t.Fatalf("decode preview: %v\n%s", err, raw)
	}
	return &preview
}

func assertDryRunEnvelope(t *testing.T, env *dryRunEnvelope, preview *draincore.DryRunFile) {
	t.Helper()
	if env.Error != nil {
		t.Errorf("error = %v, want nil", env.Error)
	}
	wantNext := "verdict drain acme/app 7 --post"
	if env.Next != wantNext {
		t.Errorf("next = %q, want %q", env.Next, wantNext)
	}
	var fromEnvelope draincore.DryRunFile
	if err := json.Unmarshal(env.Data, &fromEnvelope); err != nil {
		t.Fatalf("decode envelope data: %v\n%s", err, env.Data)
	}
	if !bytes.Equal(mustMarshal(t, preview), mustMarshal(t, fromEnvelope)) {
		t.Errorf("envelope data != preview: %s", env.Data)
	}
}

func assertDryRunPlanned(t *testing.T, preview *draincore.DryRunFile) {
	t.Helper()
	if preview.SchemaVersion != draincore.SchemaVersion || preview.Provider != "github" || preview.PR != 7 {
		t.Errorf("preview identity = %+v", preview)
	}
	if len(preview.Planned) != 2 {
		t.Fatalf("planned = %+v, want 2 entries", preview.Planned)
	}
	wantPlanned := []draincore.Planned{
		{ThreadID: "PRRT_1", RootCommentID: "101", Disposition: string(draincore.DispositionAnswer), Body: answerOneBody},
		{ThreadID: "PRRT_2", RootCommentID: "201", Disposition: string(draincore.DispositionWithdraw), Body: answerTwoBody},
	}
	for i, want := range wantPlanned {
		if preview.Planned[i] != want {
			t.Errorf("planned[%d] = %+v, want %+v", i, preview.Planned[i], want)
		}
	}
	if len(preview.Skipped) != 0 {
		t.Errorf("skipped = %+v, want none", preview.Skipped)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDryRunWithoutPostIsUsageError(t *testing.T) {
	t.Parallel()
	root, _ := setupDrainBundle(t)
	cmdr := &drainHostStub{pageJSON: twoPendingPageJSON(), self: "verdict-bot", forbidWrites: forbidHostWrites(t)}
	out, diag, code := runDrainTest(t, root, cmdr, []string{"--dry-run"})
	if code != cli.ExitUsage {
		t.Fatalf("exit = %d, want %d (out=%s diag=%s)", code, cli.ExitUsage, out, diag)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty usage output", out)
	}
	if !strings.Contains(diag, "--post") {
		t.Errorf("usage message %q does not name --post", diag)
	}
	if calls := cmdr.callLog(); len(calls) != 0 {
		t.Errorf("usage error made %d host call(s): %v", len(calls), calls)
	}
}

func TestPostDryRunSecretRefusesWithZeroWrites(t *testing.T) {
	t.Parallel()
	root, dir := setupDrainBundle(t)
	secret := []draincore.Reply{
		{ThreadID: "PRRT_1", WaitingCommentID: "102", Disposition: draincore.DispositionAnswer, Body: "key AKIAIOSFODNN7EXAMPLE"},
	}
	writePlanAndDrafts(t, dir, "github", secret, "")
	before := snapshotFiles(t, dir, draincore.KeyLedgerJSON, draincore.KeyPlanJSON,
		draincore.KeyProcedureMD, threadscore.KeyThreadsJSON, draincore.KeyDryRunJSON)
	cmdr := &drainHostStub{pageJSON: twoPendingPageJSON(), self: "verdict-bot", forbidWrites: forbidHostWrites(t)}
	out, diag, code := runDrainTest(t, root, cmdr, []string{"--post", "--dry-run"})
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want %d (out=%s diag=%s)", code, cli.ExitRefused, out, diag)
	}
	assertRefusalCode(t, out, "post-secret-shaped")
	assertFilesUnchanged(t, dir, before)
	if calls := cmdr.callLog(); len(calls) != 0 {
		t.Errorf("secret refusal made %d host call(s): %v", len(calls), calls)
	}
}

func TestPostDryRunNextKeepsAgentArgs(t *testing.T) {
	t.Parallel()
	fl, code, done := parseFlags(&cli.Runner{}, []string{"--agent", "claude", "--post", "--dry-run", "--timeout", "7", "--", "--model", "x"})
	if done || code != 0 || !fl.dryRun {
		t.Fatalf("parse = %+v, %d, %v", fl, code, done)
	}
	got := dryRunNext(Request{Repo: "acme/app", Pr: "7"}, fl)
	want := "verdict drain acme/app 7 --agent claude --post --timeout 7 -- --model x"
	if got != want {
		t.Errorf("next = %q, want %q", got, want)
	}
}

// TestDryRunWithAgentLeavesBundleProcedureAbsent proves the plan's embedded
// Procedure is not written to the real bundle when dry-run writers are blocked.
func TestDryRunWithAgentLeavesBundleProcedureAbsent(t *testing.T) {
	t.Parallel()
	root, dir := setupDrainBundle(t)
	cmdr := &drainHostStub{
		pageJSON:     `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]}}}}}`,
		self:         "verdict-bot",
		forbidWrites: forbidHostWrites(t),
	}
	out, diag, code := runDrainTest(t, root, cmdr, []string{"--agent", "nonexistent-agent", "--post", "--dry-run"})
	if code != cli.ExitOK {
		t.Fatalf("exit = %d, out=%s diag=%s", code, out, diag)
	}
	path := filepath.Join(dir, draincore.KeyProcedureMD)
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("dry run wrote %s: %v", filepath.ToSlash(path), err)
	}
}

// isOriginProbe reports the hook-free `git remote get-url origin` read, as the
// call log joins it.
func isOriginProbe(call string) bool {
	return strings.HasPrefix(call, "git -C ") && strings.Contains(call, " -c core.hooksPath=") &&
		strings.HasSuffix(call, " remote get-url origin")
}
