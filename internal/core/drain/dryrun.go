package drain

import (
	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/jq"
)

// KeyDryRunJSON is the preview file drain --post --dry-run writes beside the
// plan. It never stands in for drain.json's receipt.
const KeyDryRunJSON = "drain.dryrun.json"

// Planned is one reply a dry run would send, with the final marked body.
type Planned struct {
	ThreadID      string `json:"thread_id"`
	RootCommentID string `json:"root_comment_id"`
	Disposition   string `json:"disposition"`
	Body          string `json:"body"`
}

// DryRunFile is the preview receipt: the replies a post run would write and
// the threads it would skip, with no reply ids or host result.
type DryRunFile struct {
	SchemaVersion string    `json:"schema_version"`
	Provider      string    `enum:"github|gitlab|gitcafe|forgejo|bitbucket|azure" json:"provider"`
	HeadSHA       string    `json:"head_sha"`
	PlannedAt     string    `format:"date-time"                                   json:"planned_at"`
	Planned       []Planned `json:"planned"`
	Skipped       []Skip    `json:"skipped"`
	PR            int       `json:"pr"`
}

// Wire renders the preview in published key order, with empty lists as [].
func (f *DryRunFile) Wire() *jq.Obj {
	planned := make([]any, 0, len(f.Planned))
	for _, p := range f.Planned {
		planned = append(planned, jq.NewObj(keyThreadID, p.ThreadID, "root_comment_id", p.RootCommentID,
			"disposition", p.Disposition, "body", p.Body))
	}
	skipped := make([]any, 0, len(f.Skipped))
	for _, s := range f.Skipped {
		skipped = append(skipped, jq.NewObj(keyThreadID, s.ThreadID, "reason", s.Reason))
	}
	return jq.NewObj(
		"schema_version", f.SchemaVersion,
		"provider", f.Provider,
		"pr", f.PR,
		"head_sha", f.HeadSHA,
		"planned_at", f.PlannedAt,
		"planned", planned,
		"skipped", skipped,
	)
}

// WriteDryRun writes one preview atomically.
func WriteDryRun(path string, f *DryRunFile) error {
	if f.SchemaVersion == "" {
		f.SchemaVersion = SchemaVersion
	}
	return atomic.WriteFile(path, []byte(jq.PrettyOrdered(f.Wire())))
}
