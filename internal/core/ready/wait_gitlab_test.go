package readycore

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

const waitGitLabHead = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// gitlabWaitHost answers the glab reads pr-wait shares with ready; any other
// argv fails, so a stray call is visible in calls.
type gitlabWaitHost struct {
	mr        string
	approvals string
	discuss   string
	commit    string
	failOn    string
	calls     []string
}

func (*gitlabWaitHost) LookPath(string) error { return nil }

func (h *gitlabWaitHost) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	argv := strings.Join(cmd.Argv, " ")
	h.calls = append(h.calls, argv)
	if h.failOn != "" && strings.Contains(argv, h.failOn) {
		return core.Result{Stderr: "403 Forbidden\nsecond line", ExitCode: 1}, nil
	}
	switch {
	case strings.Contains(argv, "/repository/commits/"):
		return core.Result{Stdout: h.commit}, nil
	case strings.HasSuffix(argv, "/approvals"):
		return core.Result{Stdout: h.approvals}, nil
	case strings.Contains(argv, "/discussions?per_page=100"):
		return core.Result{Stdout: h.discuss}, nil
	case strings.HasSuffix(argv, "/merge_requests/7"):
		return core.Result{Stdout: h.mr}, nil
	}
	return core.Result{Stderr: "unstubbed", ExitCode: 1}, nil
}

func gitlabMR(state, pipeline string) string {
	return `{"state":"` + state + `","sha":"` + waitGitLabHead + `","head_pipeline":` + pipeline + `}`
}

func gitlabPipeline(status, ref, sha string) string {
	return `{"id":1,"status":"` + status + `","ref":"` + ref + `","sha":"` + sha + `"}`
}

func readGitLabWait(t *testing.T, host *gitlabWaitHost, until string) (*WaitSnapshot, error) {
	t.Helper()
	req := &core.ReadRequest{Repo: "acme/app", Pr: "7", Runtime: &core.Runtime{Commander: host}}
	return (&WaitProbe{}).Read(t.Context(), req, "gitlab", until)
}

const mergeCommit = "dddddddddddddddddddddddddddddddddddddddd"

func commitWithParents(parents ...string) string {
	return `{"id":"` + mergeCommit + `","parent_ids":["` + strings.Join(parents, `","`) + `"]}`
}

type gitlabCheckCase struct {
	name                string
	mr                  string
	commit              string
	state               string
	calls               int
	known, done, passed bool
}

const (
	branchRef   = "feat/retry"
	detachedRef = "refs/merge-requests/7/head"
	mergedRef   = "refs/merge-requests/7/merge"
	trainRef    = "refs/merge-requests/7/train"
	otherRef    = "refs/merge-requests/99/merge"
	targetSHA   = "ffffffffffffffffffffffffffffffffffffffff"
)

func open(pipeline string) string { return gitlabMR("opened", pipeline) }

