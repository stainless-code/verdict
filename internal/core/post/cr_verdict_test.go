package postcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

func crVerdictState(t *testing.T, event string) *state {
	t.Helper()
	var warnings []string
	st := postTestState(t.TempDir(), liveGuardHead, core.PostFlags{}, &warnings)
	st.event = event
	st.body = "A rendered review summary."
	st.buildPayloads()
	st.liveLoaded = true
	st.live = []liveReview{{body: st.body, commitID: st.commitID}}
	return st
}

func TestCRBindingVerdictSurvivesContentDedupe(t *testing.T) {
	t.Parallel()
	for _, event := range []string{"APPROVE", "REQUEST_CHANGES"} {
		st := crVerdictState(t, event)
		if ref := st.remoteDiff(); ref != nil {
			t.Fatalf("%s swallowed: %v", event, ref)
		}
		obj := decodePostedPayload(t, st.payloads[0])
		if obj["event"] != event {
			t.Fatalf("binding payload = %v", obj)
		}
		if _, has := obj["body"]; has {
			t.Fatal("binding retry duplicated summary")
		}
	}
}

func TestCRApprovalRequiresRemoteState(t *testing.T) {
	t.Parallel()
	for _, example := range []struct{ state, want string }{{"COMMENTED", ""}, {"", ""}, {"APPROVED", "duplicate-payload"}} {
		remoteState := example.state
		st := crVerdictState(t, "APPROVE")
		st.live = publicationReviews([]map[string]any{{"body": st.body, "commit_id": st.commitID, "state": remoteState}})
		ref := st.remoteDiff()
		got := ""
		if ref != nil {
			got = ref.Code
		}
		if got != example.want {
			t.Fatalf("state %s: reconciliation %s, want %s", remoteState, got, example.want)
		}
	}
}
