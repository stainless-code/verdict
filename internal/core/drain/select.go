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
	ReasonResolved       = "resolved"
	ReasonAnswered       = "answered"
	ReasonBotReply       = "bot-reply"
	ReasonAlreadyReplied = "already-replied"
	ReasonNotReview      = "not-review-comment"
	ReasonDeferred       = "deferred"
	ReasonNoDraft        = "no-draft"
	ReplyBy              = "drain"
	DefaultMaxThreads    = 20
	MaxMaxThreads        = 100
)

// LedgerEntry is one recorded reply. Older address-reviews entries carry only
// thread, reply_id, and at: they answer every comment in the thread created at
// or before at. Drain entries name the waiting comment in in_reply_to.
type LedgerEntry struct { //nolint:govet // field order is the published property order
	Thread    string `json:"thread"`
	ReplyID   string `json:"reply_id"`
	At        string `json:"at"`
	InReplyTo string `json:"in_reply_to,omitempty"`
	By        string `json:"by,omitempty"`
}

// Pending is one thread whose last comment is a human question verdict has
// not answered, joined to the finding the root comment carries.
type Pending struct { //nolint:govet // field order is the published property order
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
func Select(feed *threadscore.File, self string, ledger []LedgerEntry, max int) ([]Pending, []Skip, []string) {
	if feed == nil {
		return []Pending{}, []Skip{}, []string{}
	}
	pending := make([]Pending, 0, len(feed.Threads))
	skipped := make([]Skip, 0, len(feed.Threads))
	deferred := []string{}
	for i := range feed.Threads {
		thread := &feed.Threads[i]
		if !fetchcore.SelfAuthored(thread, self) {
			continue
		}
		reason := skipReason(thread, ledger)
		if reason == "" {
			pending = append(pending, toPending(thread))
			continue
		}
		skipped = append(skipped, Skip{ThreadID: thread.ID, Reason: reason})
	}
	slices.SortFunc(pending, byWaitingAt)
	if max > 0 && len(pending) > max {
		for _, item := range pending[max:] {
			skipped = append(skipped, Skip{ThreadID: item.ThreadID, Reason: ReasonDeferred})
			deferred = append(deferred, item.ThreadID)
		}
		pending = pending[:max]
	}
	return pending, skipped, deferred
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

// skipReason returns the typed reason one verdict-authored thread is not
// pending, or empty when a draft is due.
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
	if answered(ledger, thread.ID, last) {
		return ReasonAnswered
	}
	return ""
}

// lastComment is the reply awaiting an answer. A thread with no replies has
// its verdict-authored root as its last comment, so it needs no draft.
func lastComment(thread *threadscore.Thread) threadscore.Comment {
	if len(thread.Comments) == 0 {
		return threadscore.Comment{Author: thread.Author, AuthorKind: thread.AuthorKind}
	}
	return thread.Comments[len(thread.Comments)-1]
}

// answered reports whether the ledger already covers one waiting comment. An
// explicit in_reply_to answers only that comment; a legacy entry answers every
// earlier comment in the thread.
func answered(ledger []LedgerEntry, threadID string, last threadscore.Comment) bool {
	for _, entry := range ledger {
		if entry.Thread != threadID || entry.ReplyID == "" {
			continue
		}
		if entry.InReplyTo != "" {
			if entry.InReplyTo == last.ID {
				return true
			}
			continue
		}
		if notAfter(last.CreatedAt, entry.At) {
			return true
		}
	}
	return false
}

// notAfter compares two host timestamps, falling back to their string order
// for hosts that publish no parseable time.
func notAfter(when, at string) bool {
	left, lerr := time.Parse(time.RFC3339, when)
	right, rerr := time.Parse(time.RFC3339, at)
	if lerr != nil || rerr != nil {
		return strings.TrimSpace(when) != "" && strings.Compare(when, at) <= 0
	}
	return !left.After(right)
}

// toPending joins one thread with its waiting reply and finding anchor.
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

// byWaitingAt orders the oldest question first, with the thread id breaking
// ties so one feed always yields the same plan.
func byWaitingAt(a, b Pending) int {
	if c := strings.Compare(a.WaitingAt, b.WaitingAt); c != 0 {
		return c
	}
	return strings.Compare(a.ThreadID, b.ThreadID)
}
