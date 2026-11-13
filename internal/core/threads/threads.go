// Package threadscore owns verdict's PR-conversation contract: the published
// threads.json document, one pure mapper per host, and the hidden marker that
// links a posted inline comment to its finding.
//
// The feed is local and schema'd (schemas/threads.schema.json): verdict reads
// only what it is handed, and every host's review conversation normalizes to
// the same shape instead of living in a hosted database. Mappers take decoded
// provider payloads and never spawn processes, so fetch (which already holds
// the raw pages) and the threads refresh verb share one normalization path.
package threadscore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	// SchemaVersion is the only feed version read.
	SchemaVersion = "1"
	// KeyThreadsJSON is the artifact name beside the raw provider files.
	KeyThreadsJSON = "threads.json"
	// KeyResolveReceipt is the receipt the resolve mutation writes beside it.
	KeyResolveReceipt = "threads-resolve.json"

	// KindIssue marks a conversation comment.
	KindIssue = "issue-comment"
	// KindReview marks a diff-anchored review thread.
	KindReview = "review-comment"

	// AuthorHuman marks a human author.
	AuthorHuman = "human"
	// AuthorBot marks a host-declared bot or a [bot]-suffixed login.
	AuthorBot = "bot"
	// AuthorVerdict marks a comment whose final line is verdict's marker.
	AuthorVerdict = "verdict"

	// SideLeft labels a deleted-side anchor.
	SideLeft = "LEFT"
	// SideRight labels an added or context anchor.
	SideRight = "RIGHT"
)

// MaxCommentBytes caps one comment body in the feed. The raw provider file
// keeps the full text; the feed stays small enough to hand an agent, and the
// per-comment truncated flag says the excerpt is not the whole comment.
const MaxCommentBytes = 8 * 1024

// File is one bundle's normalized thread feed: the published threads.json
// document, in schema key order.
type File struct { //nolint:govet // field order is the published property order
	SchemaVersion string   `const:"1"                                            json:"schema_version"`
	Provider      string   `enum:"github|gitlab|gitcafe|forgejo|bitbucket|azure" json:"provider"`
	PR            int      `json:"pr"`
	HeadSHA       string   `json:"head_sha"`
	FetchedAt     string   `format:"date-time"                                   json:"fetched_at"`
	Truncated     bool     `json:"truncated,omitempty"                           receipt:"true"`
	Threads       []Thread `json:"threads"`
}

// Thread is one root review thread or conversation comment plus its replies.
// ID is the host's thread identity (the key a resolution call takes);
// Comments[0].ID is the root comment id (the key a reply call takes).
type Thread struct { //nolint:govet // field order is the published property order
	ID         string    `json:"id"`
	Path       string    `json:"path"`
	Line       *int      `json:"line"`
	StartLine  *int      `json:"start_line"`
	Side       *string   `json:"side"`
	Resolved   bool      `json:"resolved"`
	Outdated   bool      `json:"outdated"`
	URL        *string   `json:"url"`
	Author     string    `json:"author"`
	AuthorKind string    `enum:"human|bot|verdict"            json:"author_kind"`
	FindingSig *string   `json:"finding_sig"`
	Kind       string    `enum:"issue-comment|review-comment" json:"kind"`
	Comments   []Comment `json:"comments"`
}

// Comment is one comment inside a thread, body capped and flagged.
type Comment struct { //nolint:govet // field order is the published property order
	ID         string `json:"id"`
	Author     string `json:"author"`
	AuthorKind string `enum:"human|bot|verdict"   json:"author_kind"`
	Body       string `json:"body"`
	Truncated  bool   `json:"truncated,omitempty" receipt:"true"`
	CreatedAt  string `json:"created_at"`
}

// Wire renders the feed in published key order, with empty lists as [].
func (f *File) Wire() *jq.Obj {
	threads := make([]any, 0, len(f.Threads))
	for i := range f.Threads {
		threads = append(threads, f.Threads[i].wire())
	}
	o := jq.NewObj(
		"schema_version", f.SchemaVersion,
		"provider", f.Provider,
		"pr", f.PR,
		"head_sha", f.HeadSHA,
		"fetched_at", f.FetchedAt,
		"threads", threads,
	)
	receipt := core.ScanCut(f.Truncated, len(f.Threads))
	if receipt == nil && feedBodyCut(f.Threads) {
		receipt = core.ResultCut("response_cap", len(f.Threads), nil, nil)
	}
	return receipt.Apply(o)
}

