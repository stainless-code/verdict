package postcore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/core"
	threadscore "github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	summaryTestHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	summaryTestBase = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func seedSummaryBundle(t *testing.T, provider string) {
	t.Helper()
	dir := ".verdict/state/7"
	var prJSON string
	switch provider {
	case providerGitHub:
		prJSON = `{"state":"OPEN","headRefOid":"` + summaryTestHead + `","baseRefOid":"` + summaryTestBase + `","author":{"login":"other-author"}}`
	case providerGitLab:
		prJSON = `{"provider":"gitlab","state":"open","head_sha":"` + summaryTestHead + `","base_sha":"` + summaryTestBase + `","author":"other-author"}`
	case providerForgejo:
		prJSON = `{"provider":"forgejo","number":7,"state":"open","head_sha":"` + summaryTestHead + `","base_sha":"` + summaryTestBase + `","author":"other-author"}`
	default:
		t.Fatalf("unknown provider %q", provider)
	}
	diff := "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n" +
		"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"
	if err := writeTestFile(dir+"/PR.json", prJSON); err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile(dir+"/full.diff", diff); err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile(dir+"/review.json", postReview); err != nil {
		t.Fatal(err)
	}
	seedPostControls(t, dir, summaryTestBase)
}

func summaryReq(cmdr Commander, provider string, flags core.PostFlags, warnings, human *[]string) *core.PostRequest {
	req := postTestReq(cmdr, flags, warnings, human)
	if provider == providerForgejo {
		req.Env = forgejoEnv()
	}
	return req
}

