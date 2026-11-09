package postcore

import (
	"context"
	"slices"
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
		event    string
		wantGap  string
		wantCap  string
		wantPost string
		caps     core.HostCapabilities
	}{
		{name: "both verdicts carry", caps: both, event: eventApprove, wantPost: eventApprove},
		{name: "both request changes", caps: both, event: eventRequestChanges, wantPost: eventRequestChanges},
		{name: "comment is untouched", caps: core.HostCapabilities{}, event: eventComment, wantPost: eventComment},
		{
			name: "no approve posts comment", caps: changesOnly, event: eventApprove,
			wantGap: msgNoApproveEvent, wantCap: core.CapabilityApprove, wantPost: eventComment,
		},
		{
			name: "no changes keeps the request", caps: approveOnly, event: eventRequestChanges,
			wantGap: msgNoChangesEvent, wantCap: core.CapabilityRequestChanges, wantPost: eventRequestChanges,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gap := verdictGap(tc.caps, tc.event)
			if gap.message != tc.wantGap || gap.posted != tc.wantPost || gap.capability != tc.wantCap {
				t.Errorf("verdictGap = (%q, %q, %q), want (%q, %q, %q)",
					gap.capability, gap.message, gap.posted, tc.wantCap, tc.wantGap, tc.wantPost)
			}
		})
	}
}

// TestWithholdRecordsThePublishedReason proves the receipt records the cause
// the capability table publishes, not a second vocabulary: a withheld
// capability lands with its published code and the event the run asked for,
// one entry per capability however often a gate revisits it, and nothing at all
// for a capability the host carries.
func TestWithholdRecordsThePublishedReason(t *testing.T) {
	t.Parallel()
	caps := core.HostCapabilities{Provider: "approve-only", Fetch: true, Post: true, Approve: true, InlineAnchors: true}
	st := &state{}
	st.withhold(caps, core.CapabilityRequestChanges, eventRequestChanges)
	st.withhold(caps, core.CapabilityRequestChanges, eventRequestChanges)
	st.withhold(caps, core.CapabilityApprove, eventApprove)
	want := []core.WithheldCapability{
		{Capability: core.CapabilityRequestChanges, Reason: "no-request-changes-event", RequestedEvent: eventRequestChanges},
	}
	if !slices.Equal(st.withheld, want) {
		t.Errorf("withheld = %+v, want %+v", st.withheld, want)
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
