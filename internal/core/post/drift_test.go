package postcore

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

const driftLive = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

const liveSHAArgv = "gh\x00api\x00repos/acme/app/pulls/7\x00--jq\x00.head.sha"

// postBundleCommander scripts the post happy path with a chosen live head, so
// a leg can move the branch (or fail the lookup) without touching the rest.
func postBundleCommander(t *testing.T, live *string) *scriptCommander {
	t.Helper()
	cmdr := postCommander(t)
	if live == nil {
		cmdr.out[liveSHAArgv] = scriptResult{stderr: "HTTP 404: Not Found\n", fail: true}
		return cmdr
	}
	cmdr.out[liveSHAArgv] = scriptResult{stdout: *live + "\n"}
	return cmdr
}

// driftNotePrefix is the body note a moved HEAD prepends, byte for byte as
// docs/manual.md's stale-HEAD clause renders it.
const driftNotePrefix = "> ⚠️ Note: commits landed after this review's fetch " +
	"(fetched `aaaaaaa`, live `bbbbbbb`); treat line anchors as potentially stale. " +
	"Run `verdict fetch acme/app 7 --base-sha aaaaaaa` and review the delta.\n\n"

const staleNotePrefix = "> ⚠️ Note: posting as COMMENT instead of APPROVE — commits " +
	"landed after this review's fetch, so the review does not cover live HEAD; " +
	"run `verdict fetch acme/app 7 --base-sha aaaaaaa` and review the delta."

// TestRunDriftKeepsPinnedSnapshot proves a push after fetch only adds drift
// context: the anchors still validate against the fetch-time hunks (the
// comment stays inline), the warning names both heads and the delta command,
// the body note leads the posted body, and the marker stays the last line
// after the footer.
//
//nolint:revive // legs assert the warning, body, and posted payload inline
func TestRunDriftKeepsPinnedSnapshot(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, postReview)
	live := driftLive
	cmdr := postBundleCommander(t, &live)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: ".review/7/review.json"}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	want := "HEAD moved since fetch (fetched aaaaaaa, live bbbbbbb) — anchors potentially stale; " +
		"run verdict fetch acme/app 7 --base-sha aaaaaaa and review the delta"
	if !strings.Contains(strings.Join(resp.Warnings, "\n"), want) {
		t.Errorf("warnings = %q, want the drift line %q", resp.Warnings, want)
	}
	if resp.Data.Anchors.Kept != 1 || resp.Data.Anchors.Moved != 0 {
		t.Errorf("anchors = %+v, want the pinned snapshot's one inline anchor kept", resp.Data.Anchors)
	}
	body := postedBody(t, cmdr)
	if !strings.HasPrefix(body, driftNotePrefix) {
		t.Errorf("posted body does not lead with the drift note:\n%.400q", body)
	}
	if !strings.Contains(body, "> ✅ Looks good — minor note below.") {
		t.Errorf("posted body lost the review prose")
	}
	markerIdx := strings.LastIndex(body, "<!-- verdict-review payload=")
	footerIdx := strings.Index(body, core.FooterDivider)
	if footerIdx < 0 || markerIdx < 0 {
		t.Fatalf("posted body misses the footer divider (%d) or the marker (%d)", footerIdx, markerIdx)
	}
	if footerIdx > markerIdx {
		t.Errorf("footer divider at %d comes after the marker at %d", footerIdx, markerIdx)
	}
	if !strings.HasSuffix(body, " -->") {
		t.Errorf("hidden marker is not the posted body's last line: %.80q", body[len(body)-80:])
	}
}

// TestRunNoDriftIsQuiet proves the equal-SHA path adds nothing: no drift
// warning, no drift note, and no false "lookup failed" warning.
func TestRunNoDriftIsQuiet(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, postReview)
	live := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cmdr := postBundleCommander(t, &live)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: ".review/7/review.json"}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if joined := strings.Join(resp.Warnings, "\n"); strings.Contains(joined, "HEAD moved") || strings.Contains(joined, "live SHA lookup failed") {
		t.Errorf("equal-SHA run warned %q, want neither drift line", joined)
	}
	if body := postedBody(t, cmdr); !strings.HasPrefix(body, "> ✅ Looks good — minor note below.") {
		t.Errorf("equal-SHA body was annotated:\n%.200q", body)
	}
}

