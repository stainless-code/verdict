package postcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

func TestRemoteAuthority(t *testing.T) {
	t.Parallel()
	var warnings []string
	st := postTestState(t.TempDir(), liveGuardHead, core.PostFlags{}, &warnings)
	st.body = "summary"
	st.merged.Kept = []any{jq.NewObj("path", "a.go", "side", "RIGHT", "line", 10, "body", "broken")}
	st.event = "COMMENT"
	st.buildPayloads()
	if err := writeTestFile(st.dir+"/.posted_"+st.phash, ""); err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile(st.dir+"/reviews.json", "[]"); err != nil {
		t.Fatal(err)
	}
	if ref := st.remoteDiff(); ref != nil {
		t.Fatalf("empty remote overruled by marker: %v", ref)
	}
	if len(st.comments) != 1 {
		t.Fatal("empty remote suppressed comment")
	}
}

func TestRemoteMissingOnlyAndRearm(t *testing.T) {
	t.Parallel()
	var warnings []string
	st := postTestState(t.TempDir(), liveGuardHead, core.PostFlags{}, &warnings)
	first := projectComment(jq.NewObj("path", "a.go", "side", "RIGHT", "line", 10, "body", "broken"), "")
	second := projectComment(jq.NewObj("path", "b.go", "side", "RIGHT", "line", 20, "body", "another bug"), "")
	st.comments = []any{first, second}
	st.payloads = []string{postPayload("summary", "COMMENT", st.commitID, st.comments)}
	first.Set("commit_id", liveGuardHead)
	if err := writeTestFile(st.dir+"/inline.json", "["+first.MarshalPretty()+"]"); err != nil {
		t.Fatal(err)
	}
	if ref := st.remoteDiff(); ref != nil {
		t.Fatal(ref)
	}
	obj, ok := st.comments[0].(*jq.Obj)
	if len(st.comments) != 1 || !ok || orderedString(obj, "path") != "b.go" {
		t.Fatalf("missing comments = %v", st.comments)
	}
	st.commitID = liveGuardOther
	st.comments = []any{first, second}
	if ref := st.remoteDiff(); ref != nil {
		t.Fatal(ref)
	}
	if len(st.comments) != 2 || len(st.forced) != 1 || st.forced[0].Reason != "head-rearmed" {
		t.Fatalf("new head did not record rearm: %v", st.forced)
	}
}

//nolint:revive // the envelope and persisted receipt are asserted in the same publication
func TestRearmRecorded(t *testing.T) {
	t.Chdir(t.TempDir())
	writeGitLabBundle(t, "gitlab", gitlabBundleReview)
	prior := projectComment(jq.NewObj("path", "notes.txt", "side", "RIGHT", "line", 2,
		"body", "Consider clarifying this line."), "")
	prior.Set("commit_id", liveGuardOther)
	if err := writeTestFile(".verdict/state/7/inline.json", "["+prior.MarshalPretty()+"]"); err != nil {
		t.Fatal(err)
	}
	var warnings, human []string
	req := postTestReq(gitlabCommander(t), core.PostFlags{Approve: "false", ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
	response, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	if !response.Data.Rearmed || response.Data.PreviousHead != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("rearm output = %+v", response.Data)
	}
	raw, err := jq.CappedFile(".verdict/state/7/post.json", jq.MaxPRBytes)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := doc.(map[string]any)
	if !ok || m["rearmed"] != true || m["previous_head"] != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("rearm receipt = %v", doc)
	}
}
