package postcore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

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
			forgejoReadPrefix + "https://forgejo.invalid/api/v1/repos/acme/app/pulls/7/reviews/901/comments": {
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

func TestSummaryCommentPerHostCreateUpdateOtherAuthorPagination(t *testing.T) {
	marker := "<!-- verdict-summary head=" + summaryTestHead + " -->"
	markedOwn401 := `{"id":401,"user":{"login":"someone-else"},"author":{"username":"someone-else"},"html_url":"https://ex.invalid/c/401","updated_at":"2026-01-01T00:00:00Z","body":"Prior summary\n\n` + marker + `"}`
	markedOther := `{"id":301,"user":{"login":"different-reviewer"},"author":{"username":"different-reviewer"},"html_url":"https://ex.invalid/c/301","updated_at":"2026-01-02T00:00:00Z","body":"Someone else's summary\n\n` + marker + `"}`
	reviewMarkerComment := `{"id":302,"user":{"login":"someone-else"},"author":{"username":"someone-else"},"body":"Review note\n\n<!-- verdict-review payload=` + summaryTestHead + ` head=` + summaryTestHead + ` -->"}`
	systemNote := `{"id":303,"system":true,"author":{"username":"someone-else"},"body":"system\n\n` + marker + `"}`

	// Build a 50-item page 1 for Forgejo pagination test cleanly.
	var page50 []string
	for i := range 50 {
		page50 = append(page50, fmt.Sprintf(`{"id":%d,"user":{"login":"someone-else"},"body":"noise %d"}`, 100+i, i))
	}
	forgejoPage1 := "[" + strings.Join(page50, ",") + "]"

	providers := []struct {
		provider      string
		wantReviewURL string
		wantInlineURL string
		inlineJSON    string
		paginatedArgv string
		page2Route    string
		page1Raw      string
		page2Raw      string
	}{
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
			inlineJSON:    `[{"path":"notes.txt","line":2,"body":"Consider clarifying this line.","html_url":"https://fj.example/acme/app/pulls/7#issuecomment-77"}]`,
			page2Route:    forgejoReadPrefix + "https://forgejo.invalid/api/v1/repos/acme/app/issues/7/comments?limit=50&page=2",
			page1Raw:      forgejoPage1,
			page2Raw:      "[" + markedOwn401 + "]",
		},
	}

	for _, p := range providers {
		t.Run(p.provider+"/create", func(t *testing.T) {
			t.Chdir(t.TempDir())
			seedSummaryBundle(t, p.provider)
			cmdr := hostBaseCommander(t, p.provider, "["+reviewMarkerComment+"]", p.inlineJSON)
			var warnings, human []string
			req := summaryReq(cmdr, p.provider, core.PostFlags{
				Approve:    "false",
				ReviewFile: ".verdict/state/7/review.json",
				Summary:    "comment",
			}, &warnings, &human)
			resp, ref := Run(context.Background(), req)
			if ref != nil {
				t.Fatalf("Run refusal: %v", ref)
			}
			if resp.Data.Summary == nil || resp.Data.Summary.Action != "create" {
				t.Fatalf("summary = %+v, want create", resp.Data.Summary)
			}
			if resp.Data.Summary.Marker != marker {
				t.Errorf("marker = %q, want %q", resp.Data.Summary.Marker, marker)
			}
			// GitLab shares /notes for review summary note and PR summary comment;
			// create should post 1 extra note on GitLab, 1 on GitHub/Forgejo.
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
			summaryBody := extractSummaryStdinBody(t, createBodies[len(createBodies)-1])
			if !strings.HasSuffix(strings.TrimSpace(summaryBody), marker) {
				t.Errorf("summary body missing trailing marker %q:\n%s", marker, summaryBody)
			}
			if !strings.Contains(summaryBody, "Looks good — minor note below.") {
				t.Errorf("summary body missing rendered review prose:\n%s", summaryBody)
			}
			if !strings.Contains(summaryBody, p.wantReviewURL) {
				t.Errorf("summary body missing review URL %q:\n%s", p.wantReviewURL, summaryBody)
			}
			if !strings.Contains(summaryBody, p.wantInlineURL) {
				t.Errorf("summary body missing inline URL %q:\n%s", p.wantInlineURL, summaryBody)
			}
			rawPost, err := os.ReadFile(".verdict/state/7/post.json")
			if err != nil {
				t.Fatalf("read post.json: %v", err)
			}
			var receipt map[string]any
			if err := json.Unmarshal(rawPost, &receipt); err != nil {
				t.Fatalf("decode post.json: %v", err)
			}
			sumMap, ok := receipt["summary"].(map[string]any)
			if !ok || sumMap["action"] != "create" {
				t.Errorf("post.json summary = %v, want action=create", receipt["summary"])
			}
		})

		t.Run(p.provider+"/update_in_place_and_pagination", func(t *testing.T) {
			t.Chdir(t.TempDir())
			seedSummaryBundle(t, p.provider)
			cmdr := hostBaseCommander(t, p.provider, p.page1Raw, p.inlineJSON)
			if p.page2Route != "" {
				cmdr.out[p.page2Route] = scriptResult{stdout: p.page2Raw}
			}
			var warnings, human []string
			req := summaryReq(cmdr, p.provider, core.PostFlags{
				Approve:    "false",
				ReviewFile: ".verdict/state/7/review.json",
				Summary:    "comment",
			}, &warnings, &human)
			resp, ref := Run(context.Background(), req)
			if ref != nil {
				t.Fatalf("Run refusal: %v", ref)
			}
			if resp.Data.Summary == nil || resp.Data.Summary.Action != "update" || resp.Data.Summary.CommentID != "401" {
				t.Fatalf("summary = %+v, want update comment_id=401", resp.Data.Summary)
			}
			updBodies := callStdinForRoute(cmdr, expectedUpdateRoute(p.provider))
			if len(updBodies) != 1 {
				t.Fatalf("update calls = %d, want 1; calls=%q", len(updBodies), cmdr.calls)
			}
			// GitHub and Forgejo must issue ZERO issue/note POSTs when updating in place;
			// GitLab only posts the review note once (NOT a second summary note POST).
			createBodies := callStdinForRoute(cmdr, expectedCreateRoute(p.provider))
			wantCreateOnUpdate := 0
			if p.provider == providerGitLab {
				wantCreateOnUpdate = 1
			}
			if len(createBodies) != wantCreateOnUpdate {
				t.Fatalf("create calls during update = %d, want %d", len(createBodies), wantCreateOnUpdate)
			}
			body := extractSummaryStdinBody(t, updBodies[0])
			if !strings.HasSuffix(strings.TrimSpace(body), marker) {
				t.Errorf("updated body missing marker:\n%s", body)
			}
		})

		t.Run(p.provider+"/ignores_other_author_marker", func(t *testing.T) {
			t.Chdir(t.TempDir())
			seedSummaryBundle(t, p.provider)
			cmdr := hostBaseCommander(t, p.provider, "["+markedOther+"]", p.inlineJSON)
			var warnings, human []string
			req := summaryReq(cmdr, p.provider, core.PostFlags{
				Approve:    "false",
				ReviewFile: ".verdict/state/7/review.json",
				Summary:    "comment",
			}, &warnings, &human)
			resp, ref := Run(context.Background(), req)
			if ref != nil {
				t.Fatalf("Run refusal: %v", ref)
			}
			if resp.Data.Summary == nil || resp.Data.Summary.Action != "create" {
				t.Fatalf("summary = %+v, want create when only other author posted marker", resp.Data.Summary)
			}
			if len(callStdinForRoute(cmdr, expectedUpdateRoute(p.provider))) != 0 {
				t.Errorf("modified another author's summary comment")
			}
		})
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
	var warnings, human []string
	req := summaryReq(cmdr, providerGitHub, core.PostFlags{
		Approve:    "false",
		ReviewFile: ".verdict/state/7/review.json",
		Summary:    "comment",
	}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.Data.Summary == nil || resp.Data.Summary.Action != "update" ||
		resp.Data.Summary.CommentID != "402" || resp.Data.Summary.DuplicateCount != 2 {
		t.Fatalf("summary = %+v, want update id=402 duplicate_count=2", resp.Data.Summary)
	}
	if !strings.Contains(resp.Data.Summary.Note, "updated newest (402) without deleting") {
		t.Errorf("summary.Note = %q, want duplicate explanation", resp.Data.Summary.Note)
	}
	for _, call := range cmdr.calls {
		if strings.Contains(strings.Join(call, " "), "DELETE") {
			t.Errorf("issued DELETE call: %q", call)
		}
	}
	rawPost, err := os.ReadFile(".verdict/state/7/post.json")
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Summary *core.PostSummary `json:"summary"`
	}
	if err := json.Unmarshal(rawPost, &receipt); err != nil {
		t.Fatalf("decode post.json: %v", err)
	}
	if receipt.Summary == nil || receipt.Summary.DuplicateCount != 2 || receipt.Summary.CommentID != "402" {
		t.Errorf("post.json summary = %+v, want comment_id=402 duplicate_count=2", receipt.Summary)
	}
}

func TestSummaryCommentDryRunAndOffModes(t *testing.T) {
	marker := "<!-- verdict-summary head=" + summaryTestHead + " -->"
	existing := `[{"id":401,"user":{"login":"someone-else"},"html_url":"https://gh.example/c/401","updated_at":"2026-01-01T00:00:00Z","body":"Prior\n\n` + marker + `"}]`

	t.Run("dry_run_off_reports_none_without_summary_calls", func(t *testing.T) {
		t.Chdir(t.TempDir())
		seedSummaryBundle(t, providerGitHub)
		cmdr := postCommander(t)
		var warnings, human []string
		req := summaryReq(cmdr, providerGitHub, core.PostFlags{
			Approve:    "false",
			ReviewFile: ".verdict/state/7/review.json",
			DryRun:     true,
		}, &warnings, &human)
		req.JSONMode = false
		resp, ref := Run(context.Background(), req)
		if ref != nil {
			t.Fatalf("Run refusal: %v", ref)
		}
		if resp.Data.Summary == nil || resp.Data.Summary.Action != "none" {
			t.Fatalf("summary = %+v, want action=none", resp.Data.Summary)
		}
		if !strings.Contains(strings.Join(human, "\n"), "summary: none") {
			t.Errorf("human dry-run missing summary: none: %q", human)
		}
		for _, c := range cmdr.calls {
			if strings.Contains(strings.Join(c, " "), "/issues/") {
				t.Errorf("--summary off made issue-comment call: %q", c)
			}
		}
	})

	t.Run("dry_run_comment_create_and_update_write_nothing", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			listJSON string
			wantAct  string
			wantLine string
		}{
			{name: "create", listJSON: "[]", wantAct: "create", wantLine: "summary: create"},
			{name: "update", listJSON: existing, wantAct: "update", wantLine: "summary: update 401"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Chdir(t.TempDir())
				seedSummaryBundle(t, providerGitHub)
				cmdr := hostBaseCommander(t, providerGitHub, tc.listJSON, "[]")
				var warnings, human []string
				req := summaryReq(cmdr, providerGitHub, core.PostFlags{
					Approve:    "false",
					ReviewFile: ".verdict/state/7/review.json",
					Summary:    "comment",
					DryRun:     true,
				}, &warnings, &human)
				req.JSONMode = false
				resp, ref := Run(context.Background(), req)
				if ref != nil {
					t.Fatalf("Run refusal: %v", ref)
				}
				if resp.Data.Summary == nil || resp.Data.Summary.Action != tc.wantAct {
					t.Fatalf("summary = %+v, want %q", resp.Data.Summary, tc.wantAct)
				}
				if !strings.Contains(strings.Join(human, "\n"), tc.wantLine) {
					t.Errorf("human = %q, want substring %q", human, tc.wantLine)
				}
				envJSON := jq.PrettyOrdered(RenderPostEnvelope(resp))
				if !strings.Contains(envJSON, `"action":"`+tc.wantAct+`"`) {
					t.Errorf("envelope JSON missing summary action %q:\n%s", tc.wantAct, envJSON)
				}
				for _, c := range cmdr.calls {
					s := strings.Join(c, " ")
					if strings.Contains(s, "POST") || strings.Contains(s, "PATCH") || strings.Contains(s, "PUT") {
						t.Errorf("dry-run wrote to host: %q", c)
					}
				}
			})
		}
	})

	t.Run("real_post_off_omits_summary_and_keeps_wire_unchanged", func(t *testing.T) {
		t.Chdir(t.TempDir())
		seedSummaryBundle(t, providerGitHub)
		cmdr := postCommander(t)
		var warnings, human []string
		req := summaryReq(cmdr, providerGitHub, core.PostFlags{
			Approve:    "false",
			ReviewFile: ".verdict/state/7/review.json",
		}, &warnings, &human)
		resp, ref := Run(context.Background(), req)
		if ref != nil {
			t.Fatalf("Run refusal: %v", ref)
		}
		if resp.Data.Summary != nil {
			t.Fatalf("expected nil Summary when --summary off, got %+v", resp.Data.Summary)
		}
		envJSON := jq.PrettyOrdered(RenderPostEnvelope(resp))
		if strings.Contains(envJSON, `"summary"`) {
			t.Errorf("envelope unexpectedly contains summary key:\n%s", envJSON)
		}
		rawPost, err := os.ReadFile(".verdict/state/7/post.json")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(rawPost), `"summary"`) {
			t.Errorf("post.json unexpectedly contains summary key:\n%s", rawPost)
		}
		for _, c := range cmdr.calls {
			if strings.Contains(strings.Join(c, " "), "/issues/") {
				t.Errorf("--summary off made issue-comment call: %q", c)
			}
		}
	})

	t.Run("bad_summary_flag_refused", func(t *testing.T) {
		t.Chdir(t.TempDir())
		seedSummaryBundle(t, providerGitHub)
		cmdr := postCommander(t)
		var warnings, human []string
		req := summaryReq(cmdr, providerGitHub, core.PostFlags{
			Approve:    "false",
			ReviewFile: ".verdict/state/7/review.json",
			Summary:    "description",
		}, &warnings, &human)
		_, ref := Run(context.Background(), req)
		if ref == nil || ref.Code != "bad-flags" {
			t.Fatalf("ref = %v, want bad-flags", ref)
		}
	})
}

func TestSummaryCommentEmptyBodyGuardAndMarkerDistinctness(t *testing.T) {
	t.Parallel()
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

	sm := threadscore.SummaryMarker(summaryTestHead)
	rm := "<!-- verdict-review payload=" + summaryTestHead + " head=" + summaryTestHead + " -->"
	fm := threadscore.FindingMarker(summaryTestHead)
	if !threadscore.HasSummaryMarker("body\n\n" + sm) {
		t.Errorf("HasSummaryMarker failed on valid marker")
	}
	if threadscore.HasSummaryMarker("body\n\n" + rm) {
		t.Errorf("HasSummaryMarker matched verdict-review marker")
	}
	if threadscore.HasSummaryMarker("body\n\n" + fm) {
		t.Errorf("HasSummaryMarker matched verdict-finding marker")
	}
	if threadscore.HasSummaryMarker("quoted:\n" + sm + "\nmore") {
		t.Errorf("HasSummaryMarker matched mid-body marker")
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
