package drain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
)

// Disposition is one action the agent may take for a pending thread.
type Disposition string

const (
	// DispositionAnswer posts the draft body.
	DispositionAnswer Disposition = "answer"
	// DispositionWithdraw posts the body and resolves the finding.
	DispositionWithdraw Disposition = "withdraw"
	// DispositionDefer records a decision that still belongs to a human.
	DispositionDefer Disposition = "defer"
	// MaxBodyRunes caps one reply body. The limit is characters, not bytes,
	// so a multi-byte language does not lose half a sentence.
	MaxBodyRunes = 4000
	// markerPrefix is verdict's own comment marker, which a draft may never carry.
	markerPrefix = "<!-- verdict-"
)

// Reply is one draft answer to a waiting comment.
type Reply struct { // field order is the published property order
	ThreadID         string      `json:"thread_id"`
	WaitingCommentID string      `json:"waiting_comment_id"`
	Disposition      Disposition `enum:"answer|withdraw|defer" json:"disposition"`
	Body             string      `json:"body"                  maxLength:"4000"`
}

// RepliesFile is the single artifact the drafting agent writes.
type RepliesFile struct { // field order is the published property order
	SchemaVersion string  `const:"1"          json:"schema_version"`
	PlanDigest    string  `json:"plan_digest"`
	Replies       []Reply `json:"replies"`
}

// CheckDrafts validates one raw drafts document against a plan without any
// I/O. The site in each refusal names the exact field an agent must fix.
func CheckDrafts(raw []byte, plan *File) (*Drafts, []Skip, *core.RefusalError) {
	if plan == nil {
		return nil, nil, invalid("plan", "no plan to draft against")
	}
	doc, root, ref := parseDrafts(raw, plan)
	if ref != nil {
		return nil, nil, ref
	}
	pending := make(map[string]Pending, len(plan.Pending))
	for _, p := range plan.Pending {
		pending[p.ThreadID] = p
	}
	summary := &Drafts{File: KeyDraftsJSON}
	seen, ref := checkReplies(doc.Replies, root["replies"], pending, summary)
	if ref != nil {
		return nil, nil, ref
	}
	summary.Count = len(doc.Replies)
	return summary, noDrafts(plan.Pending, seen), nil
}

// checkReplies validates every entry once and returns the threads covered.
func checkReplies(replies []Reply, raw json.RawMessage, pending map[string]Pending, summary *Drafts) (map[string]bool, *core.RefusalError) {
	seen := make(map[string]bool, len(replies))
	for i, reply := range replies {
		site := fmt.Sprintf("replies[%d]", i)
		if ref := validateFields(raw, i, site); ref != nil {
			return nil, ref
		}
		if ref := checkReply(reply, pending, seen, site, summary); ref != nil {
			return nil, ref
		}
	}
	return seen, nil
}

// noDrafts lists pending threads the agent left unanswered.
func noDrafts(pending []Pending, seen map[string]bool) []Skip {
	skipped := []Skip{}
	for _, p := range pending {
		if !seen[p.ThreadID] {
			skipped = append(skipped, Skip{ThreadID: p.ThreadID, Reason: ReasonNoDraft})
		}
	}
	return skipped
}

// parseDrafts checks the envelope before any reply is read.
func parseDrafts(raw []byte, plan *File) (*RepliesFile, map[string]json.RawMessage, *core.RefusalError) {
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
	return &doc, root, nil
}

// checkReply validates one draft against its pending row and counts it.
func checkReply(reply Reply, pending map[string]Pending, seen map[string]bool, site string, summary *Drafts) *core.RefusalError {
	if seen[reply.ThreadID] {
		return invalid(site+".thread_id", "duplicate thread "+reply.ThreadID)
	}
	seen[reply.ThreadID] = true
	p, ok := pending[reply.ThreadID]
	if !ok {
		return invalid(site+".thread_id", "thread "+reply.ThreadID+" is not pending in the plan")
	}
	if reply.WaitingCommentID != p.WaitingCommentID {
		return invalid(site+".waiting_comment_id", "got "+reply.WaitingCommentID+", want "+p.WaitingCommentID)
	}
	if ref := checkBody(reply, site); ref != nil {
		return ref
	}
	return countDisposition(reply.Disposition, summary, site)
}

// checkBody refuses verdict markers and caps every disposition's body. Only
// answer and withdraw need non-empty text: defer records the decision itself.
func checkBody(reply Reply, site string) *core.RefusalError {
	if strings.Contains(reply.Body, markerPrefix) {
		return invalid(site+".body", "a draft must not carry verdict's marker")
	}
	if reply.Disposition == DispositionDefer {
		return nil
	}
	if reply.Body == "" {
		return invalid(site+".body", "answer and withdraw need a non-empty body")
	}
	if n := utf8.RuneCountInString(reply.Body); n > MaxBodyRunes {
		return invalid(site+".body", fmt.Sprintf("body is %d characters, max %d", n, MaxBodyRunes))
	}
	return nil
}

// countDisposition is exhaustive over the published enum, so adding a
// disposition fails compilation instead of silently dropping a draft.
func countDisposition(d Disposition, summary *Drafts, site string) *core.RefusalError {
	switch d {
	case DispositionAnswer:
		summary.Dispositions.Answer++
	case DispositionWithdraw:
		summary.Dispositions.Withdraw++
	case DispositionDefer:
		summary.Dispositions.Defer++
	default:
		return invalid(site+".disposition", fmt.Sprintf("unknown disposition %q", d))
	}
	return nil
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
		return fmt.Errorf("decode JSON: %w", err) //nolint:wrapcheck // decoded here from the capped local artifact
	}
	if dec.More() {
		return errors.New("more than one JSON value") //nolint:wrapcheck // local JSON shape rejection
	}
	return nil
}

// invalid names the site under drafts-invalid, so an agent can edit one field.
func invalid(site, reason string) *core.RefusalError {
	return &core.RefusalError{Code: "drafts-invalid", Message: "drain.replies.json " + site + ": " + reason}
}
