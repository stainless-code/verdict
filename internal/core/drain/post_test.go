package drain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	threadscore "github.com/stainless-code/verdict/internal/core/threads"
)

type postCall struct {
	root, thread, body string
}

func postFixture(t *testing.T, bodies map[string]string) (*PostRequest, *[]postCall, *[]LedgerEntry) {
	t.Helper()
	ids := []string{"PRRT_1", "PRRT_2", "PRRT_3"}
	waiting := map[string]string{"PRRT_1": "101", "PRRT_2": "201", "PRRT_3": "301"}
	var pending []Pending
	var replies []Reply
	var threads []threadscore.Thread
	for _, id := range ids {
		pending = append(pending, Pending{
			ThreadID: id, RootCommentID: "root-" + id, WaitingCommentID: waiting[id],
			WaitingAuthor: "alice", WaitingAt: "2026-11-02T00:00:00Z", Path: "a.go",
		})
		disposition := DispositionAnswer
		if id == "PRRT_2" {
			disposition = DispositionWithdraw
		}
		body := "A considered answer."
		if custom, ok := bodies[id]; ok {
			body = custom
		}
		replies = append(replies, Reply{ThreadID: id, WaitingCommentID: waiting[id],
			Disposition: disposition, Body: body})
		rootBody := "finding\n\n" + threadscore.ReplyMarker("root", "0")
		threads = append(threads, threadscore.Thread{
			ID: id, Kind: threadscore.KindReview, Author: "verdict-bot",
			AuthorKind: threadscore.AuthorVerdict,
			Comments: []threadscore.Comment{
				{ID: "root-" + id, Author: "verdict-bot", AuthorKind: threadscore.AuthorVerdict, Body: rootBody},
				{ID: waiting[id], Author: "alice", AuthorKind: threadscore.AuthorHuman, Body: "a question"},
			},
		})
	}
	plan := &File{Provider: "github", PR: 7, Pending: pending, PlanDigest: Digest(pending)}
	raw, err := json.Marshal(RepliesFile{SchemaVersion: SchemaVersion, PlanDigest: plan.PlanDigest, Replies: replies})
	if err != nil {
		t.Fatal(err)
	}
	feed := &threadscore.File{Provider: "github", PR: 7, Threads: threads}
	var calls []postCall
	var ledger []LedgerEntry
	req := &PostRequest{
		Plan: plan, DraftsRaw: raw, Feed: feed, Provider: "github", Repo: "acme/app", PR: "7",
		At: "2026-11-05T00:00:00Z",
		Reply: func(root, thread, body string) (string, error) {
			calls = append(calls, postCall{root, thread, body})
			return "reply-" + thread, nil
		},
		Resolve:  func(string) error { return nil },
		Append:   func(entry LedgerEntry) error { ledger = append(ledger, entry); return nil },
		SaveFeed: func(*threadscore.File) error { return nil },
	}
	return req, &calls, &ledger
}

// TestPostMarksBodiesAndLedger pins the reply bytes and the order: each write
// gets a locked ledger entry, and withdraw resolves only after the reply lands.
func TestPostMarksBodiesAndLedger(t *testing.T) {
	req, calls, ledger := postFixture(t, map[string]string{
		"PRRT_1": "Ping @alice, see the note.",
	})
	var resolved []string
	req.Resolve = func(id string) error { resolved = append(resolved, id); return nil }
	res, ref := Post(req)
	if ref != nil {
		t.Fatalf("Post refused: %+v", ref)
	}
	if len(*calls) != 3 || len(*ledger) != 3 {
		t.Fatalf("calls = %d, ledger = %d, want 3 each", len(*calls), len(*ledger))
	}
	first := (*calls)[0]
	if want := threadscore.ReplyMarker("PRRT_1", "101"); !strings.HasSuffix(first.body, want) {
		t.Errorf("body %q did not end with %q", first.body, want)
	}
	if !strings.Contains(first.body, "@\u200balice") || strings.Contains(first.body, "@alice") {
		t.Errorf("mention was not neutralized: %q", first.body)
	}
	if len(resolved) != 1 || resolved[0] != "PRRT_2" {
		t.Errorf("resolved = %v, want PRRT_2", resolved)
	}
	if (*ledger)[1].InReplyTo != "201" || (*ledger)[1].By != ReplyBy || (*ledger)[1].ReplyID != "reply-PRRT_2" {
		t.Errorf("withdraw ledger entry = %+v", (*ledger)[1])
	}
	if len(res.Posted) != 3 || !res.Posted[1].Resolved || res.Incomplete {
		t.Errorf("result = %+v", res)
	}
	if req.Feed.Threads[1].Resolved != true || req.Feed.Threads[0].Resolved {
		t.Error("withdraw was not the only thread marked resolved in the feed")
	}
}

