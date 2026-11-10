package fetchcore

import (
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// liveRequest builds one live-thread read request over a scripted commander.
func liveRequest(cmdr Commander, repo, pr string) core.ReadRequest {
	return core.ReadRequest{
		Runtime: &core.Runtime{Env: map[string]string{}, Commander: cmdr},
		Repo:    repo,
		Pr:      pr,
	}
}

// githubLiveCall is the script key for the resolution-only GraphQL read the
// gate issues. The report goldens pin its exact argv; this key routes it.
func githubLiveCall() string {
	return "gh\x00" + strings.Join(readcommon.ThreadsArgv("acme", "app", "5101", readcommon.QueryThreads), "\x00")
}

// TestReadLiveThreadsGitHubTallies pins the GitHub arm: no provider key means
// the GitHub read, an open thread and a node whose resolution state is
// unreadable both count open, and the malformed node is reported on top.
func TestReadLiveThreadsGitHubTallies(t *testing.T) {
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		githubLiveCall(): {stdout: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[` +
			`{"isResolved":true},{"isResolved":false},{"isResolved":"yes"}]}}}}}`},
	}}
	read, err := ReadLiveThreads(context.Background(), liveRequest(cmdr, "acme/app", "5101"), "", "", nil)
	if err != nil {
		t.Fatalf("ReadLiveThreads error: %v", err)
	}
	if read.Open != 2 || read.Malformed != 1 || read.Truncated {
		t.Errorf("read = %+v, want open 2, malformed 1, complete", read)
	}
	if len(cmdr.calls) != 1 {
		t.Errorf("calls = %d, want one paginated GraphQL read", len(cmdr.calls))
	}
}

// TestReadLiveThreadsGitLabTallies pins the adapter arm: a GitLab read resolves
// the project id, reads the discussions, and counts the two unresolved ones as
// open while the resolved one counts nothing.
func TestReadLiveThreadsGitLabTallies(t *testing.T) {
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		"glab\x00api\x00projects/acme%2Fapp\x00--jq\x00.id": {stdout: "11\n"},
		"glab\x00api\x00projects/11/merge_requests/5101/discussions?per_page=100&page=1": {stdout: `[` +
			`{"id":"d1","notes":[{"id":1,"body":"open nit","author":{"username":"ana"}}]},` +
			`{"id":"d2","notes":[{"id":2,"body":"still open","author":{"username":"ana"}}]},` +
			`{"id":"d3","notes":[{"id":3,"body":"done","resolved":true,"author":{"username":"ana"}}]}]`},
	}}
	read, err := ReadLiveThreads(context.Background(), liveRequest(cmdr, "acme/app", "5101"), "gitlab", "", nil)
	if err != nil {
		t.Fatalf("ReadLiveThreads error: %v", err)
	}
	if read.Open != 2 || read.Malformed != 0 || read.Truncated || read.Stderr != "" {
		t.Errorf("read = %+v, want two open threads and a complete clean read", read)
	}
}

// TestReadLiveThreadsPartialWalkIsIncomplete pins the incomplete-read signal: a
// page walk that stopped on a failed page keeps the threads it read and reports
// the surface as unread, without turning a partial read into an error the
// caller could mistake for "no threads".
func TestReadLiveThreadsPartialWalkIsIncomplete(t *testing.T) {
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		cafeReviewCall(""): {stdout: `{"data":{"items":[{"id":"r1","body":"review",` +
			`"author":{"handle":"rev"}}]},"next":"c2"}`},
		cafeReviewCall("c2"): {stderr: "cafe: 500 reviews down", fail: true},
		cafeCommentCall:      {stdout: `{"data":{"items":[]}}`},
	}}
	read, err := ReadLiveThreads(context.Background(), liveRequest(cmdr, "o/r", "7"), "gitcafe", "", nil)
	if err != nil {
		t.Fatalf("ReadLiveThreads error: %v", err)
	}
	if !read.Truncated || read.Open != 1 {
		t.Errorf("read = %+v, want truncated with the one open review thread", read)
	}
}

// TestReadLiveThreadsUnknownProvider pins the refusal of a provider no adapter
// reads, instead of an empty read that would pass the dimension.
func TestReadLiveThreadsUnknownProvider(t *testing.T) {
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{}}
	_, err := ReadLiveThreads(context.Background(), liveRequest(cmdr, "o/r", "5101"), "gitea", "", nil)
	if err == nil || !strings.Contains(err.Error(), `unknown provider "gitea"`) {
		t.Fatalf("error = %v, want the unknown provider refusal", err)
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("calls = %q, want none", cmdr.calls)
	}
}

// bareCommander answers with Run alone: the SDK's default runner carries no
// PATH probe.
type bareCommander struct{}

func (bareCommander) Run(_ context.Context, _ *core.Command) (core.Result, error) {
	return core.Result{}, nil
}

// TestFetchCommanderAdapters pins the commander seam: a Run-only commander is
// wrapped so the adapter read still runs (its tool pre-check reports present),
// and a request with no commander stays nil instead of opening a session.
func TestFetchCommanderAdapters(t *testing.T) {
	wrapped := fetchCommander(core.ReadRequest{Runtime: &core.Runtime{Commander: bareCommander{}}})
	if wrapped == nil {
		t.Fatal("fetchCommander dropped a Run-only commander")
	}
	if err := wrapped.LookPath("glab"); err != nil {
		t.Errorf("LookPath = %v, want the wrapped commander to report the tool present", err)
	}
	if got := fetchCommander(core.ReadRequest{}); got != nil {
		t.Errorf("fetchCommander = %v without a runtime, want nil", got)
	}
	if got := fetchCommander(core.ReadRequest{Runtime: &core.Runtime{}}); got != nil {
		t.Errorf("fetchCommander = %v without a commander, want nil", got)
	}
}
