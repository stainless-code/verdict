package drain

import (
	"reflect"
	"testing"

	threadscore "github.com/stainless-code/verdict/internal/core/threads"
)

func line(n int) *int { return &n }

func sig(s string) *string { return &s }

func thread(id, kind, author, authorKind string, resolved, outdated bool, comments ...threadscore.Comment) threadscore.Thread {
	return threadscore.Thread{
		ID: id, Kind: kind, Author: author, AuthorKind: authorKind,
		Resolved: resolved, Outdated: outdated, Path: "a.go", Line: line(10),
		FindingSig: sig("abc"), Comments: comments,
	}
}

func comment(id, author, authorKind, at string) threadscore.Comment {
	return threadscore.Comment{ID: id, Author: author, AuthorKind: authorKind, CreatedAt: at}
}

func TestSelectWaitingRules(t *testing.T) {
	t.Parallel()
	feed := &threadscore.File{Threads: []threadscore.Thread{
		thread("review", threadscore.KindReview, "verdict-bot", threadscore.AuthorVerdict, false, true,
			comment("r1", "verdict-bot", threadscore.AuthorVerdict, "2026-11-01T00:00:00Z"),
			comment("w1", "alice", threadscore.AuthorHuman, "2026-11-02T00:00:00Z")),
		thread("resolved", threadscore.KindReview, "verdict-bot", threadscore.AuthorVerdict, true, false,
			comment("r2", "verdict-bot", threadscore.AuthorVerdict, "2026-11-01T00:00:00Z"),
			comment("w2", "alice", threadscore.AuthorHuman, "2026-11-02T00:00:00Z")),
		thread("bot", threadscore.KindReview, "verdict-bot", threadscore.AuthorVerdict, false, false,
			comment("r3", "verdict-bot", threadscore.AuthorVerdict, "2026-11-01T00:00:00Z"),
			comment("w3", "dependabot[bot]", threadscore.AuthorBot, "2026-11-02T00:00:00Z")),
		thread("answered", threadscore.KindReview, "verdict-bot", threadscore.AuthorVerdict, false, false,
			comment("r4", "verdict-bot", threadscore.AuthorVerdict, "2026-11-01T00:00:00Z"),
			comment("w4", "alice", threadscore.AuthorHuman, "2026-11-02T00:00:00Z")),
		thread("verdict-last", threadscore.KindReview, "verdict-bot", threadscore.AuthorVerdict, false, false,
			comment("r5", "verdict-bot", threadscore.AuthorVerdict, "2026-11-01T00:00:00Z"),
			comment("w5", "alice", threadscore.AuthorHuman, "2026-11-02T00:00:00Z"),
			comment("v5", "verdict-bot", threadscore.AuthorVerdict, "2026-11-03T00:00:00Z")),
		thread("issue", threadscore.KindIssue, "verdict-bot", threadscore.AuthorVerdict, false, false,
			comment("r6", "verdict-bot", threadscore.AuthorVerdict, "2026-11-01T00:00:00Z"),
			comment("w6", "alice", threadscore.AuthorHuman, "2026-11-02T00:00:00Z")),
		thread("human", threadscore.KindReview, "alice", threadscore.AuthorHuman, false, false,
			comment("r7", "alice", threadscore.AuthorHuman, "2026-11-01T00:00:00Z"),
			comment("w7", "bob", threadscore.AuthorHuman, "2026-11-02T00:00:00Z")),
		thread("solo", threadscore.KindReview, "verdict-bot", threadscore.AuthorVerdict, false, false,
			comment("r8", "verdict-bot", threadscore.AuthorVerdict, "2026-11-01T00:00:00Z"),
			comment("w8", "operator", threadscore.AuthorHuman, "2026-11-04T00:00:00Z")),
	}}
	ledger := []LedgerEntry{
		{Thread: "answered", ReplyID: "9", InReplyTo: "w4", At: "2026-11-03T00:00:00Z", By: ReplyBy},
	}
	pending, skipped, deferred := Select(feed, "verdict-bot", ledger, 20)
	wantPending := []string{"review", "solo"}
	if got := ids(pending); !reflect.DeepEqual(got, wantPending) {
		t.Errorf("pending = %v, want %v", got, wantPending)
	}
	wantSkipped := []Skip{
		{ThreadID: "resolved", Reason: ReasonResolved},
		{ThreadID: "bot", Reason: ReasonBotReply},
		{ThreadID: "answered", Reason: ReasonAnswered},
		{ThreadID: "verdict-last", Reason: ReasonAlreadyReplied},
		{ThreadID: "issue", Reason: ReasonNotReview},
	}
	if !reflect.DeepEqual(skipped, wantSkipped) {
		t.Errorf("skipped = %v, want %v", skipped, wantSkipped)
	}
	if len(deferred) != 0 {
		t.Errorf("deferred = %v, want none", deferred)
	}
	if got := OtherThreads(feed, "verdict-bot"); got != 1 {
		t.Errorf("other_threads = %d, want 1", got)
	}
	if p := pending[0]; p.RootCommentID != "r1" || p.WaitingCommentID != "w1" ||
		p.WaitingAuthor != "alice" || p.Path != "a.go" || !p.Outdated || p.FindingSig == nil ||
		*p.FindingSig != "abc" || p.Line == nil || *p.Line != 10 {
		t.Errorf("pending row lost a joined value: %+v", p)
	}
}