// gitlabCheckCases is the check rollup and state matrix. The head pipeline is
// current when a branch or detached MR pipeline sits on the source head, or a
// merged-results or merge-train pipeline's merge commit lists that head as a
// parent; everything else reads as no check yet.
func gitlabCheckCases() []gitlabCheckCase {
	head := waitGitLabHead
	viaMergeCommit := func(status, ref, parent string) gitlabCheckCase {
		return gitlabCheckCase{
			mr: open(gitlabPipeline(status, ref, mergeCommit)), commit: commitWithParents(targetSHA, parent),
			state: "OPEN", calls: 2,
		}
	}
	cur := viaMergeCommit("success", mergedRef, head)
	cur.name, cur.known, cur.done, cur.passed = "merged-results pipeline of the current head", true, true, true
	train := viaMergeCommit("running", trainRef, head)
	train.name, train.known = "merge-train pipeline of the current head", true
	stale := viaMergeCommit("success", mergedRef, "old")
	stale.name = "merged-results pipeline of an older push is stale"
	return []gitlabCheckCase{
		{name: "running", mr: open(gitlabPipeline("running", branchRef, head)), state: "OPEN", calls: 1, known: true},
		{name: "waiting for callback is pending", mr: open(gitlabPipeline("waiting_for_callback", branchRef, head)), state: "OPEN", calls: 1, known: true},
		{name: "green", mr: open(gitlabPipeline("success", branchRef, head)), state: "OPEN", calls: 1, known: true, done: true, passed: true},
		{name: "failed", mr: open(gitlabPipeline("failed", branchRef, head)), state: "OPEN", calls: 1, known: true, done: true},
		{name: "detached pipeline on the head is current", mr: open(gitlabPipeline("success", detachedRef, head)), state: "OPEN", calls: 1, known: true, done: true, passed: true},
		{name: "detached pipeline of an older head is stale", mr: open(gitlabPipeline("success", detachedRef, "old")), state: "OPEN", calls: 1},
		cur, train, stale,
		{name: "another MR's merge ref is never trusted", mr: open(gitlabPipeline("success", otherRef, mergeCommit)), state: "OPEN", calls: 1},
		{name: "branch pipeline of an older head is stale", mr: open(gitlabPipeline("success", branchRef, "old")), state: "OPEN", calls: 1},
		{name: "no pipeline", mr: open(`null`), state: "OPEN", calls: 1},
		{name: "unknown status", mr: open(gitlabPipeline("mystery", branchRef, head)), state: "OPEN", calls: 1},
		{name: "merged", mr: gitlabMR("merged", `{"id":1,"status":"success"}`), state: "MERGED", calls: 1, known: true, done: true, passed: true},
		{name: "closed", mr: gitlabMR("closed", `null`), state: "CLOSED", calls: 1},
		{name: "locked is a merge in progress, not closed", mr: gitlabMR("locked", `null`), state: "OPEN", calls: 1},
	}
}

func TestWaitGitLabChecksAndState(t *testing.T) {
	for _, tc := range gitlabCheckCases() {
		t.Run(tc.name, func(t *testing.T) { assertGitLabCheckCase(t, &tc) })
	}
}

func assertGitLabCheckCase(t *testing.T, tc *gitlabCheckCase) {
	t.Helper()
	host := &gitlabWaitHost{mr: tc.mr, commit: tc.commit}
	snap, err := readGitLabWait(t, host, "checks-done")
	if err != nil {
		t.Fatal(err)
	}
	if snap.State != tc.state || snap.Head != waitGitLabHead || snap.ChecksKnown != tc.known ||
		snap.ChecksDone != tc.done || snap.ChecksGreen != tc.passed {
		t.Fatalf("snapshot = %+v", snap)
	}
	if len(host.calls) != tc.calls {
		t.Fatalf("calls = %q, want %d", host.calls, tc.calls)
	}
}

// TestWaitGitLabLockedNeverMatchesClosed pins the wait consequence: a locked
// MR is not closed, so --until closed keeps waiting.
func TestWaitGitLabLockedNeverMatchesClosed(t *testing.T) {
	snap, err := readGitLabWait(t, &gitlabWaitHost{mr: gitlabMR("locked", `null`)}, "closed")
	if err != nil {
		t.Fatal(err)
	}
	if waitMatches(untilClosed, nil, snap) || waitMatches(untilMerged, nil, snap) {
		t.Fatalf("a locked MR satisfied closed/merged: %+v", snap)
	}
}

func TestWaitGitLabReviewChangedReadsDiscussionsAndApprovals(t *testing.T) {
	host := &gitlabWaitHost{
		mr:        gitlabMR("opened", `null`),
		approvals: `{"approved_by":[{"user":{"username":"ada"}}]}`,
		discuss:   `[{"id":"d1","notes":[{"resolved":false,"system":false},{"system":false}]}]`,
	}
	snap, err := readGitLabWait(t, host, "review-changed")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Reviews != `["ada"]` || snap.Threads != `[{"id":"d1","notes":2,"resolved":false}]` {
		t.Fatalf("reviews=%s threads=%s", snap.Reviews, snap.Threads)
	}
}

// refusalCode unwraps a typed refusal; the code, not the text, is the contract.
func refusalCode(t *testing.T, err error) *core.RefusalError {
	t.Helper()
	ref, ok := errors.AsType[*core.RefusalError](err)
	if !ok {
		t.Fatalf("err = %v, want a *core.RefusalError", err)
	}
	return ref
}

