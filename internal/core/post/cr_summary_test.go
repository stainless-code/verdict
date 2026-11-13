package postcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

func TestCROmittedGitLabSummary(t *testing.T) {
	t.Parallel()
	var warnings []string
	st := postTestState(t.TempDir(), gitlabHead, core.PostFlags{}, &warnings)
	st.body = "A rendered summary already published."
	st.event = "COMMENT"
	st.merged.Kept = []any{jq.NewObj("path", "notes.txt", "side", "RIGHT", "line", 2, "body", "Missing inline finding.")}
	st.buildPayloads()
	st.liveLoaded = true
	st.live = []liveReview{{body: st.body, commitID: st.commitID}}
	if ref := st.remoteDiff(); ref != nil {
		t.Fatal(ref)
	}
	host := gitlabCommander(t)
	result := postOnce(t.Context(), host, "gitlab", nil, "acme/app", "7", st.dir, st.payloads[0])
	if !result.ok {
		t.Fatalf("inline-only submission failed: %+v", result)
	}
	if got := countCalls(host, gitlabNotesArgv); got != 0 {
		t.Fatalf("omitted summary sent %d notes, want 0", got)
	}
	if got := countCalls(host, gitlabDiscussionsArgv); got != 1 {
		t.Fatalf("inline writes %d, want 1", got)
	}
	doc, err := jq.Decode([]byte(result.posted))
	m, ok := doc.(map[string]any)
	if err != nil || !ok || jq.Interp(m["id"]) != "7" {
		t.Fatalf("inline-only receipt = %v, %v", doc, err)
	}
}

func TestCRMalformedNotesFailClosed(t *testing.T) {
	for _, raw := range []string{`[{"notes":[null]}]`, `[{"notes":null}]`, `[{"notes":"bad"}]`, `[{"notes":[{"notes":[42]}]}]`} {
		t.Run(raw, func(t *testing.T) {
			t.Chdir(t.TempDir())
			writeGitLabBundle(t, "gitlab", gitlabBundleReview)
			host := &failingDiscussionHost{readBody: raw}
			var warnings, human []string
			req := postTestReq(host, core.PostFlags{Approve: "false", Force: true, ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
			if _, ref := Run(t.Context(), req); ref == nil || ref.Code != codePostFailed {
				t.Fatalf("malformed nested notes accepted: %v", ref)
			}
			if host.attempts != 0 {
				t.Fatalf("malformed notes caused %d writes", host.attempts)
			}
		})
	}
}