func TestSelectOrderingCapAndDeferred(t *testing.T) {
	t.Parallel()
	feed := &threadscore.File{Threads: []threadscore.Thread{
		thread("new", threadscore.KindReview, "verdict-bot", threadscore.AuthorVerdict, false, false,
			comment("rn", "verdict-bot", threadscore.AuthorVerdict, "2026-11-03T00:00:00Z"),
			comment("wn", "alice", threadscore.AuthorHuman, "2026-11-03T00:00:00Z")),
		thread("old", threadscore.KindReview, "verdict-bot", threadscore.AuthorVerdict, false, false,
			comment("ro", "verdict-bot", threadscore.AuthorVerdict, "2026-11-01T00:00:00Z"),
			comment("wo", "alice", threadscore.AuthorHuman, "2026-11-01T00:00:00Z")),
		thread("middle", threadscore.KindReview, "verdict-bot", threadscore.AuthorVerdict, false, false,
			comment("rm", "verdict-bot", threadscore.AuthorVerdict, "2026-11-02T00:00:00Z"),
			comment("wm", "alice", threadscore.AuthorHuman, "2026-11-02T00:00:00Z")),
	}}
	pending, skipped, deferred := Select(feed, "verdict-bot", nil, 2)
	if got, want := ids(pending), []string{"old", "middle"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pending = %v, want %v", got, want)
	}
	if got, want := deferred, []string{"new"}; !reflect.DeepEqual(got, want) {
		t.Errorf("deferred = %v, want %v", got, want)
	}
	wantSkipped := []Skip{{ThreadID: "new", Reason: ReasonDeferred}}
	if !reflect.DeepEqual(skipped, wantSkipped) {
		t.Errorf("skipped = %v, want %v", skipped, wantSkipped)
	}
}

func TestSelectLegacyLedgerCoversEarlierComments(t *testing.T) {
	t.Parallel()
	feed := &threadscore.File{Threads: []threadscore.Thread{
		thread("t1", threadscore.KindReview, "verdict-bot", threadscore.AuthorVerdict, false, false,
			comment("r1", "verdict-bot", threadscore.AuthorVerdict, "2026-11-01T00:00:00Z"),
			comment("w1", "alice", threadscore.AuthorHuman, "2026-11-02T00:00:00Z")),
		thread("t2", threadscore.KindReview, "verdict-bot", threadscore.AuthorVerdict, false, false,
			comment("r2", "verdict-bot", threadscore.AuthorVerdict, "2026-11-01T00:00:00Z"),
			comment("w2", "alice", threadscore.AuthorHuman, "2026-11-05T00:00:00Z")),
	}}
	ledger := []LedgerEntry{
		{Thread: "t1", ReplyID: "100", At: "2026-11-03T00:00:00Z"},
		{Thread: "t2", ReplyID: "101", At: "2026-11-03T00:00:00Z"},
	}
	pending, skipped, _ := Select(feed, "verdict-bot", ledger, 20)
	if got, want := ids(pending), []string{"t2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pending = %v, want %v", got, want)
	}
	wantSkipped := []Skip{{ThreadID: "t1", Reason: ReasonAnswered}}
	if !reflect.DeepEqual(skipped, wantSkipped) {
		t.Errorf("skipped = %v, want %v", skipped, wantSkipped)
	}
}

func ids(pending []Pending) []string {
	out := make([]string, 0, len(pending))
	for _, p := range pending {
		out = append(out, p.ThreadID)
	}
	return out
}
