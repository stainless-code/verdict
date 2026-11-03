package postcore

import (
	"context"
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
