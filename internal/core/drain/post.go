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

// PostRequest carries one post run.
type PostRequest struct {
	Plan      *File
	Feed      *threadscore.File
	Reply     func(rootID, threadID, body string) (string, error)
	Resolve   func(threadID string) error
	Append    func(*LedgerEntry) error
	SaveFeed  func(*threadscore.File) error
	Provider  string
	Repo      string
	PR        string
	At        string
	DraftsRaw []byte
	Ledger    []LedgerEntry
}

// PostResult is what the final receipt records.
type PostResult struct {
	Posted     []Posted
	Failed     []Failed
	Incomplete bool
}

// ValidatePostDrafts runs the local §6 preflight gates without mutating plan:
// missing/invalid/stale drafts, ThreadReply capability, and secret scan.
func ValidatePostDrafts(plan *File, draftsRaw []byte, provider string) (*RepliesFile, *Drafts, []Skip, *core.RefusalError) {
	if plan == nil {
		return nil, nil, nil, &core.RefusalError{Code: CodeDrainFailed, Message: "drain-failed: no plan to post against"}
	}
	if len(bytes.TrimSpace(draftsRaw)) == 0 {
		return nil, nil, nil, invalid("root", "no drafts; run drain --agent <name> first")
	}
	summary, noDraftsSkips, ref := CheckDrafts(draftsRaw, plan)
	if ref != nil {
		return nil, nil, nil, ref
	}
	caps := core.CapabilitiesFor(provider)
	if !caps.ThreadReply {
		return nil, nil, nil, &core.RefusalError{Code: "provider-unimplemented", Message: fmt.Sprintf(
			"thread replies on %s are not implemented (%s)", provider, caps.ThreadReplyReason())}
	}
	var doc RepliesFile
	if err := json.Unmarshal(draftsRaw, &doc); err != nil {
		return nil, nil, nil, invalid("root", err.Error())
	}
	if ref := secretGate(doc.Replies); ref != nil {
		return nil, nil, nil, ref
	}
	return &doc, summary, noDraftsSkips, nil
}

// Post runs §6's gates in order, then posts each draft and records one ledger
// entry per successful write. A host failure is recorded and the remaining
// drafts are still attempted; the caller turns Incomplete into a refusal.
func Post(req *PostRequest) (*PostResult, *core.RefusalError) {
	doc, ref := postDrafts(req)
	if ref != nil {
		return nil, ref
	}
	byID := postThreads(req.Feed)
	res := &PostResult{Posted: []Posted{}, Failed: []Failed{}}
	for _, reply := range doc.Replies {
		postOne(req, byID, reply, res)
	}
	req.Plan.Posted = res.Posted
	req.Plan.Failed = res.Failed
	at := req.At
	req.Plan.PostedAt = &at
	saveFeed(req, res)
	res.Incomplete = len(res.Failed) > 0
	return res, nil
}

func postDrafts(req *PostRequest) (*RepliesFile, *core.RefusalError) {
	if req.Plan == nil || req.Feed == nil {
		return nil, &core.RefusalError{Code: CodeDrainFailed, Message: "drain-failed: no plan or thread feed to post against"}
	}
	doc, summary, noDraftsSkips, ref := ValidatePostDrafts(req.Plan, req.DraftsRaw, req.Provider)
	if ref != nil {
		return nil, ref
	}
	req.Plan.Drafts = summary
	req.Plan.Skipped = appendUniqueSkips(req.Plan.Skipped, noDraftsSkips)
	return doc, nil
}

func appendUniqueSkips(existing, extra []Skip) []Skip {
	for _, s := range extra {
		dup := false
		for _, prev := range existing {
			if prev.ThreadID == s.ThreadID && prev.Reason == s.Reason {
				dup = true
				break
			}
		}
		if !dup {
			existing = append(existing, s)
		}
	}
	return existing
}

func postThreads(feed *threadscore.File) map[string]*threadscore.Thread {
	byID := make(map[string]*threadscore.Thread, len(feed.Threads))
	for i := range feed.Threads {
		byID[feed.Threads[i].ID] = &feed.Threads[i]
	}
	return byID
}

func saveFeed(req *PostRequest, res *PostResult) {
	if req.SaveFeed == nil {
		return
	}
	if err := req.SaveFeed(req.Feed); err != nil {
		res.Failed = append(res.Failed, Failed{Reason: "could not update threads.json: " + err.Error()})
		req.Plan.Failed = res.Failed
	}
}

func postOne(req *PostRequest, byID map[string]*threadscore.Thread, reply Reply, res *PostResult) {
	thread, ok := byID[reply.ThreadID]
	if !ok || len(thread.Comments) == 0 {
		res.Failed = append(res.Failed, Failed{ThreadID: reply.ThreadID, Reason: "thread missing from refreshed feed"})
		return
	}
	last := &thread.Comments[len(thread.Comments)-1]
	if reason := postSkipReason(req, reply, thread, last); reason != "" {
		req.Plan.Skipped = append(req.Plan.Skipped, Skip{ThreadID: reply.ThreadID, Reason: reason})
		return
	}
	body := recordcore.NeutralizeMentions(reply.Body) + "\n\n" +
		threadscore.ReplyMarker(reply.ThreadID, reply.WaitingCommentID)
	id, err := req.Reply(thread.Comments[0].ID, reply.ThreadID, body)
	if err != nil {
		res.Failed = append(res.Failed, Failed{ThreadID: reply.ThreadID, Reason: err.Error()})
		return
	}
	if posted, ok := recordPost(req, thread, reply, id, res); ok {
		res.Posted = append(res.Posted, posted)
	}
}

func recordPost(req *PostRequest, thread *threadscore.Thread, reply Reply, id string, res *PostResult) (Posted, bool) {
	entry := LedgerEntry{
		Thread: reply.ThreadID, ReplyID: id, At: req.At,
		InReplyTo: reply.WaitingCommentID, By: ReplyBy,
	}
	if err := req.Append(&entry); err != nil {
		res.Failed = append(res.Failed, Failed{ThreadID: reply.ThreadID, Reason: "could not record reply: " + err.Error()})
		return Posted{}, false
	}
	req.Ledger = append(req.Ledger, entry)
	posted := Posted{ThreadID: reply.ThreadID, ReplyID: id, Disposition: string(reply.Disposition)}
	if reply.Disposition != DispositionWithdraw {
		return posted, true
	}
	if err := req.Resolve(reply.ThreadID); err != nil {
		res.Failed = append(res.Failed, Failed{ThreadID: reply.ThreadID, Reason: "could not resolve thread: " + err.Error()})
		return posted, true
	}
	thread.Resolved = true
	posted.Resolved = true
	return posted, true
}

func postSkipReason(req *PostRequest, reply Reply, thread *threadscore.Thread, last *threadscore.Comment) string {
	switch {
	case thread.Resolved:
		return ReasonResolved
	case last.ID != reply.WaitingCommentID:
		return ReasonStaleThread
	case answered(req.Ledger, reply.ThreadID, last):
		return ReasonAlreadyReplied
	case reply.Disposition == DispositionDefer:
		return "defer"
	default:
		return ""
	}
}

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
