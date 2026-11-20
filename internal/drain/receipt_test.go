package drain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	draincore "github.com/stainless-code/verdict/internal/core/drain"
	threadscore "github.com/stainless-code/verdict/internal/core/threads"
)

func TestAgentPostEmptyReplanKeepsReceipt(t *testing.T) {
	t.Parallel()
	root, dir := setupDrainBundle(t)
	path := filepath.Join(dir, "drain.json")
	previous := &draincore.File{
		Provider: "github", PR: 7, HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Posted: []draincore.Posted{{ThreadID: "PRRT_1", ReplyID: "501", Disposition: "answer"}},
		Failed: []draincore.Failed{{ThreadID: "PRRT_2", Reason: "host rejected reply"}},
	}
	if err := draincore.WritePlan(path, previous); err != nil {
		t.Fatal(err)
	}
	cmdr := &drainHostStub{
		pageJSON: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]}}}}}`,
		self:     "verdict-bot",
	}
	out, _, code := runDrainTest(t, root, cmdr, []string{"--agent", "nonexistent-agent", "--post"})
	got, err := draincore.ReadPlan(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Posted) != 1 || got.Posted[0].ReplyID != "501" ||
		len(got.Failed) != 1 || got.Failed[0].ThreadID != "PRRT_2" {
		t.Errorf("receipt posted=%+v failed=%+v; want reply 501 and failure PRRT_2", got.Posted, got.Failed)
	}
	if code != 1 || !strings.Contains(out, `"code": "drain-incomplete"`) {
		t.Errorf("exit = %d, out=%s; want drain-incomplete", code, out)
	}
}

func TestWithdrawRetryRetainsLedgerFailure(t *testing.T) {
	t.Parallel()
	root, dir := setupDrainBundle(t)
	writePlanAndDrafts(t, dir, "github", []draincore.Reply{
		{ThreadID: "PRRT_2", WaitingCommentID: "202", Disposition: "withdraw", Body: "Withdrawing this finding."},
	}, "")
	ledger := filepath.Join(dir, "replies.json")
	cmdr := &drainHostStub{pageJSON: twoPendingPageJSON(), self: "verdict-bot"}
	cmdr.forbidWrites = func(argv []string) {
		if !isPostReply(argv) {
			t.Errorf("unexpected host write: %q", argv)
			return
		}
		if err := os.Mkdir(ledger, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	assertWithdrawLedgerFailure(t, root, dir, cmdr, "first run")
	if err := os.Remove(ledger); err != nil {
		t.Fatal(err)
	}
	cmdr.forbidWrites = func(argv []string) { t.Errorf("unexpected retry write: %q", argv) }
	cmdr.pageJSON = strings.Replace(cmdr.pageJSON, `"url":"https://example.invalid/202"}`,
		`"url":"https://example.invalid/202"},{"id":"502","databaseId":502,"author":{"__typename":"Bot","login":"verdict-bot"},"body":"Withdrawing this finding.\n\n<!-- verdict-reply thread=PRRT_2 to=202 -->","createdAt":"2026-11-03T00:00:00Z","url":"https://example.invalid/502"}`, 1)
	assertWithdrawLedgerFailure(t, root, dir, cmdr, "retry")
	assertUnresolvedWithdraw(t, dir, cmdr)
}

func assertWithdrawLedgerFailure(t *testing.T, root, dir string, cmdr *drainHostStub, phase string) {
	t.Helper()
	out, diag, code := runDrainTest(t, root, cmdr, []string{"--post"})
	if code != 1 || !strings.Contains(out, `"code": "drain-incomplete"`) {
		t.Errorf("%s exit = %d, want 1 (drain-incomplete), out=%s diag=%s", phase, code, out, diag)
	}
	plan, err := draincore.ReadPlan(filepath.Join(dir, "drain.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Failed) != 1 || plan.Failed[0].ThreadID != "PRRT_2" ||
		!strings.HasPrefix(plan.Failed[0].Reason, "could not record reply: ") {
		t.Errorf("%s receipt failures = %+v, want PRRT_2 ledger append failure", phase, plan.Failed)
	}
}

func assertUnresolvedWithdraw(t *testing.T, dir string, cmdr *drainHostStub) {
	t.Helper()
	feed, err := threadscore.ReadFile(filepath.Join(dir, "threads.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(feed.Threads) != 2 || feed.Threads[1].ID != "PRRT_2" || feed.Threads[1].Resolved {
		t.Fatalf("refreshed threads = %+v, want unresolved PRRT_2", feed.Threads)
	}
	comments := feed.Threads[1].Comments
	if len(comments) != 3 || comments[2].ID != "502" || cmdr.postCount("PRRT_2") != 1 {
		t.Errorf("refreshed comments = %+v, reply count = %d, want reply 502 and one host post", comments, cmdr.postCount("PRRT_2"))
	}
}
