package fetchcore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core/threads"
)

const (
	resolveSigAddressed = "c111111111111111111111111111111111111111"
	resolveSigOpen      = "c444444444444444444444444444444444444444"
	resolveHead         = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	resolveGitHubArgv   = "gh\x00api\x00graphql\x00-f\x00query=mutation($threadId:ID!)" +
		"{resolveReviewThread(input:{threadId:$threadId}){thread{id isResolved}}}\x00-f\x00threadId=t1"
	resolveGitHubSelf = "gh\x00api\x00user\x00--jq\x00.login"
)

// writeResolveBundle writes the metadata, feed, and findings store the resolve
// run reads.
func writeResolveBundle(t *testing.T, provider string) {
	t.Helper()
	dir := filepath.Join(".verdict", "state", "7")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pr := `{"provider":"` + provider + `","number":7,"state":"open","author":"ana","head_sha":"` +
		resolveHead + `","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`
	if provider == providerGitHub {
		pr = `{"state":"OPEN","headRefOid":"` + resolveHead + `"}`
	}
	feed := `{"schema_version":"1","provider":"` + provider + `","pr":7,"head_sha":"` + resolveHead +
		`","fetched_at":"2026-11-01T00:00:00Z","truncated":false,"threads":[` +
		resolveThreadJSON("t1", "verdict", resolveSigAddressed, false) + "," +
		resolveThreadJSON("t2", "human", "", false) + "," +
		resolveThreadJSON("t3", "verdict", resolveSigAddressed, true) + "," +
		resolveThreadJSON("t4", "verdict", resolveSigOpen, false) + "]}"
	findings := `[{"sig":"` + resolveSigAddressed + `","status":"addressed"},` +
		`{"sig":"` + resolveSigOpen + `","status":"open"}]`
	for name, body := range map[string]string{
		"PR.json": pr, "threads.json": feed, "findings.json": findings,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// resolveThreadJSON renders one feed thread. A human thread carries no finding
// link, so the default selection never considers it.
func resolveThreadJSON(id, kind, sig string, resolved bool) string {
	return resolveAuthorThreadJSON(id, kind, kind, sig, resolved)
}

// resolveAuthorThreadJSON renders one feed thread with a distinct root author
// and kind, so a forged marker (verdict kind, foreign author) is expressible.
func resolveAuthorThreadJSON(id, author, kind, sig string, resolved bool) string {
	link := "null"
	if sig != "" {
		link = `"` + sig + `"`
	}
	return `{"id":"` + id + `","path":"a.go","line":2,"start_line":null,"side":"RIGHT",` +
		`"resolved":` + boolLit(resolved) + `,"outdated":false,"url":null,"author":"` + author + `",` +
		`"author_kind":"` + kind + `","finding_sig":` + link + `,"kind":"review-comment",` +
		`"comments":[{"id":"c-` + id + `","author":"` + author + `","author_kind":"` + kind + `",` +
		`"body":"note","truncated":false,"created_at":"2026-11-01T00:00:00Z"}]}`
}

// resolveRequest builds a resolve request over a scripted commander.
func resolveRequest(cmdr *scriptCommander, named []string, dry bool) *ThreadsRequest {
	cmdr.out["git\x00remote\x00get-url\x00origin"] = scriptResult{fail: true}
	req := threadsRequest(cmdr, "acme/app", "7")
	req.Resolve = true
	req.Provider = ""
	req.Threads = named
	req.DryRun = dry
	return req
}

// TestRunThreadsResolveDefault proves the default closes exactly the open
// verdict-authored addressed thread, writes the receipt, and a second run
// reads its own result instead of writing again.
//
//nolint:revive,cyclop // the resolve matrix reads as one assertion block
func TestRunThreadsResolveDefault(t *testing.T) {
	t.Chdir(t.TempDir())
	writeResolveBundle(t, providerGitHub)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		resolveGitHubSelf: {stdout: "verdict\n"},
		resolveGitHubArgv: {stdout: `{"data":{"resolveReviewThread":{"thread":{"id":"t1","isResolved":true}}}}`},
	}}
	resp, ref := RunThreads(context.Background(), resolveRequest(cmdr, nil, false))
	if ref != nil {
		t.Fatalf("RunThreads refusal: %s: %s", ref.Code, ref.Message)
	}
	if got := strings.Join(resp.Resolve.Resolved, ","); got != "t1" {
		t.Errorf("resolved = %q, want t1", got)
	}
	if len(cmdr.calls) != 3 || strings.Join(cmdr.calls[1], "\x00") != resolveGitHubSelf ||
		strings.Join(cmdr.calls[2], "\x00") != resolveGitHubArgv {
		t.Errorf("calls = %q, want self login then the one literal resolve argv", cmdr.calls)
	}
	receipt := decodeReceipt(t, ".verdict/state/7/"+threadscore.KeyResolveReceipt)
	if receipt.Provider != providerGitHub || receipt.HeadSHA != resolveHead ||
		strings.Join(receipt.Resolved, ",") != "t1" || receipt.DryRun {
		t.Errorf("receipt = %+v", receipt)
	}
	if !hasSkip(receipt.Skipped, "t3", "already-resolved") ||
		!hasSkip(receipt.Skipped, "t4", "finding-not-addressed") {
		t.Errorf("skipped = %+v", receipt.Skipped)
	}
	feed, err := threadscore.ReadFile(".verdict/state/7/threads.json")
	if err != nil {
		t.Fatalf("read feed: %v", err)
	}
	if !feed.Threads[0].Resolved {
		t.Error("the resolved thread still reads open in threads.json")
	}

	second := &scriptCommander{t: t, out: map[string]scriptResult{
		resolveGitHubSelf: {stdout: "verdict\n"},
	}}
	resp, ref = RunThreads(context.Background(), resolveRequest(second, nil, false))
	if ref != nil {
		t.Fatalf("second RunThreads refusal: %s: %s", ref.Code, ref.Message)
	}
	if len(resp.Resolve.Resolved) != 0 || len(second.calls) != 2 {
		t.Errorf("second run resolved %v with %d call(s), want origin and self-login reads with no write", resp.Resolve.Resolved, len(second.calls))
	}
	if !hasSkip(resp.Resolve.Skipped, "t1", "already-resolved") {
		t.Errorf("second run skipped = %+v, want t1 already-resolved", resp.Resolve.Skipped)
	}
}