func hostBaseCommander(t *testing.T, provider, listJSON, inlineJSON string) *scriptCommander {
	t.Helper()
	switch provider {
	case providerGitHub:
		cmdr := postCommander(t)
		cmdr.out["gh\x00api\x00repos/acme/app/issues/7/comments\x00--paginate"] = scriptResult{stdout: listJSON}
		if inlineJSON != "" {
			cmdr.out["gh\x00api\x00repos/acme/app/pulls/7/reviews/101/comments\x00--paginate"] = scriptResult{stdout: inlineJSON}
		}
		cmdr.out["gh\x00api\x00repos/acme/app/issues/7/comments\x00--method\x00POST\x00--input\x00-"] = scriptResult{
			stdout: `{"id":501,"html_url":"https://gh.example/acme/app/pull/7#issuecomment-501","user":{"login":"someone-else"},"body":"created"}`,
		}
		cmdr.out["gh\x00api\x00repos/acme/app/issues/comments/401\x00--method\x00PATCH\x00--input\x00-"] = scriptResult{
			stdout: `{"id":401,"html_url":"https://gh.example/acme/app/pull/7#issuecomment-401","user":{"login":"someone-else"},"body":"updated"}`,
		}
		return cmdr
	case providerGitLab:
		mrJSON := `{"iid":7,"state":"opened","web_url":"https://gl.example/acme/app/-/merge_requests/7",` +
			`"diff_refs":{"base_sha":"` + summaryTestBase + `","start_sha":"` + summaryTestBase + `","head_sha":"` + summaryTestHead + `"},` +
			`"sha":"` + summaryTestHead + `"}`
		discResp := `{"id":"disc-1","notes":[{"id":901}]}`
		out := map[string]scriptResult{
			gitlabPRArgv:          {stdout: mrJSON},
			gitlabRecheckArgv:     {stdout: `[]`},
			gitlabUserArgv:        {stdout: `{"username":"someone-else"}`},
			gitlabDiscussionsArgv: {stdout: discResp},
			gitlabNotesArgv:       {stdout: `{"id":4242,"created_at":"2026-01-01T00:00:00Z"}`},
			gitlabPRArgv + "/notes?per_page=100\x00--paginate": {stdout: listJSON},
			gitlabPRArgv + "/notes/401\x00--method\x00PUT\x00" + gitlabJSONHeader + "\x00--input\x00-": {
				stdout: `{"id":401,"author":{"username":"someone-else"},"body":"updated"}`,
			},
		}
		return &scriptCommander{t: t, out: out}
	case providerForgejo:
		var inlinePage1 []string
		for i := range 50 {
			inlinePage1 = append(inlinePage1, fmt.Sprintf(`{"id":%d,"path":"other.txt","position":%d,"body":"noise %d","html_url":"https://fj.example/acme/app/pulls/7#issuecomment-%d"}`, 200+i, i+1, i, 200+i))
		}
		fjCreateRoute := "curl\x00--config\x00" + configPlaceholder + "\x00-sS\x00-i\x00" +
			"-H\x00Content-Type: application/json\x00-X\x00POST\x00--data-binary\x00@-\x00" +
			"https://forgejo.invalid/api/v1/repos/acme/app/issues/7/comments"
		fjPatchRoute := "curl\x00--config\x00" + configPlaceholder + "\x00-sS\x00-i\x00" +
			"-H\x00Content-Type: application/json\x00-X\x00PATCH\x00--data-binary\x00@-\x00" +
			"https://forgejo.invalid/api/v1/repos/acme/app/issues/comments/401"
		out := map[string]scriptResult{
			forgejoReadPrefix + "https://forgejo.invalid/api/v1/repos/acme/app/pulls/7": {
				stdout: `{"head":{"sha":"` + summaryTestHead + `"}}`,
			},
			forgejoUserRoute: {stdout: `{"login":"someone-else"}`},
			forgejoReadPrefix + "https://forgejo.invalid/api/v1/repos/acme/app/pulls/7/reviews?limit=50&page=1": {
				stdout: `[]`,
			},
			forgejoWriteRoute: {
				stdout: forgejoAnswer("200 OK", `{"id":901,"state":"COMMENT","html_url":"https://fj.example/acme/app/pulls/7#pullrequestreview-901","submitted_at":"2026-01-01T00:00:00Z"}`),
			},
			forgejoReadPrefix + "https://forgejo.invalid/api/v1/repos/acme/app/pulls/7/reviews/901/comments?limit=50&page=1": {
				stdout: "[" + strings.Join(inlinePage1, ",") + "]",
			},
			forgejoReadPrefix + "https://forgejo.invalid/api/v1/repos/acme/app/pulls/7/reviews/901/comments?limit=50&page=2": {
				stdout: inlineJSON,
			},
			forgejoReadPrefix + "https://forgejo.invalid/api/v1/repos/acme/app/issues/7/comments?limit=50&page=1": {
				stdout: listJSON,
			},
			fjCreateRoute: {
				stdout: forgejoAnswer("201 Created", `{"id":501,"html_url":"https://fj.example/acme/app/issues/7#issuecomment-501","user":{"login":"someone-else"},"body":"created"}`),
			},
			fjPatchRoute: {
				stdout: forgejoAnswer("200 OK", `{"id":401,"html_url":"https://fj.example/acme/app/issues/7#issuecomment-401","user":{"login":"someone-else"},"body":"updated"}`),
			},
		}
		return &scriptCommander{t: t, out: out}
	default:
		t.Fatalf("unknown provider %q", provider)
		return nil
	}
}

func expectedCreateRoute(provider string) string {
	switch provider {
	case providerGitHub:
		return "gh\x00api\x00repos/acme/app/issues/7/comments\x00--method\x00POST\x00--input\x00-"
	case providerGitLab:
		return gitlabNotesArgv
	case providerForgejo:
		return "curl\x00--config\x00" + configPlaceholder + "\x00-sS\x00-i\x00" +
			"-H\x00Content-Type: application/json\x00-X\x00POST\x00--data-binary\x00@-\x00" +
			"https://forgejo.invalid/api/v1/repos/acme/app/issues/7/comments"
	default:
		return ""
	}
}

func expectedUpdateRoute(provider string) string {
	switch provider {
	case providerGitHub:
		return "gh\x00api\x00repos/acme/app/issues/comments/401\x00--method\x00PATCH\x00--input\x00-"
	case providerGitLab:
		return gitlabPRArgv + "/notes/401\x00--method\x00PUT\x00" + gitlabJSONHeader + "\x00--input\x00-"
	case providerForgejo:
		return "curl\x00--config\x00" + configPlaceholder + "\x00-sS\x00-i\x00" +
			"-H\x00Content-Type: application/json\x00-X\x00PATCH\x00--data-binary\x00@-\x00" +
			"https://forgejo.invalid/api/v1/repos/acme/app/issues/comments/401"
	default:
		return ""
	}
}

