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
	st.comments = []any{projectComment(jq.NewObj("path", "a.go", "side", "RIGHT", "line", 10, "body", "broken"), "")}
	st.payloads = []string{postPayload(st.body, "COMMENT", st.commitID, st.comments)}
	st.phash = "cached"
	if err := writeTestFile(st.dir+"/.posted_cached", ""); err != nil {
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
