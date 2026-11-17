package drain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
)

// Disposition is one action the agent may take for a pending thread.
type Disposition string

const (
	DispositionAnswer   Disposition = "answer"
	DispositionWithdraw Disposition = "withdraw"
	DispositionDefer    Disposition = "defer"
	// MaxBodyRunes caps one reply body. The limit is characters, not bytes,
	// so a multi-byte language does not lose half a sentence.
	MaxBodyRunes = 4000
)

// Reply is one draft answer to a waiting comment.
type Reply struct { //nolint:govet // field order is the published property order
	ThreadID         string      `json:"thread_id"`
	WaitingCommentID string      `json:"waiting_comment_id"`
	Disposition      Disposition `enum:"answer|withdraw|defer" json:"disposition"`
	Body             string      `maxLength:"4000"             json:"body"`
}

// RepliesFile is the single artifact the drafting agent writes.
type RepliesFile struct { //nolint:govet // field order is the published property order
	SchemaVersion string  `const:"1" json:"schema_version"`
	PlanDigest    string  `json:"plan_digest"`
	Replies       []Reply `json:"replies"`
}

// CheckDrafts validates one raw drafts document against a plan without any
// I/O. The site in each refusal names the exact field an agent must fix.
func CheckDrafts(raw []byte, plan *File) (*Drafts, []Skip, *core.RefusalError) {
	if plan == nil {
		return nil, nil, invalid("plan", "no plan to draft against")
	}
	var root map[string]json.RawMessage
	if err := decodeStrict(raw, &root); err != nil {
		return nil, nil, invalid("root", err.Error())
	}
	for key := range root {
		if !slices.Contains([]string{"schema_version", "plan_digest", "replies"}, key) {
			return nil, nil, invalid("root."+key, "unknown key")
		}
	}
	var doc RepliesFile
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, nil, invalid("root", err.Error())
	}
	if doc.SchemaVersion != SchemaVersion {
		return nil, nil, invalid("schema_version", fmt.Sprintf("got %q, want %q", doc.SchemaVersion, SchemaVersion))
	}
	if doc.PlanDigest != plan.PlanDigest {
		return nil, nil, &core.RefusalError{Code: "drafts-stale", Message: fmt.Sprintf(
			"drain.replies.json plan_digest %q does not match the plan's %q; re-run drain for a fresh plan",
			doc.PlanDigest, plan.PlanDigest)}
	}
	pending := make(map[string]Pending, len(plan.Pending))
	for _, p := range plan.Pending {
		pending[p.ThreadID] = p
	}
	summary := &Drafts{File: KeyDraftsJSON}
	seen := make(map[string]bool, len(doc.Replies))
	skipped := []Skip{}
	for i, reply := range doc.Replies {
		site := fmt.Sprintf("replies[%d]", i)
		if err := validateFields(root["replies"], i, site); err != nil {
			return nil, nil, err
		}
		if seen[reply.ThreadID] {
			return nil, nil, invalid(site+".thread_id", "duplicate thread "+reply.ThreadID)
		}
		seen[reply.ThreadID] = true
		p, ok := pending[reply.ThreadID]
		if !ok {
			return nil, nil, invalid(site+".thread_id", "thread "+reply.ThreadID+" is not pending in the plan")
		}
		if reply.WaitingCommentID != p.WaitingCommentID {
			return nil, nil, invalid(site+".waiting_comment_id", "got "+reply.WaitingCommentID+
				", want "+p.WaitingCommentID)
		}
		switch reply.Disposition {
		case DispositionAnswer, DispositionWithdraw, DispositionDefer:
		default:
			return nil, nil, invalid(site+".disposition", fmt.Sprintf("unknown disposition %q", reply.Disposition))
		}
		if strings.Contains(reply.Body, "<!-- verdict-") {
			return nil, nil, invalid(site+".body", "a draft must not carry verdict's marker")
		}
		if reply.Disposition == DispositionDefer {
			summary.Dispositions.Defer++
			continue
		}
		if reply.Body == "" {
			return nil, nil, invalid(site+".body", "answer and withdraw need a non-empty body")
		}
		if n := utf8.RuneCountInString(reply.Body); n > MaxBodyRunes {
			return nil, nil, invalid(site+".body", fmt.Sprintf("body is %d characters, max %d", n, MaxBodyRunes))
		}
		switch reply.Disposition {
		case DispositionAnswer:
			summary.Dispositions.Answer++
		case DispositionWithdraw:
			summary.Dispositions.Withdraw++
		}
	}
	summary.Count = len(doc.Replies)
	for _, p := range plan.Pending {
		if !seen[p.ThreadID] {
			skipped = append(skipped, Skip{ThreadID: p.ThreadID, Reason: ReasonNoDraft})
		}
	}
	return summary, skipped, nil
}

// validateFields rejects unknown keys in one draft entry, naming the field.
func validateFields(raw json.RawMessage, index int, site string) *core.RefusalError {
	var replies []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &replies); err != nil {
		return invalid(site, err.Error())
	}
	if index >= len(replies) {
		return invalid(site, "missing entry")
	}
	for key := range replies[index] {
		if !slices.Contains([]string{"thread_id", "waiting_comment_id", "disposition", "body"}, key) {
			return invalid(site+"."+key, "unknown key")
		}
	}
	return nil
}

// decodeStrict decodes one capped JSON document with unknown keys refused.
func decodeStrict(raw []byte, into any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("more than one JSON value")
	}
	return nil
}

// invalid names the site under drafts-invalid, so an agent can edit one field.
func invalid(site, reason string) *core.RefusalError {
	return &core.RefusalError{Code: "drafts-invalid", Message: "drain.replies.json " + site + ": " + reason}
}
