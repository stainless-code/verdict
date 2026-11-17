package drain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	threadscore "github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	// SchemaVersion is the only drain JSON version published.
	SchemaVersion = "1"
	// KeyPlanJSON is the plan and receipt written into the bundle.
	KeyPlanJSON = "drain.json"
	// KeyDraftsJSON is the single artifact the drafting agent writes.
	KeyDraftsJSON = "drain.replies.json"
	// KeyLedgerJSON is the older replies ledger drain joins.
	KeyLedgerJSON = "replies.json"
	// CodeDrainFailed refuses a plan that cannot be built.
	CodeDrainFailed = "drain-failed"
	// CodeDraftsInvalid refuses a draft with a named bad site.
	CodeDraftsInvalid = "drafts-invalid"
	// CodeDraftsStale refuses a draft written against another plan.
	CodeDraftsStale = "drafts-stale"
	keyThreadID     = "thread_id"
)

// Dispositions counts the draft verdicts the agent chose.
type Dispositions struct {
	Answer   int `json:"answer"`
	Withdraw int `json:"withdraw"`
	Defer    int `json:"defer"`
}

// Drafts is the validated draft summary recorded in the plan.
type Drafts struct {
	File         string       `json:"file"`
	Count        int          `json:"count"`
	Dispositions Dispositions `json:"dispositions"`
}

// Posted is one successful host write (PR B records these).
type Posted struct {
	ThreadID    string `json:"thread_id"`
	ReplyID     string `json:"reply_id"`
	Disposition string `json:"disposition"`
	Resolved    bool   `json:"resolved"`
}

// Failed is one host write that did not land (PR B records these).
type Failed struct {
	ThreadID string `json:"thread_id"`
	Reason   string `json:"reason"`
}

// File is one drain plan and, after posting, its receipt.
type File struct { //nolint:govet // field order is the published JSON contract
	SchemaVersion string    `const:"1"                                            json:"schema_version"`
	Provider      string    `enum:"github|gitlab|gitcafe|forgejo|bitbucket|azure" json:"provider"`
	PR            int       `json:"pr"`
	HeadSHA       string    `json:"head_sha"`
	PlannedAt     string    `format:"date-time"                                   json:"planned_at"`
	Self          string    `json:"self"`
	PlanDigest    string    `json:"plan_digest"`
	Pending       []Pending `json:"pending"`
	Skipped       []Skip    `json:"skipped"`
	OtherThreads  int       `json:"other_threads"`
	Drafts        *Drafts   `anyOf:"null"                                         json:"drafts"`
	Posted        []Posted  `json:"posted"`
	Failed        []Failed  `json:"failed"`
	PostedAt      *string   `format:"date-time"                                   json:"posted_at"`
}

// Wire renders the plan in published key order, with empty lists as [].
func (f *File) Wire() *jq.Obj {
	pending := make([]any, 0, len(f.Pending))
	for i := range f.Pending {
		pending = append(pending, toObj(&f.Pending[i]))
	}
	skipped := make([]any, 0, len(f.Skipped))
	for _, s := range f.Skipped {
		skipped = append(skipped, jq.NewObj(keyThreadID, s.ThreadID, "reason", s.Reason))
	}
	posted := make([]any, 0, len(f.Posted))
	for _, p := range f.Posted {
		posted = append(posted, jq.NewObj(keyThreadID, p.ThreadID, "reply_id", p.ReplyID,
			"disposition", p.Disposition, "resolved", p.Resolved))
	}
	failed := make([]any, 0, len(f.Failed))
	for _, x := range f.Failed {
		failed = append(failed, jq.NewObj(keyThreadID, x.ThreadID, "reason", x.Reason))
	}
	var drafts any
	if f.Drafts != nil {
		drafts = jq.NewObj("file", f.Drafts.File, "count", f.Drafts.Count, "dispositions",
			jq.NewObj("answer", f.Drafts.Dispositions.Answer, "withdraw", f.Drafts.Dispositions.Withdraw,
				"defer", f.Drafts.Dispositions.Defer))
	}
	var postedAt any
	if f.PostedAt != nil {
		postedAt = *f.PostedAt
	}
	return jq.NewObj(
		"schema_version", f.SchemaVersion,
		"provider", f.Provider,
		"pr", f.PR,
		"head_sha", f.HeadSHA,
		"planned_at", f.PlannedAt,
		"self", f.Self,
		"plan_digest", f.PlanDigest,
		"pending", pending,
		"skipped", skipped,
		"other_threads", f.OtherThreads,
		"drafts", drafts,
		"posted", posted,
		"failed", failed,
		"posted_at", postedAt,
	)
}

func toObj(p *Pending) *jq.Obj {
	var line, finding any
	if p.Line != nil {
		line = *p.Line
	}
	if p.FindingSig != nil {
		finding = *p.FindingSig
	}
	return jq.NewObj(
		keyThreadID, p.ThreadID,
		"root_comment_id", p.RootCommentID,
		"waiting_comment_id", p.WaitingCommentID,
		"waiting_author", p.WaitingAuthor,
		"waiting_at", p.WaitingAt,
		"path", p.Path,
		"line", line,
		"outdated", p.Outdated,
		"finding_sig", finding,
	)
}

// NewPlan selects the work for one run and pins it with a digest.
func NewPlan(provider string, pr int, head, at, self string, feed *threadscore.File, ledger []LedgerEntry, limit int) (*File, []string) {
	pending, skipped, deferred := Select(feed, self, ledger, limit)
	return &File{
		SchemaVersion: SchemaVersion, Provider: provider, PR: pr, HeadSHA: head,
		PlannedAt: at, Self: self, PlanDigest: Digest(pending),
		Pending: pending, Skipped: skipped, OtherThreads: OtherThreads(feed, self),
		Posted: []Posted{}, Failed: []Failed{},
	}, deferred
}

// Digest pins the canonical pending list: one thread id and waiting comment
// id per line, in plan order. A draft written against a different selection
// cannot match it.
func Digest(pending []Pending) string {
	var sb strings.Builder
	for _, p := range pending {
		fmt.Fprintf(&sb, "%s\n%s\n", p.ThreadID, p.WaitingCommentID)
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

// WritePlan writes one plan atomically.
func WritePlan(path string, f *File) error {
	if f.SchemaVersion == "" {
		f.SchemaVersion = SchemaVersion
	}
	return atomic.WriteFile(path, []byte(jq.PrettyOrdered(f.Wire())))
}

// ReadPlan reads the plan a draft was written against.
func ReadPlan(path string) (*File, error) {
	raw, err := jq.CappedFile(path, jq.MaxPRBytes)
	if err != nil {
		return nil, jq.NewErrf("read plan: %v", err)
	}
	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, jq.NewErrf("decode plan: %v", err)
	}
	if f.SchemaVersion != SchemaVersion {
		return nil, jq.NewErrf("plan schema_version %q, want %q", f.SchemaVersion, SchemaVersion)
	}
	return &f, nil
}
