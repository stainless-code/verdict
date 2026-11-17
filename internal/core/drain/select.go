// Package drain selects review threads that are waiting on a verdict reply.
// Selection is pure: the feed, the authenticated login, and the reply ledger
// are inputs, and the caller owns every write.
package drain

import (
	"slices"
	"strings"
	"time"

	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	threadscore "github.com/stainless-code/verdict/internal/core/threads"
)

const (
	// ReasonResolved is a thread the host already closed.
	ReasonResolved = "resolved"
	// ReasonAnswered is a thread the recorded ledger already answers.
	ReasonAnswered = "answered"
	// ReasonBotReply is a thread whose last reply came from a bot.
	ReasonBotReply = "bot-reply"
	// ReasonAlreadyReplied is a thread verdict has already answered.
	ReasonAlreadyReplied = "already-replied"
	// ReasonNotReview is a self-authored thread that is not a review comment.
	ReasonNotReview = "not-review-comment"
	// ReasonDeferred is a pending thread past the operator's cap.
	ReasonDeferred = "deferred"
	// ReasonNoDraft is a pending thread the agent did not write a draft for.
	ReasonNoDraft = "no-draft"
	// ReplyBy is the author recorded on a drain ledger entry.
	ReplyBy = "drain"
	// DefaultMaxThreads is the cap used when the operator gives none.
	DefaultMaxThreads = 20
	// MaxMaxThreads is the largest cap the verb accepts.
	MaxMaxThreads = 100
)

// LedgerEntry is one recorded reply. Older address-reviews entries carry only
// thread, reply_id, and at: they answer every comment in the thread created at
// or before at. Drain entries name the waiting comment in in_reply_to.
type LedgerEntry struct {
	Thread    string `json:"thread"`
	ReplyID   string `json:"reply_id"`
	At        string `json:"at"`
	InReplyTo string `json:"in_reply_to,omitempty"`
	By        string `json:"by,omitempty"`
}

// Pending is one thread whose last comment is a human question verdict has
// not answered, joined to the finding the root comment carries.
type Pending struct { //nolint:govet // field order is the published JSON contract
	ThreadID         string  `json:"thread_id"`
	RootCommentID    string  `json:"root_comment_id"`
	WaitingCommentID string  `json:"waiting_comment_id"`
	WaitingAuthor    string  `json:"waiting_author"`
	WaitingAt        string  `json:"waiting_at"`
	Path             string  `json:"path"`
	Line             *int    `json:"line"`
	Outdated         bool    `json:"outdated"`
	FindingSig       *string `json:"finding_sig"`
}

// Skip is one verdict-authored thread the run did not draft, with the reason
// an operator can check against the feed.
type Skip struct {
	ThreadID string `json:"thread_id"`
	Reason   string `json:"reason"`
}

// Select applies the drain waiting rules to one refreshed feed. Pending is
// oldest waiting comment first; beyond max, threads are deferred. Threads
// verdict did not author are excluded from all three results.
func Select(feed *threadscore.File, self string, ledger []LedgerEntry, limit int) ([]Pending, []Skip, []string) {
	pending, skipped := classify(feed, self, ledger)
	slices.SortFunc(pending, func(a, b Pending) int { return byWaitingAt(&a, &b) })
	deferred := []string{}
	if limit > 0 && len(pending) > limit {
		for _, item := range pending[limit:] {
			skipped = append(skipped, Skip{ThreadID: item.ThreadID, Reason: ReasonDeferred})
			deferred = append(deferred, item.ThreadID)
		}
		pending = pending[:limit]
	}
	return pending, skipped, deferred
}

func classify(feed *threadscore.File, self string, ledger []LedgerEntry) ([]Pending, []Skip) {
	if feed == nil {
		return []Pending{}, []Skip{}
	}
	pending := make([]Pending, 0, len(feed.Threads))
	skipped := make([]Skip, 0, len(feed.Threads))
	for i := range feed.Threads {
		thread := &feed.Threads[i]
		if !fetchcore.SelfAuthored(thread, self) {
			continue
		}
		reason := skipReason(thread, ledger)
		if reason != "" {
			skipped = append(skipped, Skip{ThreadID: thread.ID, Reason: reason})
			continue
		}
		pending = append(pending, toPending(thread))
	}
	return pending, skipped
}

// OtherThreads counts threads verdict did not author. They are never listed
// as skips: a reply to someone else's thread is not verdict's to make.
func OtherThreads(feed *threadscore.File, self string) int {
	if feed == nil {
		return 0
	}
	n := 0
	for i := range feed.Threads {
		if !fetchcore.SelfAuthored(&feed.Threads[i], self) {
			n++
		}
	}
	return n
}

func skipReason(thread *threadscore.Thread, ledger []LedgerEntry) string {
	if thread.Kind != threadscore.KindReview {
		return ReasonNotReview
	}
	if thread.Resolved {
		return ReasonResolved
	}
	last := lastComment(thread)
	switch last.AuthorKind {
	case threadscore.AuthorBot:
		return ReasonBotReply
	case threadscore.AuthorVerdict:
		return ReasonAlreadyReplied
	}
	if answered(ledger, thread.ID, &last) {
		return ReasonAnswered
	}
	return ""
}

func lastComment(thread *threadscore.Thread) threadscore.Comment {
	if len(thread.Comments) == 0 {
		return threadscore.Comment{Author: thread.Author, AuthorKind: thread.AuthorKind}
	}
	return thread.Comments[len(thread.Comments)-1]
}

func answered(ledger []LedgerEntry, threadID string, last *threadscore.Comment) bool {
	for i := range ledger {
		entry := &ledger[i]
		if entry.Thread == threadID && entry.ReplyID != "" && entryAnswers(entry, last) {
			return true
		}
	}
	return false
}

func entryAnswers(entry *LedgerEntry, last *threadscore.Comment) bool {
	if entry.InReplyTo != "" {
		return entry.InReplyTo == last.ID
	}
	return notAfter(last.CreatedAt, entry.At)
}

func notAfter(when, at string) bool {
	left, lerr := time.Parse(time.RFC3339, when)
	right, rerr := time.Parse(time.RFC3339, at)
	if lerr != nil || rerr != nil {
		return strings.TrimSpace(when) != "" && strings.Compare(when, at) <= 0
	}
	return !left.After(right)
}

func toPending(thread *threadscore.Thread) Pending {
	last := lastComment(thread)
	root := ""
	if len(thread.Comments) > 0 {
		root = thread.Comments[0].ID
	}
	return Pending{
		ThreadID:         thread.ID,
		RootCommentID:    root,
		WaitingCommentID: last.ID,
		WaitingAuthor:    last.Author,
		WaitingAt:        last.CreatedAt,
		Path:             thread.Path,
		Line:             thread.Line,
		Outdated:         thread.Outdated,
		FindingSig:       thread.FindingSig,
	}
}

func byWaitingAt(a, b *Pending) int {
	left, leftErr := time.Parse(time.RFC3339, a.WaitingAt)
	right, rightErr := time.Parse(time.RFC3339, b.WaitingAt)
	if leftErr == nil && rightErr == nil {
		if left.Before(right) {
			return -1
		}
		if left.After(right) {
			return 1
		}
	} else if c := strings.Compare(a.WaitingAt, b.WaitingAt); c != 0 {
		return c
	}
	return strings.Compare(a.ThreadID, b.ThreadID)
}