func callStdinForRoute(cmdr *scriptCommander, route string) []string {
	var bodies []string
	for i, c := range cmdr.calls {
		if routeKey(c) == route {
			bodies = append(bodies, cmdr.stdin[i])
		}
	}
	return bodies
}

func extractSummaryStdinBody(t *testing.T, raw string) string {
	t.Helper()
	var doc struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("summary write stdin not JSON: %v\n%s", err, raw)
	}
	return doc.Body
}

type summaryHostCase struct {
	provider      string
	wantReviewURL string
	wantInlineURL string
	inlineJSON    string
	page2Route    string
	page1Raw      string
	page2Raw      string
}

func runSummaryPostCase(t *testing.T, provider string, cmdr *scriptCommander, flags core.PostFlags) *core.PostResponse {
	t.Helper()
	var warnings, human []string
	req := summaryReq(cmdr, provider, flags, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	return resp
}

func TestSummaryCommentPerHostCreateUpdateOtherAuthorPagination(t *testing.T) {
	marker := "<!-- verdict-summary head=" + summaryTestHead + " -->"
	markedOwn401 := `{"id":401,"user":{"login":"someone-else"},"author":{"username":"someone-else"},"html_url":"https://ex.invalid/c/401","updated_at":"2026-01-01T00:00:00Z","body":"Prior summary\n\n` + marker + `"}`
	markedOther := `{"id":301,"user":{"login":"different-reviewer"},"author":{"username":"different-reviewer"},"html_url":"https://ex.invalid/c/301","updated_at":"2026-01-02T00:00:00Z","body":"Someone else's summary\n\n` + marker + `"}`
	reviewMarkerComment := `{"id":302,"user":{"login":"someone-else"},"author":{"username":"someone-else"},"body":"Review note\n\n<!-- verdict-review payload=` + summaryTestHead + ` head=` + summaryTestHead + ` -->"}`
	systemNote := `{"id":303,"system":true,"author":{"username":"someone-else"},"body":"system\n\n` + marker + `"}`

	var page50 []string
	for i := range 50 {
		page50 = append(page50, fmt.Sprintf(`{"id":%d,"user":{"login":"someone-else"},"body":"noise %d"}`, 100+i, i))
	}
	forgejoPage1 := "[" + strings.Join(page50, ",") + "]"

	providers := []summaryHostCase{
		{
			provider:      providerGitHub,
			wantReviewURL: "https://example.invalid/r/101",
			wantInlineURL: "https://gh.example/acme/app/pull/7#discussion_r88",
			inlineJSON:    `[{"path":"notes.txt","line":2,"body":"Consider clarifying this line.\n\n<!-- verdict-finding sig=1111111111111111111111111111111111111111 -->","html_url":"https://gh.example/acme/app/pull/7#discussion_r88"}]`,
			page1Raw:      "[" + reviewMarkerComment + "]\n[" + markedOwn401 + "]",
		},
		{
			provider:      providerGitLab,
			wantReviewURL: "https://gl.example/acme/app/-/merge_requests/7",
			wantInlineURL: "https://gl.example/acme/app/-/merge_requests/7#note_901",
			page1Raw:      "[" + systemNote + "]\n[" + markedOwn401 + "]",
		},
		{
			provider:      providerForgejo,
			wantReviewURL: "https://fj.example/acme/app/pulls/7#pullrequestreview-901",
			wantInlineURL: "https://fj.example/acme/app/pulls/7#issuecomment-77",
			inlineJSON:    `[{"path":"notes.txt","position":1,"body":"Unrelated note.","html_url":"https://fj.example/acme/app/pulls/7#issuecomment-76"},{"path":"notes.txt","position":2,"body":"Consider clarifying this line.","html_url":"https://fj.example/acme/app/pulls/7#issuecomment-77"}]`,
			page2Route:    forgejoReadPrefix + "https://forgejo.invalid/api/v1/repos/acme/app/issues/7/comments?limit=50&page=2",
			page1Raw:      forgejoPage1,
			page2Raw:      "[" + markedOwn401 + "]",
		},
	}

	for i := range providers {
		p := &providers[i]
		t.Run(p.provider+"/create", func(t *testing.T) {
			verifyHostSummaryCreate(t, p, marker, reviewMarkerComment)
		})
		t.Run(p.provider+"/update_in_place_and_pagination", func(t *testing.T) {
			verifyHostSummaryUpdate(t, p, marker)
		})
		t.Run(p.provider+"/ignores_other_author_marker", func(t *testing.T) {
			verifyHostSummaryIgnoresOther(t, p, markedOther)
		})
	}
}

func verifyHostSummaryCreate(t *testing.T, p *summaryHostCase, marker, reviewMarkerComment string) {
	t.Helper()
	t.Chdir(t.TempDir())
	seedSummaryBundle(t, p.provider)
	cmdr := hostBaseCommander(t, p.provider, "["+reviewMarkerComment+"]", p.inlineJSON)
	resp := runSummaryPostCase(t, p.provider, cmdr, core.PostFlags{
		Approve: "false", ReviewFile: ".verdict/state/7/review.json", Summary: "comment",
	})
	if resp.Data.Summary == nil || resp.Data.Summary.Action != "create" || resp.Data.Summary.Marker != marker {
		t.Fatalf("summary = %+v, want create with marker %q", resp.Data.Summary, marker)
	}
	createBodies := callStdinForRoute(cmdr, expectedCreateRoute(p.provider))
	wantCreateCount := 1
	if p.provider == providerGitLab {
		wantCreateCount = 2
	}
	if len(createBodies) != wantCreateCount {
		t.Fatalf("create calls = %d, want %d; calls=%q", len(createBodies), wantCreateCount, cmdr.calls)
	}
	if upd := callStdinForRoute(cmdr, expectedUpdateRoute(p.provider)); len(upd) != 0 {
		t.Errorf("unexpected update calls: %d", len(upd))
	}
	assertSummaryBodyContains(t, createBodies[len(createBodies)-1], marker, p.wantReviewURL, p.wantInlineURL)
	assertPostReceiptSummary(t, "create", "")
}

func assertSummaryBodyContains(t *testing.T, rawStdin, marker, reviewURL, inlineURL string) {
	t.Helper()
	body := extractSummaryStdinBody(t, rawStdin)
	if !strings.HasSuffix(strings.TrimSpace(body), marker) {
		t.Errorf("summary body missing trailing marker %q:\n%s", marker, body)
	}
	for _, want := range []string{"Looks good — minor note below.", reviewURL, inlineURL} {
		if !strings.Contains(body, want) {
			t.Errorf("summary body missing %q:\n%s", want, body)
		}
	}
}

func assertPostReceiptSummary(t *testing.T, wantAction, wantID string) {
	t.Helper()
	rawPost, err := os.ReadFile(".verdict/state/7/post.json")
	if err != nil {
		t.Fatalf("read post.json: %v", err)
	}
	var receipt struct {
		Summary *core.PostSummary `json:"summary"`
	}
	if err := json.Unmarshal(rawPost, &receipt); err != nil {
		t.Fatalf("decode post.json: %v", err)
	}
	if receipt.Summary == nil || receipt.Summary.Action != wantAction {
		t.Errorf("post.json summary = %+v, want action=%s", receipt.Summary, wantAction)
	}
	if wantID != "" && (receipt.Summary == nil || receipt.Summary.CommentID != wantID || receipt.Summary.DuplicateCount != 2) {
		t.Errorf("post.json summary = %+v, want comment_id=%s duplicate_count=2", receipt.Summary, wantID)
	}
}

func verifyHostSummaryUpdate(t *testing.T, p *summaryHostCase, marker string) {
	t.Helper()
	t.Chdir(t.TempDir())
	seedSummaryBundle(t, p.provider)
	cmdr := hostBaseCommander(t, p.provider, p.page1Raw, p.inlineJSON)
	if p.page2Route != "" {
		cmdr.out[p.page2Route] = scriptResult{stdout: p.page2Raw}
	}
	resp := runSummaryPostCase(t, p.provider, cmdr, core.PostFlags{
		Approve: "false", ReviewFile: ".verdict/state/7/review.json", Summary: "comment",
	})
	if resp.Data.Summary == nil || resp.Data.Summary.Action != "update" || resp.Data.Summary.CommentID != "401" {
		t.Fatalf("summary = %+v, want update comment_id=401", resp.Data.Summary)
	}
	updBodies := callStdinForRoute(cmdr, expectedUpdateRoute(p.provider))
	if len(updBodies) != 1 {
		t.Fatalf("update calls = %d, want 1; calls=%q", len(updBodies), cmdr.calls)
	}
	wantCreateOnUpdate := 0
	if p.provider == providerGitLab {
		wantCreateOnUpdate = 1
	}
	if len(callStdinForRoute(cmdr, expectedCreateRoute(p.provider))) != wantCreateOnUpdate {
		t.Fatalf("create calls during update != %d", wantCreateOnUpdate)
	}
	if body := extractSummaryStdinBody(t, updBodies[0]); !strings.HasSuffix(strings.TrimSpace(body), marker) {
		t.Errorf("updated body missing marker:\n%s", body)
	}
}

func verifyHostSummaryIgnoresOther(t *testing.T, p *summaryHostCase, markedOther string) {
	t.Helper()
	t.Chdir(t.TempDir())
	seedSummaryBundle(t, p.provider)
	cmdr := hostBaseCommander(t, p.provider, "["+markedOther+"]", p.inlineJSON)
	resp := runSummaryPostCase(t, p.provider, cmdr, core.PostFlags{
		Approve: "false", ReviewFile: ".verdict/state/7/review.json", Summary: "comment",
	})
	if resp.Data.Summary == nil || resp.Data.Summary.Action != "create" {
		t.Fatalf("summary = %+v, want create when only other author posted marker", resp.Data.Summary)
	}
	if len(callStdinForRoute(cmdr, expectedUpdateRoute(p.provider))) != 0 {
		t.Errorf("modified another author's summary comment")
	}
}

func TestSummaryCommentMultipleMatchesUpdatesNewestNeverDeletes(t *testing.T) {
	t.Chdir(t.TempDir())
	seedSummaryBundle(t, providerGitHub)
	marker := "<!-- verdict-summary head=" + summaryTestHead + " -->"
	older := `{"id":401,"user":{"login":"someone-else"},"html_url":"https://gh.example/c/401","updated_at":"2026-01-01T00:00:00Z","body":"Older\n\n` + marker + `"}`
	newer := `{"id":402,"user":{"login":"someone-else"},"html_url":"https://gh.example/c/402","updated_at":"2026-01-03T00:00:00Z","body":"Newer\n\n` + marker + `"}`

	cmdr := hostBaseCommander(t, providerGitHub, "["+older+","+newer+"]", "[]")
	cmdr.out["gh\x00api\x00repos/acme/app/issues/comments/402\x00--method\x00PATCH\x00--input\x00-"] = scriptResult{
		stdout: `{"id":402,"html_url":"https://gh.example/c/402","user":{"login":"someone-else"},"body":"updated newest"}`,
	}
	resp := runSummaryPostCase(t, providerGitHub, cmdr, core.PostFlags{
		Approve: "false", ReviewFile: ".verdict/state/7/review.json", Summary: "comment",
	})
	if resp.Data.Summary == nil || resp.Data.Summary.Action != "update" ||
		resp.Data.Summary.CommentID != "402" || resp.Data.Summary.DuplicateCount != 2 {
		t.Fatalf("summary = %+v, want update id=402 duplicate_count=2", resp.Data.Summary)
	}
	if !strings.Contains(resp.Data.Summary.Note, "updated newest (402) without deleting") {
		t.Errorf("summary.Note = %q, want duplicate explanation", resp.Data.Summary.Note)
	}
	assertNoCallContaining(t, cmdr.calls, "DELETE")
	assertPostReceiptSummary(t, "update", "402")
}

func assertNoCallContaining(t *testing.T, calls [][]string, needle string) {
	t.Helper()
	for _, call := range calls {
		if strings.Contains(strings.Join(call, " "), needle) {
			t.Errorf("unexpected %s call: %q", needle, call)
		}
	}
}

func TestSummaryCommentDryRunAndOffModes(t *testing.T) {
	marker := "<!-- verdict-summary head=" + summaryTestHead + " -->"
	existing := `[{"id":401,"user":{"login":"someone-else"},"html_url":"https://gh.example/c/401","updated_at":"2026-01-01T00:00:00Z","body":"Prior\n\n` + marker + `"}]`

	t.Run("dry_run_off_reports_none_without_summary_calls", func(t *testing.T) {
		verifyDryRunOff(t)
	})
	t.Run("dry_run_comment_create_and_update_write_nothing", func(t *testing.T) {
		t.Run("create", func(t *testing.T) { verifyDryRunComment(t, "[]", "create", "summary: create") })
		t.Run("update", func(t *testing.T) { verifyDryRunComment(t, existing, "update", "summary: update 401") })
	})
	t.Run("real_post_off_omits_summary_and_keeps_wire_unchanged", func(t *testing.T) {
		verifyRealPostOff(t)
	})
	t.Run("bad_summary_flag_refused", func(t *testing.T) {
		verifyBadSummaryFlag(t)
	})
}

func verifyDryRunOff(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	seedSummaryBundle(t, providerGitHub)
	cmdr := postCommander(t)
	var warnings, human []string
	req := summaryReq(cmdr, providerGitHub, core.PostFlags{
		Approve: "false", ReviewFile: ".verdict/state/7/review.json", DryRun: true,
	}, &warnings, &human)
	req.JSONMode = false
	resp, ref := Run(context.Background(), req)
	if ref != nil || resp.Data.Summary == nil || resp.Data.Summary.Action != "none" {
		t.Fatalf("ref=%v summary=%+v, want action=none", ref, resp)
	}
	if !strings.Contains(strings.Join(human, "\n"), "summary: none") {
		t.Errorf("human dry-run missing summary: none: %q", human)
	}
	assertNoCallContaining(t, cmdr.calls, "/issues/")
}

func verifyDryRunComment(t *testing.T, listJSON, wantAct, wantLine string) {
	t.Helper()
	t.Chdir(t.TempDir())
	seedSummaryBundle(t, providerGitHub)
	cmdr := hostBaseCommander(t, providerGitHub, listJSON, "[]")
	var warnings, human []string
	req := summaryReq(cmdr, providerGitHub, core.PostFlags{
		Approve: "false", ReviewFile: ".verdict/state/7/review.json", Summary: "comment", DryRun: true,
	}, &warnings, &human)
	req.JSONMode = false
	resp, ref := Run(context.Background(), req)
	if ref != nil || resp.Data.Summary == nil || resp.Data.Summary.Action != wantAct {
		t.Fatalf("ref=%v summary=%+v, want %q", ref, resp, wantAct)
	}
	if !strings.Contains(strings.Join(human, "\n"), wantLine) {
		t.Errorf("human = %q, want substring %q", human, wantLine)
	}
	if envJSON := jq.PrettyOrdered(RenderPostEnvelope(resp)); !strings.Contains(envJSON, `"action":"`+wantAct+`"`) {
		t.Errorf("envelope JSON missing summary action %q:\n%s", wantAct, envJSON)
	}
	for _, verb := range []string{"POST", "PATCH", "PUT"} {
		assertNoCallContaining(t, cmdr.calls, verb)
	}
}

func verifyRealPostOff(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	seedSummaryBundle(t, providerGitHub)
	cmdr := postCommander(t)
	resp := runSummaryPostCase(t, providerGitHub, cmdr, core.PostFlags{
		Approve: "false", ReviewFile: ".verdict/state/7/review.json",
	})
	if resp.Data.Summary != nil {
		t.Fatalf("expected nil Summary when --summary off, got %+v", resp.Data.Summary)
	}
	if envJSON := jq.PrettyOrdered(RenderPostEnvelope(resp)); strings.Contains(envJSON, `"summary"`) {
		t.Errorf("envelope unexpectedly contains summary key:\n%s", envJSON)
	}
	rawPost, err := os.ReadFile(".verdict/state/7/post.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawPost), `"summary"`) {
		t.Errorf("post.json unexpectedly contains summary key:\n%s", rawPost)
	}
	assertNoCallContaining(t, cmdr.calls, "/issues/")
}

func verifyBadSummaryFlag(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	seedSummaryBundle(t, providerGitHub)
	cmdr := postCommander(t)
	var warnings, human []string
	req := summaryReq(cmdr, providerGitHub, core.PostFlags{
		Approve: "false", ReviewFile: ".verdict/state/7/review.json", Summary: "description",
	}, &warnings, &human)
	if _, ref := Run(context.Background(), req); ref == nil || ref.Code != "bad-flags" {
		t.Fatalf("ref = %v, want bad-flags", ref)
	}
}

func TestSummaryCommentEmptyBodyGuardAndMarkerDistinctness(t *testing.T) {
	t.Parallel()
	verifyHostEmptyBodyGuards(t)
	verifySummaryMarkerDistinctness(t)
}

func verifyHostEmptyBodyGuards(t *testing.T) {
	t.Helper()
	for _, host := range []hostSummary{githubHost{}, gitlabHost{}, forgejoHost{}} {
		cmdr := &scriptCommander{t: t, out: map[string]scriptResult{}}
		if _, err := host.createSummaryComment(context.Background(), cmdr, forgejoEnv(), "acme/app", "7", "   \n\t "); err == nil {
			t.Errorf("%T create accepted whitespace body", host)
		}
		if _, err := host.updateSummaryComment(context.Background(), cmdr, forgejoEnv(), "acme/app", "7", "1", ""); err == nil {
			t.Errorf("%T update accepted empty body", host)
		}
		if len(cmdr.calls) != 0 {
			t.Errorf("%T issued host calls on empty body: %q", host, cmdr.calls)
		}
	}
}

func verifySummaryMarkerDistinctness(t *testing.T) {
	t.Helper()
	sm := threadscore.SummaryMarker(summaryTestHead)
	rm := "<!-- verdict-review payload=" + summaryTestHead + " head=" + summaryTestHead + " -->"
	fm := threadscore.FindingMarker(summaryTestHead)
	if !threadscore.HasSummaryMarker("body\n\n" + sm) {
		t.Errorf("HasSummaryMarker failed on valid marker")
	}
	for _, badBody := range []string{"body\n\n" + rm, "body\n\n" + fm, "quoted:\n" + sm + "\nmore"} {
		if threadscore.HasSummaryMarker(badBody) {
			t.Errorf("HasSummaryMarker unexpectedly matched %q", badBody)
		}
	}
	if reviewMarkerRe.MatchString(sm) {
		t.Errorf("reviewMarkerRe matched summary marker %q", sm)
	}
	if _, ok := threadscore.FindingSig(sm); ok {
		t.Errorf("FindingSig matched summary marker %q", sm)
	}
	if !threadscore.IsVerdictBody(sm) {
		t.Errorf("IsVerdictBody rejected summary marker %q", sm)
	}
}

func TestInlineLinksFromMapsForgejoPositionAndOriginalPosition(t *testing.T) {
	t.Parallel()
	items, err := publicationList(`[
		{"path":"notes.txt","position":7,"original_position":3,"body":"active","html_url":"https://fj.example/c/1"},
		{"path":"notes.txt","position":0,"original_position":4,"body":"outdated","html_url":"https://fj.example/c/2"}
	]`)
	if err != nil {
		t.Fatalf("publicationList: %v", err)
	}
	got := inlineLinksFromMaps(items)
	if len(got) != 2 || got[0].Line != 7 || got[1].Line != 4 {
		t.Fatalf("inlineLinksFromMaps = %+v, want Line 7 and Line 4", got)
	}
}

func TestCapSummaryWithMarkerOver64KiB(t *testing.T) {
	t.Parallel()
	marker := "<!-- verdict-summary head=" + summaryTestHead + " -->"
	oversized := strings.Repeat("🔥", 18000)
	got := capSummaryWithMarker(oversized, marker, summaryBodyLimit)
	if len(got) > summaryBodyLimit {
		t.Fatalf("len(got) = %d, want <= %d", len(got), summaryBodyLimit)
	}
	if !utf8.ValidString(got) {
		t.Fatal("capped summary is not valid UTF-8")
	}
	if !strings.HasSuffix(got, "\n\n_(truncated)_\n\n"+marker) {
		t.Fatalf("capped summary missing truncation notice and marker suffix")
	}
	if !threadscore.HasSummaryMarker(got) {
		t.Fatal("HasSummaryMarker rejected capped summary")
	}
}

func TestRenderSummaryInlineSectionEmptyCommentsIgnoresOrphanLinks(t *testing.T) {
	t.Parallel()
	links := []summaryInlineLink{{Path: "notes.txt", Line: 2, Body: "orphan", URL: "https://ex.invalid/1"}}
	if got := renderSummaryInlineSection(nil, links); got != "" {
		t.Fatalf("renderSummaryInlineSection(nil, links) = %q, want empty", got)
	}
}
