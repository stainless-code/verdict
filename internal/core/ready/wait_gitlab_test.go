package readycore

import (
	"context"
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
	calls     []string
}

func (*gitlabWaitHost) LookPath(string) error { return nil }

func (h *gitlabWaitHost) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	argv := strings.Join(cmd.Argv, " ")
	h.calls = append(h.calls, argv)
	switch {
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

func readGitLabWait(t *testing.T, host *gitlabWaitHost, until string) (*WaitSnapshot, error) {
	t.Helper()
	req := &core.ReadRequest{Repo: "acme/app", Pr: "7", Runtime: &core.Runtime{Commander: host}}
	return (&WaitProbe{}).Read(t.Context(), req, "gitlab", until)
}

func TestWaitGitLabChecksAndState(t *testing.T) {
	cases := []struct {
		name                string
		mr                  string
		state               string
		known, done, passed bool
	}{
		{"running", gitlabMR("opened", `{"id":1,"status":"running","sha":"`+waitGitLabHead+`"}`), "OPEN", true, false, false},
		{"green", gitlabMR("opened", `{"id":1,"status":"success","sha":"`+waitGitLabHead+`"}`), "OPEN", true, true, true},
		{"failed", gitlabMR("opened", `{"id":1,"status":"failed","sha":"`+waitGitLabHead+`"}`), "OPEN", true, true, false},
		{"no pipeline", gitlabMR("opened", `null`), "OPEN", false, false, false},
		{"pipeline of an older head", gitlabMR("opened", `{"id":1,"status":"success","sha":"old"}`), "OPEN", false, false, false},
		{"unknown status", gitlabMR("opened", `{"id":1,"status":"mystery","sha":"`+waitGitLabHead+`"}`), "OPEN", false, false, false},
		{"merged", gitlabMR("merged", `{"id":1,"status":"success"}`), "MERGED", true, true, true},
		{"closed", gitlabMR("closed", `null`), "CLOSED", false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertGitLabSnapshot(t, tc.mr, tc.state, tc.known, tc.done, tc.passed) })
	}
}

func assertGitLabSnapshot(t *testing.T, mr, state string, known, done, passed bool) {
	t.Helper()
	host := &gitlabWaitHost{mr: mr}
	snap, err := readGitLabWait(t, host, "checks-done")
	if err != nil {
		t.Fatal(err)
	}
	if snap.State != state || snap.Head != waitGitLabHead || snap.ChecksKnown != known ||
		snap.ChecksDone != done || snap.ChecksGreen != passed {
		t.Fatalf("snapshot = %+v", snap)
	}
	if len(host.calls) != 1 {
		t.Fatalf("calls = %q; only the merge request read is needed", host.calls)
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

func TestWaitGitLabHardFailures(t *testing.T) {
	if _, err := readGitLabWait(t, &gitlabWaitHost{}, "merged"); err == nil || !strings.Contains(err.Error(), "pr-read-failed") {
		t.Fatalf("unreadable MR err = %v", err)
	}
	host := &gitlabWaitHost{mr: gitlabMR("opened", `null`), approvals: `[]`}
	if _, err := readGitLabWait(t, host, "review-changed"); err == nil || !strings.Contains(err.Error(), "pr-read-failed") {
		t.Fatalf("unreadable approvals err = %v", err)
	}
	if _, err := readGitLabWait(t, &gitlabWaitHost{mr: `{"state":"opened"}`}, "merged"); err == nil {
		t.Fatal("a merge request with no head read as complete")
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