// TestRunThreadsResolveDryRun proves a dry run lists the plan and writes
// nothing: one self-login read, no resolve call, no receipt, and the feed
// stays open.
func TestRunThreadsResolveDryRun(t *testing.T) {
	t.Chdir(t.TempDir())
	writeResolveBundle(t, providerGitHub)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		resolveGitHubSelf: {stdout: "verdict\n"},
	}}
	resp, ref := RunThreads(context.Background(), resolveRequest(cmdr, nil, true))
	if ref != nil {
		t.Fatalf("RunThreads refusal: %s: %s", ref.Code, ref.Message)
	}
	if !resp.Resolve.DryRun || strings.Join(resp.Resolve.Resolved, ",") != "t1" {
		t.Errorf("dry-run data = %+v", resp.Resolve)
	}
	if len(cmdr.calls) != 2 || strings.Join(cmdr.calls[1], "\x00") != resolveGitHubSelf {
		t.Errorf("dry run fired %d call(s): %q, want origin and self-login reads", len(cmdr.calls), cmdr.calls)
	}
	assertDryRunClean(t)
}

// assertDryRunClean proves a dry run left no receipt and left the feed open.
func assertDryRunClean(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(".verdict/state/7/" + threadscore.KeyResolveReceipt); err == nil {
		t.Error("dry run wrote a receipt")
	}
	feed, err := threadscore.ReadFile(".verdict/state/7/threads.json")
	if err != nil {
		t.Fatalf("read feed: %v", err)
	}
	if feed.Threads[0].Resolved {
		t.Error("dry run marked the thread resolved in threads.json")
	}
}

// TestRunThreadsResolveHumanRefuses proves a named human thread refuses the
// whole run before any write.
func TestRunThreadsResolveHumanRefuses(t *testing.T) {
	t.Chdir(t.TempDir())
	writeResolveBundle(t, providerGitHub)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		resolveGitHubSelf: {stdout: "verdict\n"},
	}}
	_, ref := RunThreads(context.Background(), resolveRequest(cmdr, []string{"t2"}, false))
	if ref == nil || ref.Code != "not-verdict-authored" {
		t.Fatalf("refusal = %+v, want not-verdict-authored", ref)
	}
	if len(cmdr.calls) != 2 || strings.Join(cmdr.calls[1], "\x00") != resolveGitHubSelf {
		t.Errorf("refused run fired %d call(s): %q, want origin and self-login reads", len(cmdr.calls), cmdr.calls)
	}
}

