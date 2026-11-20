package drain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	threadscore "github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/untrusted"
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
	DryRun    bool
}

// PostResult is what the final receipt records.
type PostResult struct {
	Posted     []Posted
	Failed     []Failed
	Planned    []Planned
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
// entry per successful write. A dry run stops after composing each marked body
// and returns it in Planned. A host failure is recorded and the remaining
// drafts are still attempted; the caller turns Incomplete into a refusal.
func Post(req *PostRequest) (*PostResult, *core.RefusalError) {
	doc, ref := postDrafts(req)
	if ref != nil {
		return nil, ref
	}
	byID := postThreads(req.Feed)
	res := &PostResult{Posted: []Posted{}, Failed: []Failed{}, Planned: []Planned{}}
	for _, reply := range doc.Replies {
		postOne(req, byID, reply, res)
	}
	if req.DryRun {
		return res, nil
	}
	req.Plan.Posted = mergePosted(req.Plan.Posted, res.Posted)
	req.Plan.Failed = mergeFailed(req.Plan.Failed, res.Failed, res.Posted)
	at := req.At
	req.Plan.PostedAt = &at
	saveFeed(req, res)
	res.Incomplete = len(res.Failed) > 0
	return res, nil
}

func mergePosted(existing, extra []Posted) []Posted {
	out := make([]Posted, 0, len(existing)+len(extra))
	out = append(out, existing...)
	for _, p := range extra {
		idx := slices.IndexFunc(out, func(e Posted) bool {
			return e.ThreadID != "" && e.ThreadID == p.ThreadID
		})
		if idx >= 0 {
			out[idx] = p
		} else {
			out = append(out, p)
		}
	}
	return out
}

func mergeFailed(existing, extra []Failed, postedNow []Posted) []Failed {
	out := keepUnpostedFailures(existing, postedNow)
	for _, f := range extra {
		out = upsertFailed(out, f)
	}
	return out
}

func keepUnpostedFailures(existing []Failed, postedNow []Posted) []Failed {
	out := make([]Failed, 0, len(existing))
	for _, e := range existing {
		if e.ThreadID == "" || !hasPostedThread(postedNow, e.ThreadID) {
			out = append(out, e)
		}
	}
	return out
}

func upsertFailed(out []Failed, f Failed) []Failed {
	idx := slices.IndexFunc(out, func(e Failed) bool {
		if f.ThreadID != "" {
			return e.ThreadID == f.ThreadID
		}
		return e == f
	})
	if idx >= 0 {
		out[idx] = f
		return out
	}
	return append(out, f)
}

func hasPostedThread(posted []Posted, threadID string) bool {
	return slices.ContainsFunc(posted, func(p Posted) bool {
		return p.ThreadID == threadID
	})
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
		if !slices.Contains(existing, s) {
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
		req.Plan.Failed = mergeFailed(req.Plan.Failed, res.Failed, res.Posted)
	}
}

func postOne(req *PostRequest, byID map[string]*threadscore.Thread, reply Reply, res *PostResult) {
	thread, ok := byID[reply.ThreadID]
	if !ok || len(thread.Comments) == 0 {
		missingThread(req, reply, res)
		return
	}
	last := &thread.Comments[len(thread.Comments)-1]
	if reason := postSkipReason(req, reply, thread, last); reason != "" {
		req.Plan.Skipped = appendUniqueSkips(req.Plan.Skipped, []Skip{{ThreadID: reply.ThreadID, Reason: reason}})
		return
	}
	body := untrusted.NeutralizeMentions(reply.Body) + "\n\n" +
		threadscore.ReplyMarker(reply.ThreadID, reply.WaitingCommentID)
	if req.DryRun {
		planReply(reply, thread, res, body)
		return
	}
	id, err := req.Reply(thread.Comments[0].ID, reply.ThreadID, body)
	if err != nil {
		res.Failed = append(res.Failed, Failed{ThreadID: reply.ThreadID, Reason: err.Error()})
		return
	}
	if posted, ok := recordPost(req, thread, reply, id, res); ok {
		res.Posted = append(res.Posted, posted)
	}
}

func missingThread(req *PostRequest, reply Reply, res *PostResult) {
	reason := "thread missing from refreshed feed"
	if req.DryRun {
		req.Plan.Skipped = appendUniqueSkips(req.Plan.Skipped, []Skip{{ThreadID: reply.ThreadID, Reason: reason}})
		return
	}
	res.Failed = append(res.Failed, Failed{ThreadID: reply.ThreadID, Reason: reason})
}

func planReply(reply Reply, thread *threadscore.Thread, res *PostResult, body string) {
	res.Planned = append(res.Planned, Planned{
		ThreadID: reply.ThreadID, RootCommentID: thread.Comments[0].ID,
		Disposition: string(reply.Disposition), Body: body,
	})
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
