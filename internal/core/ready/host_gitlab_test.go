package readycore_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	readycore "github.com/stainless-code/verdict/internal/core/ready"
)

// gitlabReadyCommander answers the three GitLab live reads: the merge
// request, its pipelines, and its discussions.
type gitlabReadyCommander struct {
	mr        string
	pipelines string
	pipeFail  bool
	discuss   string
}

func (c *gitlabReadyCommander) LookPath(string) error { return nil }

func (c *gitlabReadyCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	joined := strings.Join(cmd.Argv, " ")
	switch {
	case strings.Contains(joined, "/pipelines?per_page=100"):
		if c.pipeFail {
			return core.Result{Stderr: "boom", ExitCode: 1}, nil
		}
		return core.Result{Stdout: c.pipelines}, nil
	case strings.Contains(joined, "/discussions?per_page=100"):
		return core.Result{Stdout: c.discuss}, nil
	case strings.Contains(joined, "/merge_requests/"):
		return core.Result{Stdout: c.mr}, nil
	}
	return core.Result{Stderr: "unstubbed", ExitCode: 1}, nil
}

const gitlabHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

const gitlabLongDescription = "A description that is long enough to pass the thin check easily here."

// writeGitLabReadyBundle writes a GitLab bundle over a satisfied capture,
// so only the live reads can block readiness.
func writeGitLabReadyBundle(t *testing.T) {
	t.Helper()
	_, rs := complianceResolved(t)
	rs.Outcomes = []config.CheckObservation{{
		Check: "head-current", Group: rs.Resolved.Plan.Groups[0].ID,
		Head: gitlabHead, Passed: true,
	}}
	bundle := writeReadyBundle(t, rs)
	write(t, filepath.Join(bundle, "PR.json"),
		`{"provider":"gitlab","number":5201,"head_sha":"`+gitlabHead+`","state":"open"}`)
	write(t, filepath.Join(bundle, "findings.json"), `[]`)
}

func runGitLabReady(t *testing.T, cmdr core.Commander) *readycore.Response {
	t.Helper()
	req := &readycore.Request{Check: true}
	req.ReadRequest = core.ReadRequest{
		Runtime: &core.Runtime{Commander: cmdr},
		Repo:    "acme/app", Pr: "5201",
	}
	resp, ref := readycore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("ready refusal: %v", ref)
	}
	return resp
}

func blockerKinds(blockers []readycore.Blocker) string {
	kinds := make([]string, 0, len(blockers))
	for _, b := range blockers {
		kinds = append(kinds, b.Kind)
	}
	return strings.Join(kinds, ",")
}

// TestReadyGitLabBlocked proves the GitLab read reports the draft,
// conflict, and failing-pipeline blockers together and fails the gate.
func TestReadyGitLabBlocked(t *testing.T) {
	writeGitLabReadyBundle(t)
	resp := runGitLabReady(t, &gitlabReadyCommander{
		mr: `{"id":9001,"iid":5201,"title":"Retry count off by one",` +
			`"description":"` + gitlabLongDescription + `",` +
			`"state":"opened","draft":true,"has_conflicts":true,"detailed_merge_status":"conflict",` +
			`"sha":"` + gitlabHead + `","merged_at":null,` +
			`"head_pipeline":{"id":12,"status":"success","sha":"` + gitlabHead + `"}}`,
		pipelines: `[{"id":12,"status":"success"},{"id":13,"status":"failed"}]`,
		discuss:   `[{"id":"d1","notes":[{"id":1,"body":"lgtm","resolved":true,"system":false}]}]`,
	})
	if resp.Ready {
		t.Fatal("ready passed on a draft, conflicting MR with a failed pipeline")
	}
	if !resp.GateFailed {
		t.Fatal("ready --check passed on a draft, conflicting MR with a failed pipeline")
	}
	if got := blockerKinds(resp.Data.Blockers); got != "ci-failing,draft,conflict" {
		t.Fatalf("blockers = %q, want ci-failing,draft,conflict", got)
	}
	if got := resp.Data.Blockers[0].Detail; got != "1 failing check(s): #13 failed" {
		t.Errorf("ci detail = %q, want the failed pipeline named", got)
	}
	if got := resp.Data.Blockers[2].Detail; got != "mergeable=CONFLICTING mergeStateStatus=CONFLICT" {
		t.Errorf("conflict detail = %q, want both merge fields", got)
	}
	if len(resp.Warnings) != 0 {
		t.Errorf("warnings = %v, want none on a fully read MR", resp.Warnings)
	}
}

// TestReadyGitLabClean proves a clean merge request reports ready with no
// blockers and a passing gate.
func TestReadyGitLabClean(t *testing.T) {
	writeGitLabReadyBundle(t)
	resp := runGitLabReady(t, &gitlabReadyCommander{
		mr: `{"id":9001,"iid":5201,"title":"Retry count off by one",` +
			`"description":"` + gitlabLongDescription + `",` +
			`"state":"opened","draft":false,"has_conflicts":false,` +
			`"sha":"` + gitlabHead + `","merged_at":null,` +
			`"head_pipeline":{"id":12,"status":"success","sha":"` + gitlabHead + `"}}`,
		pipelines: `[{"id":12,"status":"success"}]`,
		discuss:   `[{"id":"d1","notes":[{"id":1,"body":"lgtm","resolved":true,"system":false}]}]`,
	})
	if !resp.Ready || resp.GateFailed {
		t.Fatalf("ready = %v gateFailed = %v blockers = %+v, want ready and passing",
			resp.Ready, resp.GateFailed, resp.Data.Blockers)
	}
	if len(resp.Data.Blockers) != 0 {
		t.Fatalf("blockers = %+v, want none on a clean MR", resp.Data.Blockers)
	}
}

// TestReadyGitLabHeadPipelineFallback proves a failed head pipeline still
// blocks when the pipeline list read is degraded.
func TestReadyGitLabHeadPipelineFallback(t *testing.T) {
	writeGitLabReadyBundle(t)
	resp := runGitLabReady(t, &gitlabReadyCommander{
		mr: `{"id":9001,"iid":5201,"title":"Retry count off by one",` +
			`"description":"` + gitlabLongDescription + `",` +
			`"state":"opened","draft":false,"has_conflicts":false,` +
			`"sha":"` + gitlabHead + `","merged_at":null,` +
			`"head_pipeline":{"id":12,"status":"failed","sha":"` + gitlabHead + `"}}`,
		pipeFail: true,
		discuss:  `[]`,
	})
	if resp.Ready {
		t.Fatal("ready passed with a failed head pipeline and no list read")
	}
	if got := blockerKinds(resp.Data.Blockers); got != "ci-failing" {
		t.Fatalf("blockers = %q, want only ci-failing", got)
	}
	if got := resp.Data.Blockers[0].Detail; got != "1 failing check(s): head pipeline failed" {
		t.Errorf("ci detail = %q, want the head pipeline named", got)
	}
}
