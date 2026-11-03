package postcore

import (
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// hoistHead is the reviewed SHA the minimal bundle records.
const hoistHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// hoistPostRecord writes a completed-post record for hoistHead.
func hoistPostRecord(t *testing.T) {
	t.Helper()
	if err := writeTestFile(".review/7/post.json",
		`{"head":"`+hoistHead+`","posted_at":"2026-01-01T00:00:00Z"}`); err != nil {
		t.Fatal(err)
	}
}

// TestRunAlreadyPostedZeroNetwork proves the same-SHA finalizer guard runs
// before drift's live lookup: a repeat post refuses typed with zero Commander
// calls and zero warnings from the skipped network reads (BL05).
func TestRunAlreadyPostedZeroNetwork(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, `{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,"comments":[]}`)
	hoistPostRecord(t)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{}}
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: ".review/7/review.json"}, &warnings, &human)
	_, ref := Run(context.Background(), req)
	assertAlreadyPostedZeroCalls(t, ref, cmdr, warnings)
}

// TestRunLiveAlreadyPostedZeroNetwork proves the fetched-reviews leg keeps
// the same hoist: a fresh bundle (no post.json) refuses from reviews.json
// before drift's live lookup, with zero Commander calls (BL05).
func TestRunLiveAlreadyPostedZeroNetwork(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, `{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,"comments":[]}`)
	writeLiveReview(t, ".review/7", markedReview(hoistHead), hoistHead)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{}}
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: ".review/7/review.json"}, &warnings, &human)
	_, ref := Run(context.Background(), req)
	assertAlreadyPostedZeroCalls(t, ref, cmdr, warnings)
}

// TestRunAlreadyPostedBeatsApproveBlocked proves the hoist's precedence: with
// an open finding and a binding APPROVE, the local finalizer refusal wins
// over approve-blocked, still with zero Commander calls (and therefore before
// the live-thread recheck that sets up approve-blocked-live).
func TestRunAlreadyPostedBeatsApproveBlocked(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, `{"schema_version":"1","body":"> ✅ Ship it.","approved":true,"comments":[]}`)
	if err := writeTestFile(".review/7/findings.json", `[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,`+
		`"anchored":true,"body":"Usable body here with enough substance.","suggestion":null,`+
		`"inBounds":true,"rigor":"walked","severity":"important",`+
		`"sig":"b222222222222222222222222222222222222222","sig_version":"1",`+
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]`); err != nil {
		t.Fatal(err)
	}
	hoistPostRecord(t)
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{}}
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{
		Approve: "true", Bind: true, ReviewFile: ".review/7/review.json",
	}, &warnings, &human)
	_, ref := Run(context.Background(), req)
	assertAlreadyPostedZeroCalls(t, ref, cmdr, warnings)
}

// assertAlreadyPostedZeroCalls proves the typed already-posted refusal and
// that no network read (or its warning) ran first.
func assertAlreadyPostedZeroCalls(t *testing.T, ref *core.RefusalError, cmdr *scriptCommander, warnings []string) {
	t.Helper()
	if ref == nil {
		t.Fatal("already-posted repost proceeded")
	}
	if ref.Code != "already-posted" {
		t.Fatalf("code = %q, want already-posted (message: %s)", ref.Code, ref.Message)
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("repost made %d Commander call(s): %q", len(cmdr.calls), cmdr.calls)
	}
	if len(warnings) != 0 {
		t.Errorf("skipped-network warnings = %q, want none", warnings)
	}
}

// hoistBadSchemaReview is a same-SHA bundle whose review.json merge would
// refuse as bad-review-schema (unknown schema_version).
const hoistBadSchemaReview = `{"schema_version":"9","body":"> ✅ Looks good — minor note below.","approved":false,"comments":[]}`

// TestRunSameSHAPrecedence pins the hoist's refusal precedence over the two
// later gates: the merge's schema validation and duplicateGuard's
// duplicate-payload marker. A same-SHA repeat wins both with zero Commander
// calls, so the hoist's zero-network property survives the precedence flip.
func TestRunSameSHAPrecedence(t *testing.T) {
	cases := []struct {
		name   string
		review string
		marker bool // mint a completed post first (post.json + .posted_<hash>)
	}{
		{name: "invalid-schema-masks-validation", review: hoistBadSchemaReview},
		{name: "store-marker-masks-duplicate-payload", review: postReview, marker: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			postBundle(t, tc.review)
			if tc.marker {
				seedPostedOnce(t)
			} else {
				hoistPostRecord(t)
			}
			// An empty script proves the refusal precedes every forge read.
			cmdr := &scriptCommander{t: t, out: map[string]scriptResult{}}
			var warnings, human []string
			req := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: ".review/7/review.json"}, &warnings, &human)
			_, ref := Run(context.Background(), req)
			assertAlreadyPostedZeroCalls(t, ref, cmdr, warnings)
		})
	}
}

// TestRunForceSameSHAFallsThrough proves --force bypasses both same-SHA
// guards: the bundle that just refused as already-posted validates and posts
// again, with the identical payload, through the full merge tail.
func TestRunForceSameSHAFallsThrough(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, postReview)
	first := seedPostedOnce(t)
	cmdr := postCommander(t, nil)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{
		Approve: "false", ReviewFile: ".review/7/review.json", Force: true,
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

// assertOneHoistedReview requires exactly one reviews POST, carrying the
// hoisted commit id.
func assertOneHoistedReview(t *testing.T, cmdr *scriptCommander) {
	t.Helper()
	posted := 0
	for i, call := range cmdr.calls {
		if !strings.Contains(strings.Join(call, " "), "/reviews") {
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

// seedPostedOnce runs the full post path once, minting post.json and the
// duplicate-payload marker, and returns the payload hash.
func seedPostedOnce(t *testing.T) string {
	t.Helper()
	cmdr := postCommander(t, nil)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: ".review/7/review.json"}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("seed post refused: %v", ref)
	}
	if !exists(".review/7/post.json") {
		t.Fatal("seed post wrote no post.json")
	}
	if !exists(".review/7/.posted_" + resp.Data.PayloadHash) {
		t.Fatalf("seed post wrote no .posted_%s marker", resp.Data.PayloadHash)
	}
	return resp.Data.PayloadHash
}