// TestRunDriftLookupFailed proves an unreadable live head degrades to the
// documented warning instead of a drift note or a refusal.
func TestRunDriftLookupFailed(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, postReview)
	cmdr := postBundleCommander(t, nil)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: ".review/7/review.json"}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal on a failed lookup: %v", ref)
	}
	joined := strings.Join(resp.Warnings, "\n")
	if !strings.Contains(joined, "live SHA lookup failed — proceeding on fetched hunks") {
		t.Errorf("warnings = %q, want the degraded-lookup notice", joined)
	}
	if strings.Contains(joined, "HEAD moved since fetch") {
		t.Errorf("warnings = %q, want no drift line without a live head", joined)
	}
	if body := postedBody(t, cmdr); strings.Contains(body, "commits landed after this review's fetch") {
		t.Errorf("body carries a drift note for a failed lookup:\n%.200q", body)
	}
}

// TestRunStaleApproveDowngrades proves a binding APPROVE on a moved HEAD
// never lands as APPROVE: it posts as COMMENT with the stale warning and the
// two-line body note.
//
//nolint:revive // legs assert the warning, body, and posted payload inline
func TestRunStaleApproveDowngrades(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	postBundle(t, `{"schema_version":"1","body":"> ✅ Ship it.","approved":true,"comments":[]}`)
	live := driftLive
	cmdr := postBundleCommander(t, &live)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{
		Approve: "true", Bind: true, ReviewFile: ".review/7/review.json",
	}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.Data.Event != eventComment {
		t.Errorf("event = %q, want COMMENT (a stale review never binds)", resp.Data.Event)
	}
	want := "stale fetch — APPROVE downgraded to COMMENT (review covers fetched aaaaaaa, live HEAD is bbbbbbb; " +
		"run verdict fetch acme/app 7 --base-sha aaaaaaa and review the delta to bind)"
	if !strings.Contains(strings.Join(resp.Warnings, "\n"), want) {
		t.Errorf("warnings = %q, want %q", resp.Warnings, want)
	}
	body := postedBody(t, cmdr)
	if !strings.HasPrefix(body, staleNotePrefix) {
		t.Fatalf("downgrade note does not front the body:\n%.300q", body)
	}
	driftAt := strings.Index(body, driftNotePrefix)
	if driftAt < len(staleNotePrefix) {
		t.Fatalf("drift note (at %d) does not follow the downgrade note:\n%.600q", driftAt, body)
	}
	if body[driftAt-2:driftAt] != "\n\n" || body[driftAt-3] == '\n' {
		t.Errorf("downgrade and drift notes are not separated by exactly one blank line:\n%.600q", body)
	}
	if strings.Contains(body[:driftAt], `\n`) {
		t.Errorf("downgrade note carries a literal backslash-n:\n%.600q", body)
	}
	if !strings.Contains(body[driftAt:], "> ✅ Ship it.") {
		t.Errorf("review prose does not follow the drift note:\n%.600q", body)
	}
	if payload := postedPayloads(t, cmdr); len(payload) != 1 || payload[0]["event"] != eventComment {
		t.Errorf("posted payloads = %v, want one COMMENT review", payload)
	}
}

// postedBody returns the first reviews-POST stdin body decoded from the
// scripted commander.
func postedBody(t *testing.T, cmdr *scriptCommander) string {
	t.Helper()
	payloads := postedPayloads(t, cmdr)
	if len(payloads) == 0 {
		t.Fatal("no reviews POST fired")
	}
	return payloadBody(t, payloads[0])
}

// payloadBody returns one decoded payload's body, failing the test when it
// is not a string.
func payloadBody(t *testing.T, payload map[string]any) string {
	t.Helper()
	body, ok := payload["body"].(string)
	if !ok {
		t.Fatalf("posted body is not a string: %v", payload["body"])
	}
	return body
}