// wire renders one thread in published key order, with empty lists as [].
func (t *Thread) wire() *jq.Obj {
	comments := make([]any, 0, len(t.Comments))
	for i := range t.Comments {
		comments = append(comments, t.Comments[i].Wire())
	}
	var line, startLine, side, url, sig any
	if t.Line != nil {
		line = *t.Line
	}
	if t.StartLine != nil {
		startLine = *t.StartLine
	}
	if t.Side != nil {
		side = *t.Side
	}
	if t.URL != nil {
		url = *t.URL
	}
	if t.FindingSig != nil {
		sig = *t.FindingSig
	}
	return jq.NewObj(
		"id", t.ID,
		"path", t.Path,
		"line", line,
		"start_line", startLine,
		"side", side,
		"resolved", t.Resolved,
		"outdated", t.Outdated,
		"url", url,
		"author", t.Author,
		"author_kind", t.AuthorKind,
		"finding_sig", sig,
		"kind", t.Kind,
		"comments", comments,
	)
}

// Wire renders one comment in published key order.
func (c *Comment) Wire() *jq.Obj {
	o := jq.NewObj(
		"id", c.ID,
		"author", c.Author,
		"author_kind", c.AuthorKind,
		"body", c.Body,
		"created_at", c.CreatedAt,
	)
	var receipt *core.Truncation
	if c.Truncated {
		receipt = core.ResultCut("response_cap", len(c.Body), nil, nil)
	}
	return receipt.Apply(o)
}

// WriteFile writes the feed atomically to path (the bundle's threads.json).
func WriteFile(path string, f *File) error {
	if f.SchemaVersion == "" {
		f.SchemaVersion = SchemaVersion
	}
	if err := atomic.WriteFile(path, []byte(jq.PrettyOrdered(f.Wire()))); err != nil {
		return jq.Sealed("write "+path, err)
	}
	return nil
}

// ReadFile reads one bundle's threads.json through the capped, non-blocking
// reader, so a feed path swapped for a FIFO cannot wait for a writer. A
// missing, oversized, undecodable, or wrong-version document is an error,
// never an empty feed: a resolve run must not read a failed load as "nothing
// to close".
// ReadFile preserves [fs.ErrNotExist] for an absent feed.
func ReadFile(path string) (*File, error) {
	raw, err := readcommon.ReadRegularCapped(path, jq.MaxPRBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", path, err) //nolint:wrapcheck // preserves the documented missing-file inspection contract
	}

	if err != nil {
		return nil, jq.Sealed("read "+path, err)
	}
	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, jq.Sealed("decode "+path, err)
	}
	if f.SchemaVersion != SchemaVersion {
		return nil, jq.Sealed("threads.json schema", fmt.Errorf(
			"schema_version %q, want %q", f.SchemaVersion, SchemaVersion))
	}
	return &f, nil
}

// ResolveReceipt is the receipt `threads resolve` writes: the host, head, and
// timestamp of the mutation, the ids it closed, and the candidates it skipped
// with a machine-readable reason.
type ResolveReceipt struct { //nolint:govet // field order is the published property order
	SchemaVersion string        `const:"1"                                            json:"schema_version"`
	Provider      string        `enum:"github|gitlab|gitcafe|forgejo|bitbucket|azure" json:"provider"`
	Repo          string        `json:"repo"`
	PR            int           `json:"pr"`
	HeadSHA       string        `json:"head_sha"`
	ResolvedAt    string        `format:"date-time"                                   json:"resolved_at"`
	DryRun        bool          `json:"dry_run"`
	Resolved      []string      `json:"resolved"`
	Skipped       []ResolveSkip `json:"skipped"`
}

