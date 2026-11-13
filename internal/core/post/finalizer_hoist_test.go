package postcore

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

const hoistHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func hoistPostRecord(t *testing.T) {
	t.Helper()
	if err := writeTestFile(".verdict/state/7/post.json",
		`{"head":"`+hoistHead+`","posted_at":"2026-01-01T00:00:00Z"}`); err != nil {
		t.Fatal(err)
	}
}

//nolint:revive // compare the same gate with publication evidence present and absent
func TestSuppressionDoesNotChangeGate(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, `{"schema_version":"1","body":"> ✅ Ship it.","approved":true,"comments":[]}`)
	if err := writeTestFile(".verdict/state/7/findings.json", `[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,`+
		`"anchored":true,"body":"Usable body here with enough substance.","suggestion":null,`+
		`"inBounds":true,"rigor":"walked","severity":"important",`+
		`"sig":"b222222222222222222222222222222222222222","sig_version":"1",`+
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]`); err != nil {
		t.Fatal(err)
	}
	hoistPostRecord(t)
	remote := projectComment(jq.NewObj("path", "notes.txt", "line", 2, "side", "RIGHT",
		"body", "Usable body here with enough substance."), "")
	remote.Set("commit_id", hoistHead)
	if err := writeTestFile(".verdict/state/7/inline.json", "["+remote.MarshalPretty()+"]"); err != nil {
		t.Fatal(err)
	}

	cmdr := postCommander(t)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{
		Approve: "true", Bind: true, ReviewFile: ".verdict/state/7/review.json",
	}, &warnings, &human)
	_, ref := Run(context.Background(), req)
	if ref == nil || ref.Code != "approve-blocked" {
		t.Fatalf("gate = %v, want approve-blocked", ref)
	}
	if err := os.Remove(".verdict/state/7/post.json"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(".verdict/state/7/inline.json"); err != nil {
		t.Fatal(err)
	}
	_, without := Run(context.Background(), req)
	if without == nil || without.Code != ref.Code {
		t.Fatalf("gate without suppression evidence = %v; with = %v", without, ref)
	}
}

func TestRunForceSameSHAFallsThrough(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, postReview)
	first := seedPostedOnce(t)
	cmdr := postCommander(t)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{
		Approve: "false", ReviewFile: ".verdict/state/7/review.json", Force: true,
	}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("forced repost refusal: %v", ref)
	}
	if resp.Data == nil || resp.Data.PayloadHash != first {
		t.Fatalf("forced repost data = %+v, want payload hash %s", resp.Data, first)
	}
	assertOneHoistedReview(t, cmdr)
}

func assertOneHoistedReview(t *testing.T, cmdr *scriptCommander) {
	t.Helper()
	posted := 0
	for i, call := range cmdr.calls {
		if !strings.Contains(strings.Join(call, " "), "--method POST") {
			continue
		}
		posted++
		payload := decodePostedPayload(t, cmdr.stdin[i])
		if payload["commit_id"] != hoistHead {
			t.Errorf("forced repost commit_id = %v, want %s", payload["commit_id"], hoistHead)
		}
	}
	if posted != 1 {
		t.Fatalf("forced repost reviews POSTs = %d, want 1", posted)
	}
}

func seedPostedOnce(t *testing.T) string {
	t.Helper()
	cmdr := postCommander(t)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("seed post refused: %v", ref)
	}
	if !exists(".verdict/state/7/post.json") {
		t.Fatal("seed post wrote no post.json")
	}
	if !exists(".verdict/state/7/.posted_" + resp.Data.PayloadHash) {
		t.Fatalf("seed post wrote no .posted_%s marker", resp.Data.PayloadHash)
	}
	return resp.Data.PayloadHash
}
