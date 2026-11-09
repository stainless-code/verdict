package postcore

import (
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestVerdictGap proves the capability table decides a binding verdict: a host
// that carries both events posts what was asked, and a host missing one
// degrades with the matching message. Every row flips a capability away from
// the shipped table, so a gate that stops consulting the flag fails here.
func TestVerdictGap(t *testing.T) {
	t.Parallel()
	both := core.HostCapabilities{Provider: "both", Fetch: true, Post: true, Approve: true, RequestChanges: true}
	approveOnly := core.HostCapabilities{Provider: "approve-only", Fetch: true, Post: true, Approve: true}
	changesOnly := core.HostCapabilities{Provider: "changes-only", Fetch: true, Post: true, RequestChanges: true}
	for _, tc := range []struct {
		name     string
		caps     core.HostCapabilities
		event    string
		wantGap  string
		wantPost string
	}{
		{name: "both verdicts carry", caps: both, event: eventApprove, wantPost: eventApprove},
		{name: "both request changes", caps: both, event: eventRequestChanges, wantPost: eventRequestChanges},
		{name: "comment is untouched", caps: core.HostCapabilities{}, event: eventComment, wantPost: eventComment},
		{
			name: "no approve posts comment", caps: changesOnly, event: eventApprove,
			wantGap: msgNoApproveEvent, wantPost: eventComment,
		},
		{
			name: "no changes keeps the request", caps: approveOnly, event: eventRequestChanges,
			wantGap: msgNoChangesEvent, wantPost: eventRequestChanges,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gap, posted := verdictGap(tc.caps, tc.event)
			if gap != tc.wantGap || posted != tc.wantPost {
				t.Errorf("verdictGap = (%q, %q), want (%q, %q)", gap, posted, tc.wantGap, tc.wantPost)
			}
		})
	}
}

// TestRunPostGitLabWarnsOnRequestChanges proves the capability reaches the
// operator: a bound request-changes review on GitLab keeps the requested event,
// posts the discussions and summary note, fires no approve call, and warns that
// the verdict did not bind. Turning gitlab's request_changes capability on
// would drop the warning and fail this test.
func TestRunPostGitLabWarnsOnRequestChanges(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGitLabBundle(t, providerGitLab, gitlabBundleReview)
	cmdr := gitlabCommander(t)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{
		Approve: "false", RequestChanges: true, Bind: true,
		ReviewFile: ".verdict/state/7/review.json",
	}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.Data.Event != eventRequestChanges {
		t.Errorf("event = %q, want %s", resp.Data.Event, eventRequestChanges)
	}
	if got := countCalls(cmdr, gitlabApproveArgv); got != 0 {
		t.Errorf("a request-changes review approved (%q)", callKeys(cmdr))
	}
	if got := countCalls(cmdr, gitlabDiscussionsArgv); got != 1 {
		t.Errorf("discussions = %d, want 1 (%q)", got, callKeys(cmdr))
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "GitLab has no request-changes review event") {
		t.Errorf("warnings = %q, want the capability gap named", warnings)
	}
	if n := strings.Count(joined, "request-changes review event"); n != 1 {
		t.Errorf("the gap is warned %d times, want 1: %q", n, warnings)
	}
}