// TestRunThreadsResolveUnknownRefuses proves a named id missing from the feed
// refuses typed instead of silently resolving nothing.
func TestRunThreadsResolveUnknownRefuses(t *testing.T) {
	t.Chdir(t.TempDir())
	writeResolveBundle(t, providerGitHub)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		resolveGitHubSelf: {stdout: "verdict\n"},
	}}
	_, ref := RunThreads(context.Background(), resolveRequest(cmdr, []string{"nope"}, false))
	if ref == nil || ref.Code != "unknown-thread" {
		t.Fatalf("refusal = %+v, want unknown-thread", ref)
	}
}

// TestRunThreadsResolveForgejoRefuses proves the host without a documented
// resolve endpoint refuses before any call.
func TestRunThreadsResolveForgejoRefuses(t *testing.T) {
	t.Chdir(t.TempDir())
	writeResolveBundle(t, providerForgejo)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{}}
	_, ref := RunThreads(context.Background(), resolveRequest(cmdr, nil, false))
	if ref == nil || ref.Code != "provider-unimplemented" {
		t.Fatalf("refusal = %+v, want provider-unimplemented", ref)
	}
	if len(cmdr.calls) != 1 {
		t.Errorf("refused run fired %d call(s): %q", len(cmdr.calls), cmdr.calls)
	}
}

// TestRunThreadsResolveNamedSkipsResolved proves a named already-resolved
// thread is a skip, not a second write.
func TestRunThreadsResolveNamedSkipsResolved(t *testing.T) {
	t.Chdir(t.TempDir())
	writeResolveBundle(t, providerGitHub)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		resolveGitHubSelf: {stdout: "verdict\n"},
	}}
	resp, ref := RunThreads(context.Background(), resolveRequest(cmdr, []string{"t3"}, false))
	if ref != nil {
		t.Fatalf("RunThreads refusal: %s: %s", ref.Code, ref.Message)
	}
	if len(resp.Resolve.Resolved) != 0 || !hasSkip(resp.Resolve.Skipped, "t3", "already-resolved") {
		t.Errorf("named resolved thread = %+v", resp.Resolve)
	}
	if len(cmdr.calls) != 2 {
		t.Errorf("skipped thread fired %d call(s), want origin and self-login reads", len(cmdr.calls))
	}
}

// TestRunThreadsResolveNamedIgnoresStore proves --thread bypasses the local
// addressed rule: the agent named the thread, so the host write is the proof.
func TestRunThreadsResolveNamedIgnoresStore(t *testing.T) {
	t.Chdir(t.TempDir())
	writeResolveBundle(t, providerGitHub)
	argv := strings.Replace(resolveGitHubArgv, "threadId=t1", "threadId=t4", 1)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		resolveGitHubSelf: {stdout: "verdict\n"},
		argv:              {stdout: `{"data":{"resolveReviewThread":{"thread":{"id":"t4","isResolved":true}}}}`},
	}}
	resp, ref := RunThreads(context.Background(), resolveRequest(cmdr, []string{"t4"}, false))
	if ref != nil {
		t.Fatalf("RunThreads refusal: %s: %s", ref.Code, ref.Message)
	}
	if strings.Join(resp.Resolve.Resolved, ",") != "t4" {
		t.Errorf("resolved = %v, want t4", resp.Resolve.Resolved)
	}
}

