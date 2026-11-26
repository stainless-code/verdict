package readycore

import (
	"context"
	"slices"
	"strings"

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
	checks, ref := gitlabWaitChecks(ctx, st, snapshot.Head)
	if ref != nil {
		return nil, ref
	}
	snapshot.Checks = jq.Compact(checks)
	snapshot.ChecksKnown, snapshot.ChecksDone, snapshot.ChecksGreen = tallyChecks(checks, gitlabWaitPipeline)
	if until == untilReviewChanged {
		if snapshot.Reviews, snapshot.Threads, ref = gitlabWaitReviews(ctx, st); ref != nil {
			return nil, ref
		}
	}
	if !validWaitState(snapshot) {
		return nil, incompleteRead()
	}
	return snapshot, nil
}

// gitlabWaitChecks reports the merge request's head pipeline as its one check
// when that pipeline belongs to the current source head; a pipeline for an
// older head says nothing about it and reads as no check yet, which stays
// pending exactly like an empty GitHub rollup.
func gitlabWaitChecks(ctx context.Context, st *state, head string) ([]map[string]any, *core.RefusalError) {
	pipeline, ok := st.live["head_pipeline"].(map[string]any)
	if !ok {
		return nil, nil
	}
	current, ref := gitlabPipelineCurrent(ctx, st, pipeline, head)
	if ref != nil || !current {
		return nil, ref
	}
	return []map[string]any{{keyID: pipeline[keyID], keyPipelineStatus: pipeline[keyPipelineStatus]}}, nil
}

// gitlabPipelineCurrent decides whether the head pipeline ran for the MR's
// current source head. A branch or detached MR pipeline runs on the source
// head itself, so its sha must equal it. A merged-results or merge-train
// pipeline (ref refs/merge-requests/<iid>/merge or /train) runs on a synthetic
// merge commit whose sha is never the head, so it is current only when the head
// is one of that commit's parents; a merge commit built from an older push does
// not list the new head, and the read says no check yet.
func gitlabPipelineCurrent(ctx context.Context, st *state, pipeline map[string]any, head string) (bool, *core.RefusalError) {
	sha := jq.StrOf(pipeline["sha"])
	if sha == "" || sha == head {
		return true, nil
	}
	if !gitlabMergeRef(jq.StrOf(pipeline["ref"]), st.pr) {
		return false, nil
	}
	return gitlabCommitHasParent(ctx, st, sha, head)
}

// gitlabCommitHasParent reads one commit and reports whether parent is among
// its parents.
func gitlabCommitHasParent(ctx context.Context, st *state, sha, parent string) (bool, *core.RefusalError) {
	stdout, stderr, ok := coreexec.Host(ctx, st.commander(), toolGlab, "api", gitlabCommitPath(st.repo, sha))
	if !ok {
		return false, gitlabWaitRead(st, "merge commit "+sha, stderr)
	}
	doc, err := jq.Decode([]byte(stdout))
	commit, isMap := jq.AsMap(doc)
	if err != nil || !isMap {
		return false, waitReadFailed()
	}
	for _, p := range jq.Elements(commit["parent_ids"]) {
		if jq.StrOf(p) == parent {
			return true, nil
		}
	}
	return false, nil
}

// gitlabMergeRef reports a pipeline ref that names this MR's merged-results or
// merge-train commit.
func gitlabMergeRef(ref, iid string) bool {
	rest, ok := strings.CutPrefix(ref, "refs/merge-requests/"+iid+"/")
	return ok && (rest == "merge" || rest == "train")
}

// gitlabWaitRead names a failed glab read with its bounded first stderr line,
// so an auth or permission failure is not hidden behind a generic message.
func gitlabWaitRead(st *state, what, stderr string) *core.RefusalError {
	return &core.RefusalError{
		Code:    waitReadCode,
		Message: core.Msgf("could not read %s for %s"+hashSep+"%s: %s", what, st.repo, st.pr, firstLine(stderr)),
	}
}

// gitlabWaitPipeline classifies one pipeline status as terminal, passed, known.
// The list is GitLab's full status vocabulary; a status outside it stays
// unknown, so a new one can never read as finished.
func gitlabWaitPipeline(check map[string]any) (bool, bool, bool) {
	switch jq.StrOf(check[keyPipelineStatus]) {
	case "created", "waiting_for_resource", "preparing", "waiting_for_callback", "pending", "running",
		"scheduled", "manual", "canceling":
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
// Both lists are sorted, because the API's order is not part of its contract
// and a reorder must not read as a change.
func gitlabWaitReviews(ctx context.Context, st *state) (reviews, threads string, ref *core.RefusalError) {
	approvals, ref := gitlabReadApprovals(ctx, st)
	if ref != nil {
		return "", "", ref
	}
	rows, ref := gitlabReadThreadRows(ctx, st)
	if ref != nil {
		return "", "", ref
	}
	return jq.Compact(approvals), jq.Compact(rows), nil
}

func gitlabReadThreadRows(ctx context.Context, st *state) ([]any, *core.RefusalError) {
	stdout, stderr, ok := gitlabReadDiscussions(ctx, st)
	if !ok {
		return nil, gitlabWaitRead(st, "review threads", stderr)
	}
	discussions, err := gitlabDiscussions(stdout)
	if err != nil {
		return nil, waitReadFailed()
	}
	rows := make([]map[string]any, 0, len(discussions))
	for _, d := range discussions {
		m, ok := d.(map[string]any)
		if !ok {
			return nil, waitReadFailed()
		}
		rows = append(rows, gitlabThreadRow(m))
	}
	slices.SortStableFunc(rows, func(a, b map[string]any) int { return strings.Compare(jq.R(a[keyID]), jq.R(b[keyID])) })
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, row)
	}
	return out, nil
}

func gitlabThreadRow(m map[string]any) map[string]any {
	row := map[string]any{keyID: m[keyID], "notes": len(jq.Elements(m["notes"]))}
	if root, ok := gitlabRootNote(m); ok {
		row["resolved"] = root["resolved"]
	}
	return row
}

// gitlabReadApprovals lists the usernames that approved the merge request,
// sorted.
func gitlabReadApprovals(ctx context.Context, st *state) ([]string, *core.RefusalError) {
	stdout, stderr, ok := coreexec.Host(ctx, st.commander(), toolGlab, "api", gitlabAPIPath(st.repo, st.pr, "/approvals"))
	if !ok {
		return nil, gitlabWaitRead(st, "merge request approvals", stderr)
	}
	doc, err := jq.Decode([]byte(stdout))
	m, isMap := jq.AsMap(doc)
	if err != nil || !isMap {
		return nil, waitReadFailed()
	}
	names := []string{}
	for _, entry := range jq.Elements(m["approved_by"]) {
		if name, ok := approverName(entry); ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names, nil
}

func approverName(entry any) (string, bool) {
	em, ok := entry.(map[string]any)
	if !ok {
		return "", false
	}
	user, ok := em["user"].(map[string]any)
	if !ok {
		return "", false
	}
	return jq.StrOf(user["username"]), true
}
