package drain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
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
	markerPrefix = "<!-- verdict-"
	keyReplies   = "replies"
)

// Reply is one draft answer to a waiting comment.
type Reply struct {
	ThreadID         string      `json:"thread_id"`
	WaitingCommentID string      `json:"waiting_comment_id"`
	Disposition      Disposition `enum:"answer|withdraw|defer" json:"disposition"`
	Body             string      `json:"body"                  maxLength:"4000"`
}

// RepliesFile is the single artifact the drafting agent writes.
type RepliesFile struct {
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
	seen, ref := checkReplies(doc.Replies, root[keyReplies], pending, summary)
	if ref != nil {
		return nil, nil, ref
	}
	summary.Count = len(doc.Replies)
	return summary, noDrafts(plan.Pending, seen), nil
}

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

func noDrafts(pending []Pending, seen map[string]bool) []Skip {
	skipped := []Skip{}
	for _, p := range pending {
		if !seen[p.ThreadID] {
			skipped = append(skipped, Skip{ThreadID: p.ThreadID, Reason: ReasonNoDraft})
		}
	}
	return skipped
}

func parseDrafts(raw []byte, plan *File) (*RepliesFile, map[string]json.RawMessage, *core.RefusalError) {
	var root map[string]json.RawMessage
	if err := decodeStrict(raw, &root); err != nil {
		return nil, nil, invalid("root", err.Error())
	}
	if ref := checkRootKeys(root); ref != nil {
		return nil, nil, ref
	}
	replies, ref := parseReplies(root)
	if ref != nil {
		return nil, nil, ref
	}
	var doc RepliesFile
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, nil, invalid("root", err.Error())
	}
	doc.Replies = replies
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

func checkRootKeys(root map[string]json.RawMessage) *core.RefusalError {
	for key := range root {
		if !slices.Contains([]string{"schema_version", "plan_digest", keyReplies}, key) {
			return invalid("root."+key, "unknown key")
		}
	}
	return nil
}

func parseReplies(root map[string]json.RawMessage) ([]Reply, *core.RefusalError) {
	raw, ok := root[keyReplies]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, invalid(keyReplies, "must be an array")
	}
	var replies []Reply
	if err := json.Unmarshal(raw, &replies); err != nil {
		return nil, invalid(keyReplies, err.Error())
	}
	return replies, nil
}

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

func checkBody(reply Reply, site string) *core.RefusalError {
	if strings.Contains(reply.Body, markerPrefix) {
		return invalid(site+".body", "a draft must not carry verdict's marker")
	}
	if n := utf8.RuneCountInString(reply.Body); n > MaxBodyRunes {
		return invalid(site+".body", fmt.Sprintf("body is %d characters, max %d", n, MaxBodyRunes))
	}
	if reply.Disposition != DispositionDefer && strings.TrimSpace(reply.Body) == "" {
		return invalid(site+".body", "answer and withdraw need a non-empty body")
	}
	return nil
}

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

func decodeStrict(raw []byte, into any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return jq.NewErrf("decode JSON: %v", err)
	}
	if dec.More() {
		return jq.NewErr("more than one JSON value")
	}
	return nil
}

func invalid(site, reason string) *core.RefusalError {
	return &core.RefusalError{Code: "drafts-invalid", Message: "drain.replies.json " + site + ": " + reason}
}