// postedPayloads decodes every reviews-POST stdin body.
func postedPayloads(t *testing.T, cmdr *scriptCommander) []map[string]any {
	t.Helper()
	var out []map[string]any
	for i, call := range cmdr.calls {
		if !strings.Contains(strings.Join(call, " "), "/reviews") {
			continue
		}
		out = append(out, decodePostedPayload(t, cmdr.stdin[i]))
	}
	return out
}

// TestRunRecheckOnlyOnApprove proves the live-thread recheck is an APPROVE
// path: a COMMENT post and a dry run spawn no thread read at all.
func TestRunRecheckOnlyOnApprove(t *testing.T) {
	t.Run("comment-post", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		postBundle(t, postReview)
		live := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		cmdr := postBundleCommander(t, &live)
		var warnings, human []string
		req := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: ".review/7/review.json"}, &warnings, &human)
		if _, ref := Run(context.Background(), req); ref != nil {
			t.Fatalf("Run refusal: %v", ref)
		}
		assertNoThreadRead(t, cmdr, "COMMENT post")
	})
	t.Run("dry-run-approve", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		postBundle(t, `{"schema_version":"1","body":"> ✅ Ship it.","approved":true,"comments":[]}`)
		live := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		cmdr := postBundleCommander(t, &live)
		cmdr.out["gh\x00api\x00user\x00--jq\x00.login"] = scriptResult{stdout: "reviewer-bot\n"}
		var warnings, human []string
		req := postTestReq(cmdr, core.PostFlags{
			Approve: "true", Bind: true, ReviewFile: ".review/7/review.json", DryRun: true,
		}, &warnings, &human)
		resp, ref := Run(context.Background(), req)
		if ref != nil {
			t.Fatalf("Run refusal: %v", ref)
		}
		if resp.Data.Event != "APPROVE" {
			t.Fatalf("event = %q, want the APPROVE scenario this leg guards", resp.Data.Event)
		}
		assertNoThreadRead(t, cmdr, "dry run")
	})
}

// assertNoThreadRead fails when any scripted call was the live-thread read.
func assertNoThreadRead(t *testing.T, cmdr *scriptCommander, who string) {
	t.Helper()
	for _, call := range cmdr.calls {
		if strings.Contains(strings.Join(call, " "), "graphql") {
			t.Fatalf("%s spawned the live-thread read: %q", who, call)
		}
	}
}

// TestRunApproveLiveThreadBoundary proves the live-approve gate refuses only
// a surplus of live open threads: zero local and zero live binds, one live
// thread the store does not know refuses.
//
//nolint:gocognit,revive // legs assert the warning, body, and posted payload inline
func TestRunApproveLiveThreadBoundary(t *testing.T) {
	threadsArgvKey := "gh\x00api\x00graphql\x00--paginate\x00-F\x00owner=acme\x00-F\x00name=app\x00-F\x00pr=7\x00-f\x00query=" + queryLiveCount
	nodes := func(n int) string {
		var b strings.Builder
		b.WriteString(`{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[`)
		for i := range n {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`{"isResolved":false}`)
		}
		b.WriteString(`]}}}}}`)
		return b.String()
	}
	for _, tc := range []struct {
		name    string
		wantMsg string
		live    int
		wantRef bool
	}{
		{name: "no-live-threads-binds", live: 0},
		{
			name: "one-live-thread-refuses", live: 1, wantRef: true,
			wantMsg: "refusing APPROVE: 1 live open thread(s) vs 0 local open finding(s) — " +
				"1 thread(s) the store does not know about",
		},
		{
			name: "two-live-threads-refuses", live: 2, wantRef: true,
			wantMsg: "refusing APPROVE: 2 live open thread(s) vs 0 local open finding(s) — " +
				"2 thread(s) the store does not know about",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			postBundle(t, `{"schema_version":"1","body":"> ✅ Ship it.","approved":true,"comments":[]}`)
			live := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			cmdr := postBundleCommander(t, &live)
			cmdr.out[threadsArgvKey] = scriptResult{stdout: nodes(tc.live)}
			cmdr.out["gh\x00api\x00user\x00--jq\x00.login"] = scriptResult{stdout: "reviewer-bot\n"}
			var warnings, human []string
			req := postTestReq(cmdr, core.PostFlags{
				Approve: "true", Bind: true, ReviewFile: ".review/7/review.json",
			}, &warnings, &human)
			resp, ref := Run(context.Background(), req)
			if !tc.wantRef {
				if ref != nil {
					t.Fatalf("no live threads refused APPROVE: %v", ref)
				}
				if resp.Data.Event != "APPROVE" {
					t.Errorf("event = %q, want APPROVE", resp.Data.Event)
				}
				return
			}
			if ref == nil {
				t.Fatalf("%d surplus live thread(s) proceeded to APPROVE", tc.live)
			}
			if ref.Code != "approve-blocked" || !strings.Contains(ref.Message, tc.wantMsg) {
				t.Errorf("refusal = %q %q, want approve-blocked naming the surplus", ref.Code, ref.Message)
			}
		})
	}
}

