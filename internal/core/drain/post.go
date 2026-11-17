package drain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	threadscore "github.com/stainless-code/verdict/internal/core/threads"
)

// ReasonStaleThread names a draft whose waiting comment changed after the
// plan was made: the agent answered a question that is no longer the last one.
const ReasonStaleThread = "stale-thread"

// PostRequest carries one post run. The write callbacks are injected so the
// ordering and the idempotency rules can be tested without a host.
type PostRequest struct {
	Plan      *File
	DraftsRaw []byte
	Feed      *threadscore.File
	Ledger    []LedgerEntry
	Provider  string
	Repo      string
	PR        string
	At        string
	Reply     func(rootID, threadID, body string) (string, error)
	Resolve   func(threadID string) error
	Append    func(LedgerEntry) error
	SaveFeed  func(*threadscore.File) error
}

// PostResult is what the final receipt records.
type PostResult struct {
	Posted     []Posted
	Failed     []Failed
	Incomplete bool
}

// Post runs §6's gates in order, then posts each draft and records one ledger
// entry per successful write. A host failure is recorded and the remaining
// drafts are still attempted; the caller turns Incomplete into a refusal.
func Post(req *PostRequest) (*PostResult, *core.RefusalError) {
	if req.Plan == nil || req.Feed == nil {
		return nil, &core.RefusalError{Code: CodeDrainFailed, Message: "drain-failed: no plan or thread feed to post against"}
	}
	if len(bytes.TrimSpace(req.DraftsRaw)) == 0 {
		return nil, invalid("root", "no drafts; run drain --agent <name> first")
	}
	summary, noDraftsSkips, ref := CheckDrafts(req.DraftsRaw, req.Plan)
	if ref != nil {
		return nil, ref
	}
	req.Plan.Drafts = summary
	req.Plan.Skipped = append(req.Plan.Skipped, noDraftsSkips...)
	caps := core.CapabilitiesFor(req.Provider)
	if !caps.ThreadReply {
		return nil, &core.RefusalError{Code: "provider-unimplemented", Message: fmt.Sprintf(
			"thread replies on %s are not implemented (%s)", req.Provider, caps.ThreadReplyReason())}
	}
	var doc RepliesFile
	if err := json.Unmarshal(req.DraftsRaw, &doc); err != nil {
		return nil, invalid("root", err.Error())
	}
	if ref := secretGate(doc.Replies); ref != nil {
		return nil, ref
	}
	byID := make(map[string]*threadscore.Thread, len(req.Feed.Threads))
	for i := range req.Feed.Threads {
		byID[req.Feed.Threads[i].ID] = &req.Feed.Threads[i]
	}
	res := &PostResult{Posted: []Posted{}, Failed: []Failed{}}
	for _, reply := range doc.Replies {
		postOne(req, byID, reply, res)
	}
	req.Plan.Posted = res.Posted
	req.Plan.Failed = res.Failed
	at := req.At
	req.Plan.PostedAt = &at
	if req.SaveFeed != nil {
		if err := req.SaveFeed(req.Feed); err != nil {
			res.Failed = append(res.Failed, Failed{ThreadID: "", Reason: "could not update threads.json: " + err.Error()})
			req.Plan.Failed = res.Failed
		}
	}
	res.Incomplete = len(res.Failed) > 0
	return res, nil
}

func postOne(req *PostRequest, byID map[string]*threadscore.Thread, reply Reply, res *PostResult) {
	thread, ok := byID[reply.ThreadID]
	if !ok || len(thread.Comments) == 0 {
		res.Failed = append(res.Failed, Failed{ThreadID: reply.ThreadID, Reason: "thread missing from refreshed feed"})
		return
	}
	last := thread.Comments[len(thread.Comments)-1]
	switch {
	case thread.Resolved:
		req.Plan.Skipped = append(req.Plan.Skipped, Skip{ThreadID: reply.ThreadID, Reason: ReasonResolved})
		return
	case last.ID != reply.WaitingCommentID:
		req.Plan.Skipped = append(req.Plan.Skipped, Skip{ThreadID: reply.ThreadID, Reason: ReasonStaleThread})
		return
	case answered(req.Ledger, reply.ThreadID, &last):
		req.Plan.Skipped = append(req.Plan.Skipped, Skip{ThreadID: reply.ThreadID, Reason: ReasonAlreadyReplied})
		return
	case reply.Disposition == DispositionDefer:
		req.Plan.Skipped = append(req.Plan.Skipped, Skip{ThreadID: reply.ThreadID, Reason: "defer"})
		return
	}
	body := recordcore.NeutralizeMentions(reply.Body) + "\n\n" +
		threadscore.ReplyMarker(reply.ThreadID, reply.WaitingCommentID)
	id, err := req.Reply(thread.Comments[0].ID, reply.ThreadID, body)
	if err != nil {
		res.Failed = append(res.Failed, Failed{ThreadID: reply.ThreadID, Reason: err.Error()})
		return
	}
	entry := LedgerEntry{Thread: reply.ThreadID, ReplyID: id, At: req.At,
		InReplyTo: reply.WaitingCommentID, By: ReplyBy}
	if err := req.Append(entry); err != nil {
		res.Failed = append(res.Failed, Failed{ThreadID: reply.ThreadID, Reason: "could not record reply: " + err.Error()})
		return
	}
	req.Ledger = append(req.Ledger, entry)
	posted := Posted{ThreadID: reply.ThreadID, ReplyID: id, Disposition: string(reply.Disposition)}
	if reply.Disposition == DispositionWithdraw {
		if err := req.Resolve(reply.ThreadID); err != nil {
			res.Failed = append(res.Failed, Failed{ThreadID: reply.ThreadID, Reason: "could not resolve thread: " + err.Error()})
		} else {
			thread.Resolved = true
			posted.Resolved = true
		}
	}
	res.Posted = append(res.Posted, posted)
}

// secretGate scans every outbound body before the first write and names the
// draft site. There is no override on drain: a posted reply cannot be unsent.
func secretGate(replies []Reply) *core.RefusalError {
	for i, reply := range replies {
		if reply.Disposition == DispositionDefer {
			continue
		}
		if shapes := postcore.BodySecretShapes(reply.Body); len(shapes) > 0 {
			return &core.RefusalError{Code: postcore.CodeSecretShaped, Message: fmt.Sprintf(
				"%s: replies[%d].body carries a secret-shaped value (%s); remove it before posting",
				postcore.CodeSecretShaped, i, strings.Join(shapes, ", "))}
		}
	}
	return nil
}
