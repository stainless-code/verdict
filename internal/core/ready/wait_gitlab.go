package readycore

import (
	"context"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
)

const keyID = "id"

// gitlab reads one GitLab observation through the same glab reads `ready`
// uses: the live merge request (state, head, head pipeline) and, for
// review-changed only, the discussions. It writes no local file.
func (*WaitProbe) gitlab(ctx context.Context, req *core.ReadRequest, until string) (*WaitSnapshot, error) {
	st := &state{req: &Request{ReadRequest: *req}, repo: req.Repo, pr: req.Pr}
	if _, ref := gitlabReadLive(ctx, st); ref != nil {
		return nil, ref
	}
	snapshot := &WaitSnapshot{
		State: jq.StrOf(st.live["state"]), Head: jq.StrOf(st.live["headRefOid"]),
		Reviews: "[]", Threads: "[]",
	}
	checks := gitlabWaitChecks(st.live, snapshot.Head)
	snapshot.Checks = jq.Compact(checks)
	snapshot.ChecksKnown, snapshot.ChecksDone, snapshot.ChecksGreen = tallyChecks(checks, gitlabWaitPipeline)
	if until == untilReviewChanged {
		var ref *core.RefusalError
		if snapshot.Reviews, snapshot.Threads, ref = gitlabWaitReviews(ctx, st); ref != nil {
			return nil, ref
		}
	}
	if !validWaitState(snapshot) {
		return nil, incompleteRead()
	}
	return snapshot, nil
}

// gitlabWaitChecks reports the merge request's head pipeline as its one check.
// A pipeline for an older commit says nothing about this head, so it reads as
// no check yet, which stays pending exactly like an empty GitHub rollup.
func gitlabWaitChecks(live map[string]any, head string) []map[string]any {
	pipeline, ok := live["head_pipeline"].(map[string]any)
	if !ok {
		return nil
	}
	if sha := jq.StrOf(pipeline["sha"]); sha != "" && sha != head {
		return nil
	}
	return []map[string]any{{keyID: pipeline[keyID], keyPipelineStatus: pipeline[keyPipelineStatus]}}
}

// gitlabWaitPipeline classifies one pipeline status as terminal, passed, known.
func gitlabWaitPipeline(check map[string]any) (bool, bool, bool) {
	switch jq.StrOf(check[keyPipelineStatus]) {
	case "created", "waiting_for_resource", "preparing", "pending", "running", "scheduled", "manual", "canceling":
		return false, false, true
	case "success", "skipped":
		return true, true, true
	case "failed", codeCanceled:
		return true, false, true
	}
	return false, false, false
}

// gitlabWaitReviews renders the review signal as canonical JSON: who approved,
// and each discussion's id, resolution, and note count. GitLab has no review
// object, so a new approval, thread, reply, or resolution is what moves it.
func gitlabWaitReviews(ctx context.Context, st *state) (reviews, threads string, ref *core.RefusalError) {
	approvals, ok := gitlabReadApprovals(ctx, st)
	if !ok {
		return "", "", waitReadFailed()
	}
	rows, ok := gitlabReadThreadRows(ctx, st)
	if !ok {
		return "", "", waitReadFailed()
	}
	return jq.Compact(approvals), jq.Compact(rows), nil
}

func gitlabReadThreadRows(ctx context.Context, st *state) ([]any, bool) {
	stdout, ok := gitlabReadDiscussions(ctx, st)
	if !ok {
		return nil, false
	}
	discussions, err := gitlabDiscussions(stdout)
	if err != nil {
		return nil, false
	}
	rows := make([]any, 0, len(discussions))
	for _, d := range discussions {
		m, ok := d.(map[string]any)
		if !ok {
			return nil, false
		}
		rows = append(rows, gitlabThreadRow(m))
	}
	return rows, true
}

func gitlabThreadRow(m map[string]any) map[string]any {
	row := map[string]any{keyID: m[keyID], "notes": len(jq.Elements(m["notes"]))}
	if root, ok := gitlabRootNote(m); ok {
		row["resolved"] = root["resolved"]
	}
	return row
}

// gitlabReadApprovals lists the usernames that approved the merge request.
func gitlabReadApprovals(ctx context.Context, st *state) ([]any, bool) {
	stdout, _, ok := coreexec.Host(ctx, st.commander(), toolGlab, "api", gitlabAPIPath(st.repo, st.pr, "/approvals"))
	if !ok {
		return nil, false
	}
	doc, err := jq.Decode([]byte(stdout))
	m, isMap := jq.AsMap(doc)
	if err != nil || !isMap {
		return nil, false
	}
	names := []any{}
	for _, entry := range jq.Elements(m["approved_by"]) {
		if name, ok := approverName(entry); ok {
			names = append(names, name)
		}
	}
	return names, true
}

func approverName(entry any) (any, bool) {
	em, ok := entry.(map[string]any)
	if !ok {
		return nil, false
	}
	user, ok := em["user"].(map[string]any)
	if !ok {
		return nil, false
	}
	return user["username"], true
}