// pinnedHunkRow renders one side-aware range row in the fetch-time snapshot
// format (`file<TAB>side<TAB>start<TAB>end`).
func pinnedHunkRow(side string, start, end int) string {
	return fmt.Sprintf("notes.txt\t%s\t%d\t%d\n", side, start, end)
}

// TestRunAnchorsUsePinnedSnapshot proves post validates against the
// fetch-time hunks.tsv, not the full diff it sits beside: the same comment is
// inline under a snapshot that covers its line and moves into the body under
// a snapshot that does not, with the snapshot left byte-identical.
//
//nolint:gocognit,revive,cyclop // legs assert the warning, body, and posted payload inline
func TestRunAnchorsUsePinnedSnapshot(t *testing.T) {
	cases := []struct {
		name     string
		snapshot string
		wantBody string
		wantKept int
	}{
		{
			name:     "pinned-covers-the-line",
			snapshot: pinnedHunkRow("OLD", 1, 3) + pinnedHunkRow("NEW", 1, 3),
			wantKept: 1,
		},
		{
			name:     "pinned-excludes-the-line",
			snapshot: pinnedHunkRow("OLD", 10, 12) + pinnedHunkRow("NEW", 10, 12),
			wantKept: 0,
			wantBody: "### ℹ️ Unanchored observations",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			postBundle(t, postReview)
			if err := writeTestFile(".review/7/hunks.tsv", tc.snapshot); err != nil {
				t.Fatal(err)
			}
			live := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			cmdr := postBundleCommander(t, &live)
			var warnings, human []string
			req := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: ".review/7/review.json"}, &warnings, &human)
			resp, ref := Run(context.Background(), req)
			if ref != nil {
				t.Fatalf("Run refusal: %v", ref)
			}
			if resp.Data.Anchors.Kept != tc.wantKept {
				t.Errorf("anchors = %+v, want %d kept", resp.Data.Anchors, tc.wantKept)
			}
			if got := readTestFile(t, ".review/7/hunks.tsv"); got != tc.snapshot {
				t.Errorf("pinned snapshot was rewritten:\n%q", got)
			}
			body := postedBody(t, cmdr)
			if tc.wantBody != "" && !strings.Contains(body, tc.wantBody) {
				t.Errorf("posted body misses %q:\n%.600q", tc.wantBody, body)
			}
			if tc.wantBody != "" && !strings.Contains(body, "anchor 2-2 (RIGHT) outside reviewed hunks") {
				t.Errorf("posted body misses the moved-anchor reason:\n%.600q", body)
			}
			if tc.wantKept == 1 {
				if strings.Contains(body, "Unanchored observations") {
					t.Errorf("covering snapshot moved the anchor:\n%.600q", body)
				}
				for _, p := range postedPayloads(t, cmdr) {
					comments, ok := p["comments"].([]any)
					if !ok || len(comments) != 1 {
						t.Fatalf("posted comments = %v, want the one inline comment", p["comments"])
					}
				}
			}
			if joined := strings.Join(resp.Warnings, "\n"); strings.Contains(joined, "rebuilding from full.diff") {
				t.Errorf("warnings = %q, want no rebuild notice for a pinned snapshot", joined)
			}
		})
	}
}

// readTestFile reads a fixture file the test wrote.
func readTestFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}