// ResolveSkip is one candidate the run did not write, with the reason it was
// left open.
type ResolveSkip struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// Wire renders one receipt in published key order, with empty lists as [].
func (r *ResolveReceipt) Wire() *jq.Obj {
	resolved := make([]any, 0, len(r.Resolved))
	for _, id := range r.Resolved {
		resolved = append(resolved, id)
	}
	skipped := make([]any, 0, len(r.Skipped))
	for _, skip := range r.Skipped {
		skipped = append(skipped, jq.NewObj("id", skip.ID, "reason", skip.Reason))
	}
	return jq.NewObj(
		"schema_version", r.SchemaVersion,
		"provider", r.Provider,
		"repo", r.Repo,
		"pr", r.PR,
		"head_sha", r.HeadSHA,
		"resolved_at", r.ResolvedAt,
		"dry_run", r.DryRun,
		"resolved", resolved,
		"skipped", skipped,
	)
}

// WriteResolveReceipt writes one resolve receipt atomically.
func WriteResolveReceipt(path string, r *ResolveReceipt) error {
	if r.SchemaVersion == "" {
		r.SchemaVersion = SchemaVersion
	}
	if err := atomic.WriteFile(path, []byte(jq.PrettyOrdered(r.Wire()))); err != nil {
		return jq.Sealed("write "+path, err)
	}
	return nil
}

// CountFile reads the thread count from a feed, 0 when the file is missing or
// unreadable. Callers use it for advisory hints only (never for a gate).
func CountFile(path string) int {
	raw, err := jq.CappedFile(path, jq.MaxPRBytes)
	if err != nil {
		return 0
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return 0
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return 0
	}
	return len(jq.Elements(m["threads"]))
}

// PRNumber parses a PR positional for the feed (0 when it is not numeric).
func PRNumber(pr string) int {
	n, err := strconv.Atoi(pr)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// Hidden finding marker: the final line of every inline comment verdict
// posts. It is an HTML comment, so GitHub renders nothing, and it stays
// backward compatible with comments posted before it existed (no marker
// reads as no link).
const findingMarkerFormat = "<!-- verdict-finding sig=%s -->"

// findingMarkerRe matches only a body's final line. A marker quoted in the
// middle of a human or another bot's comment never links.
var findingMarkerRe = regexp.MustCompile(`^<!-- verdict-finding sig=([0-9a-f]{40}) -->$`)

// reviewMarkerRe matches the post verb's hidden review marker (the body's
// final line), which identifies a verdict-authored review summary.
var reviewMarkerRe = regexp.MustCompile(`^<!-- verdict-review payload=[0-9a-f]{40} head=[0-9a-f]{0,40} -->$`)

// FindingMarker renders the hidden final line carrying a finding sig.
func FindingMarker(sig string) string {
	return fmt.Sprintf(findingMarkerFormat, sig)
}

// FindingSig reads the hidden finding marker from a body's final line.
func FindingSig(body string) (string, bool) {
	m := findingMarkerRe.FindStringSubmatch(finalLine(body))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// IsVerdictBody reports whether a body carries a verdict marker as its final
// line: a finding marker on an inline comment or the review marker on a
// summary. Used only for the advisory author_kind label.
func IsVerdictBody(body string) bool {
	last := finalLine(body)
	return findingMarkerRe.MatchString(last) || reviewMarkerRe.MatchString(last)
}

// AuthorKind classifies one comment author: verdict when its own marker is the
// final line, bot when the host says so (or the login carries GitHub's [bot]
// suffix), else human.
func AuthorKind(login string, hostBot bool, body string) string {
	switch {
	case IsVerdictBody(body):
		return AuthorVerdict
	case hostBot || strings.HasSuffix(login, "[bot]"):
		return AuthorBot
	default:
		return AuthorHuman
	}
}

func finalLine(body string) string {
	trimmed := strings.TrimRight(body, " \t\r\n")
	if _, after, found := strings.CutLast(trimmed, "\n"); found {
		return after
	}
	return trimmed
}

// CapBody caps one comment body at MaxCommentBytes, never splitting a UTF-8
// rune, and reports whether the excerpt is truncated.
func CapBody(body string) (string, bool) {
	if len(body) <= MaxCommentBytes {
		return body, false
	}
	cut := body[:MaxCommentBytes]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return strings.TrimRight(cut, "\r\n"), true
}

func feedBodyCut(threads []Thread) bool {
	for i := range threads {
		for _, comment := range threads[i].Comments {
			if comment.Truncated {
				return true
			}
		}
	}
	return false
}
