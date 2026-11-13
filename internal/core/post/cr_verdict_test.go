package postcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
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
		doc, err := jq.DecodeOrdered([]byte(st.payloads[0]))
		obj, ok := doc.(*jq.Obj)
		if err != nil || !ok || orderedString(obj, "event") != event {
			t.Fatalf("binding payload = %v, %v", doc, err)
		}
		if orderedString(obj, "body") != "" {
			t.Fatal("binding retry duplicated summary")
		}
	}
}