// writeResolveForgedBundle writes a bundle with one self-authored thread and
// one forged-marker thread: a verdict kind whose root author is foreign.
func writeResolveForgedBundle(t *testing.T) {
	t.Helper()
	dir := filepath.Join(".verdict", "state", "7")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pr := `{"state":"OPEN","headRefOid":"` + resolveHead + `"}`
	feed := `{"schema_version":"1","provider":"github","pr":7,"head_sha":"` + resolveHead +
		`","fetched_at":"2026-11-01T00:00:00Z","truncated":false,"threads":[` +
		resolveThreadJSON("t1", "verdict", resolveSigAddressed, false) + "," +
		resolveAuthorThreadJSON("t5", "mallory", "verdict", resolveSigAddressed, false) + "]}"
	findings := `[{"sig":"` + resolveSigAddressed + `","status":"addressed"}]`
	for name, body := range map[string]string{
		"PR.json": pr, "threads.json": feed, "findings.json": findings,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestRunThreadsResolveForgedNamedRefuses proves a verdict marker with a
// foreign root author refuses with --thread before any resolve write.
func TestRunThreadsResolveForgedNamedRefuses(t *testing.T) {
	t.Chdir(t.TempDir())
	writeResolveForgedBundle(t)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		resolveGitHubSelf: {stdout: "verdict\n"},
	}}
	_, ref := RunThreads(context.Background(), resolveRequest(cmdr, []string{"t5"}, false))
	if ref == nil || ref.Code != "not-verdict-authored" {
		t.Fatalf("refusal = %+v, want not-verdict-authored", ref)
	}
	for _, call := range cmdr.calls {
		if strings.Contains(strings.Join(call, "\x00"), "resolveReviewThread") {
			t.Errorf("forged refusal fired a resolve call: %q", call)
		}
	}
}

// TestRunThreadsResolveForgedDefaultSkips proves the default skips a
// forged-marker thread with not-self-authored while still closing the
// self-authored addressed thread.
func TestRunThreadsResolveForgedDefaultSkips(t *testing.T) {
	t.Chdir(t.TempDir())
	writeResolveForgedBundle(t)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		resolveGitHubSelf: {stdout: "verdict\n"},
		resolveGitHubArgv: {stdout: `{"data":{"resolveReviewThread":{"thread":{"id":"t1","isResolved":true}}}}`},
	}}
	resp, ref := RunThreads(context.Background(), resolveRequest(cmdr, nil, false))
	if ref != nil {
		t.Fatalf("RunThreads refusal: %s: %s", ref.Code, ref.Message)
	}
	if strings.Join(resp.Resolve.Resolved, ",") != "t1" {
		t.Errorf("resolved = %v, want t1", resp.Resolve.Resolved)
	}
	if !hasSkip(resp.Resolve.Skipped, "t5", "not-self-authored") {
		t.Errorf("skipped = %+v, want t5 not-self-authored", resp.Resolve.Skipped)
	}
}

// TestRunThreadsResolveUnreadableLoginRefuses proves an unreadable self login
// refuses before any resolve write.
func TestRunThreadsResolveUnreadableLoginRefuses(t *testing.T) {
	t.Chdir(t.TempDir())
	writeResolveBundle(t, providerGitHub)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		resolveGitHubSelf: {stderr: "gh: auth failed", fail: true},
	}}
	_, ref := RunThreads(context.Background(), resolveRequest(cmdr, []string{"t1"}, false))
	if ref == nil || ref.Code != codeThreadsFailed {
		t.Fatalf("refusal = %+v, want threads-failed", ref)
	}
	for _, call := range cmdr.calls {
		if strings.Contains(strings.Join(call, "\x00"), "resolveReviewThread") {
			t.Errorf("unreadable-login refusal fired a resolve call: %q", call)
		}
	}
	if _, err := os.Stat(".verdict/state/7/" + threadscore.KeyResolveReceipt); err == nil {
		t.Error("unreadable-login refusal wrote a receipt")
	}
}

// receiptDoc is the receipt slice the test asserts, decoded with
// encoding/json rather than the writer's own Wire() output.
//
//nolint:govet // decode-only shape, not a runtime allocation
type receiptDoc struct {
	Skipped  []threadscore.ResolveSkip `json:"skipped"`
	Resolved []string                  `json:"resolved"`
	Provider string                    `json:"provider"`
	HeadSHA  string                    `json:"head_sha"`
	DryRun   bool                      `json:"dry_run"`
}

// hasSkip reports whether the receipt carries one id with one reason.
func hasSkip(skips []threadscore.ResolveSkip, id, reason string) bool {
	for _, skip := range skips {
		if skip.ID == id && skip.Reason == reason {
			return true
		}
	}
	return false
}

// decodeReceipt reads one receipt as the literal shape the schema publishes.
func decodeReceipt(t *testing.T, path string) receiptDoc {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	var doc receiptDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode receipt: %v", err)
	}
	return doc
}