func TestWaitGitLabHardFailures(t *testing.T) {
	_, err := readGitLabWait(t, &gitlabWaitHost{}, "merged")
	if ref := refusalCode(t, err); ref.Code != "pr-read-failed" {
		t.Fatalf("unreadable MR code = %q", ref.Code)
	}
	host := &gitlabWaitHost{mr: gitlabMR("opened", `null`), approvals: `[]`}
	_, err = readGitLabWait(t, host, "review-changed")
	if ref := refusalCode(t, err); ref.Code != "pr-read-failed" {
		t.Fatalf("unreadable approvals code = %q", ref.Code)
	}
	if _, err := readGitLabWait(t, &gitlabWaitHost{mr: `{"state":"opened"}`}, "merged"); err == nil {
		t.Fatal("a merge request with no head read as complete")
	}
}

// TestWaitGitLabRefusalKeepsHostError proves a 403 on a secondary read is not
// hidden behind a generic message: glab's stderr rides along, folded to one
// bounded line like the other read errors.
func TestWaitGitLabRefusalKeepsHostError(t *testing.T) {
	for _, failOn := range []string{"/approvals", "/discussions", "/repository/commits/"} {
		mr := gitlabMR("opened", gitlabPipeline("success", "refs/merge-requests/7/merge", mergeCommit))
		host := &gitlabWaitHost{mr: mr, failOn: failOn, approvals: `{}`, discuss: `[]`, commit: commitWithParents("x", waitGitLabHead)}
		_, err := readGitLabWait(t, host, "review-changed")
		ref := refusalCode(t, err)
		if ref.Code != "pr-read-failed" || !strings.Contains(ref.Message, "403 Forbidden") {
			t.Fatalf("%s: refusal = %+v", failOn, ref)
		}
	}
}

// TestWaitGitLabReorderIsNotAChange proves the API's list order is not a
// review change: the same approvers and discussions in another order render
// the same snapshot strings.
func TestWaitGitLabReorderIsNotAChange(t *testing.T) {
	read := func(approvals, discuss string) *WaitSnapshot {
		t.Helper()
		host := &gitlabWaitHost{mr: gitlabMR("opened", `null`), approvals: approvals, discuss: discuss}
		snap, err := readGitLabWait(t, host, "review-changed")
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	note := `"notes":[{"resolved":false,"system":false}]`
	first := read(`{"approved_by":[{"user":{"username":"bob"}},{"user":{"username":"ada"}}]}`,
		`[{"id":"b",`+note+`},{"id":"a",`+note+`}]`)
	second := read(`{"approved_by":[{"user":{"username":"ada"}},{"user":{"username":"bob"}}]}`,
		`[{"id":"a",`+note+`},{"id":"b",`+note+`}]`)
	if first.Reviews != second.Reviews || first.Threads != second.Threads {
		t.Fatalf("reorder changed the snapshot:\n%s | %s\n%s | %s", first.Reviews, first.Threads, second.Reviews, second.Threads)
	}
	if first.Reviews != `["ada","bob"]` || !strings.HasPrefix(first.Threads, `[{"id":"a"`) {
		t.Fatalf("not sorted: %s | %s", first.Reviews, first.Threads)
	}
}

// TestWaitGitLabEndToEnd drives the whole verb on a GitLab MR: a pipeline that
// finishes between observations ends the wait with the condition met.
func TestWaitGitLabEndToEnd(t *testing.T) {
	req, _, _ := waitTestRequest(t, "checks-green")
	host := &gitlabWaitHost{mr: gitlabMR("opened", `{"id":1,"status":"success","sha":"`+waitGitLabHead+`"}`)}
	req.Commander = host
	req.Pr = "7"
	req.Provider = "gitlab"
	resp, ref := Wait(t.Context(), req)
	if ref != nil || resp.Data.Outcome != "condition-met" {
		t.Fatalf("ref=%v resp=%+v", ref, resp)
	}
}

// TestWaitRefusesUnsupportedProviderBeforeAnyRead proves the refusal comes from
// the table at request time, not from deep inside a poll.
func TestWaitRefusesUnsupportedProviderBeforeAnyRead(t *testing.T) {
	req, host, _ := waitTestRequest(t, "merged")
	req.Provider = "forgejo"
	_, ref := Wait(t.Context(), req)
	want := "pr-wait has no live reads for forgejo — supported hosts: github, gitlab"
	if ref == nil || ref.Code != "provider-unimplemented" || ref.Message != want || host.polls != 0 {
		t.Fatalf("ref=%v polls=%d", ref, host.polls)
	}
}