// TestPostPartialFailureRerunIsIdempotent fails write 2, then reruns with the
// recorded ledger. The thread whose reply landed is never written twice.
func TestPostPartialFailureRerunIsIdempotent(t *testing.T) {
	req, calls, ledger := postFixture(t, nil)
	failSecond := true
	req.Reply = func(root, thread, body string) (string, error) {
		if thread == "PRRT_2" && failSecond {
			return "", errors.New("host rejected the reply")
		}
		*calls = append(*calls, postCall{root, thread, body})
		return "reply-" + thread, nil
	}
	first, ref := Post(req)
	if ref != nil || !first.Incomplete || len(first.Failed) != 1 || first.Failed[0].ThreadID != "PRRT_2" {
		t.Fatalf("first result = %+v, refusal = %+v", first, ref)
	}
	if len(*ledger) != 2 {
		t.Fatalf("ledger after failure = %+v", *ledger)
	}
	req.Ledger = append([]LedgerEntry{}, (*ledger)...)
	// The live refresh reads the posted marker back as verdict-authored.
	for i := range req.Feed.Threads {
		if req.Feed.Threads[i].ID == "PRRT_1" {
			last := &req.Feed.Threads[i].Comments[1]
			last.Body += "\n\n" + threadscore.ReplyMarker("PRRT_1", last.ID)
			last.AuthorKind = threadscore.AuthorVerdict
		}
	}
	*calls = nil
	failSecond = false
	second, ref := Post(req)
	if ref != nil || second.Incomplete || len(second.Posted) != 1 {
		t.Fatalf("second result = %+v, refusal = %+v", second, ref)
	}
	for _, call := range *calls {
		if call.thread == "PRRT_1" {
			t.Errorf("rerun double-posted %q", call.thread)
		}
	}
	if !hasSkip(req.Plan.Skipped, "PRRT_1", ReasonAlreadyReplied) ||
		!hasSkip(req.Plan.Skipped, "PRRT_3", ReasonAlreadyReplied) {
		t.Errorf("rerun skips = %+v", req.Plan.Skipped)
	}
}

func hasSkip(skips []Skip, thread, reason string) bool {
	for _, skip := range skips {
		if skip.ThreadID == thread && skip.Reason == reason {
			return true
		}
	}
	return false
}

// TestPostStaleAndSecretRefusals pins the live checks before the first write:
// a new comment makes a draft stale, and a credential body refuses the whole
// run with no host call at all.
func TestPostStaleAndSecretRefusals(t *testing.T) {
	req, calls, ledger := postFixture(t, nil)
	req.Feed.Threads[0].Comments[1].ID = "999"
	res, ref := Post(req)
	if ref != nil || len(res.Posted) != 2 {
		t.Fatalf("stale result = %+v, refusal = %+v", res, ref)
	}
	if len(res.Failed) != 0 {
		t.Errorf("stale failures = %+v", res.Failed)
	}
	if !hasSkip(req.Plan.Skipped, "PRRT_1", ReasonStaleThread) {
		t.Errorf("stale skip missing from %+v", req.Plan.Skipped)
	}
	for _, call := range *calls {
		if call.thread == "PRRT_1" {
			t.Error("stale thread was posted")
		}
	}
	if len(*ledger) != 2 {
		t.Errorf("stale run wrote %d ledger entries, want 2", len(*ledger))
	}

	secretReq, secretCalls, secretLedger := postFixture(t, map[string]string{
		"PRRT_1": "key AKIAIOSFODNN7EXAMPLE here",
	})
	_, ref = Post(secretReq)
	if ref == nil || ref.Code != "post-secret-shaped" {
		t.Fatalf("secret refusal = %+v, want post-secret-shaped", ref)
	}
	if len(*secretCalls) != 0 || len(*secretLedger) != 0 {
		t.Errorf("secret draft made %d call(s) and %d ledger entry/entries", len(*secretCalls), len(*secretLedger))
	}
}

// TestPostNoDraftsRefuses pins the advice a post run gives when --agent has
// not written the artifact yet.
func TestPostNoDraftsRefuses(t *testing.T) {
	req, calls, ledger := postFixture(t, nil)
	req.DraftsRaw = nil
	_, ref := Post(req)
	if ref == nil || ref.Code != CodeDraftsInvalid ||
		!strings.Contains(ref.Message, "no drafts; run drain --agent <name> first") {
		t.Fatalf("refusal = %+v", ref)
	}
	if len(*calls) != 0 || len(*ledger) != 0 {
		t.Error("no-drafts refusal made a write")
	}
}

// TestPostRefusesUnsupportedHost pins the order: the capability gate fires
// before the drafts are posted, and the reason names the missing capability.
func TestPostRefusesUnsupportedHost(t *testing.T) {
	req, calls, ledger := postFixture(t, nil)
	req.Provider = "forgejo"
	_, ref := Post(req)
	if ref == nil || ref.Code != "provider-unimplemented" {
		t.Fatalf("refusal = %+v, want provider-unimplemented", ref)
	}
	if len(*calls) != 0 || len(*ledger) != 0 {
		t.Error("unsupported host made a write")
	}
}
