package readycore

import (
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

type waitHost struct {
	calls []string
	count int
}

func (*waitHost) LookPath(string) error { return nil }
func (h *waitHost) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	argv := strings.Join(cmd.Argv, " ")
	h.calls = append(h.calls, argv)
	switch {
	case strings.Contains(argv, "api repos/acme/app/pulls/42 --include"):
		h.count++
		if h.count > 1 {
			return core.Result{Stdout: "HTTP/2.0 304 Not Modified\r\n\r\n", ExitCode: 1}, nil
		}
		return core.Result{Stdout: "HTTP/2.0 200 OK\r\nETag: \"revision-1\"\r\n\r\n" + `{"state":"open","merged_at":null,"head":{"sha":"abc"}}`}, nil
	case strings.Contains(argv, "pr view"):
		return core.Result{Stdout: `{"headRefOid":"abc","statusCheckRollup":[{"__typename":"CheckRun","status":"COMPLETED","conclusion":"FAILURE"}],"reviews":[{"id":"r1","state":"APPROVED"}]}`}, nil
	case strings.Contains(argv, "graphql"):
		return core.Result{Stdout: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}`}, nil
	}
	return core.Result{ExitCode: 1}, nil
}

func TestWaitProbeConditionalAndFailedChecks(t *testing.T) {
	host := &waitHost{}
	req := &core.ReadRequest{Repo: "acme/app", Pr: "42", Runtime: &core.Runtime{Commander: host}}
	probe := &WaitProbe{}
	for range 2 {
		snapshot, ref := probe.Read(t.Context(), req, "github")
		if ref != nil {
			t.Fatalf("%v calls %q", ref, host.calls)
		}
		assertWaitSnapshot(t, snapshot)
	}
	if len(host.calls) != 6 || host.calls[3] != `gh api repos/acme/app/pulls/42 --include -H If-None-Match: "revision-1"` {
		t.Fatalf("calls: %q", host.calls)
	}
}

func TestWaitProbeUnsupported(t *testing.T) {
	for _, provider := range []string{"gitlab", "forgejo"} {
		host := &waitHost{}
		_, ref := (&WaitProbe{}).Read(t.Context(), &core.ReadRequest{Runtime: &core.Runtime{Commander: host}}, provider)
		if ref == nil || ref.Code != "provider-unimplemented" || !strings.Contains(ref.Message, provider) || len(host.calls) != 0 {
			t.Fatalf("provider %s: ref=%v calls=%v", provider, ref, host.calls)
		}
	}
}

func TestWaitChecksPendingAndUnknown(t *testing.T) {
	for _, checks := range [][]map[string]any{nil, {{"__typename": "Mystery"}}, {{"__typename": "CheckRun", "status": "IN_PROGRESS"}}} {
		_, done, green := waitChecks(checks)
		if done || green {
			t.Fatalf("checks %v became complete/green", checks)
		}
	}
}

func assertWaitSnapshot(t *testing.T, snapshot *WaitSnapshot) {
	t.Helper()
	if snapshot.State != "OPEN" || snapshot.Head != "abc" || !snapshot.ChecksKnown || !snapshot.ChecksDone || snapshot.ChecksGreen || snapshot.Reviews != `[{"id":"r1","state":"APPROVED"}]` || snapshot.Threads != `[]` {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

type pageHost struct{ calls int }

func (h *pageHost) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	h.calls++
	if h.calls == 1 {
		return core.Result{Stdout: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":true,"endCursor":"page-2"},"nodes":[{"id":"one","isResolved":false,"comments":{"totalCount":1,"nodes":[{"body":"first"}]}}]}}}}}`}, nil
	}
	if !strings.Contains(strings.Join(cmd.Argv, " "), "endCursor=page-2") {
		return core.Result{ExitCode: 1}, nil
	}
	return core.Result{Stdout: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":false},"nodes":[{"id":"two","isResolved":true,"comments":{"totalCount":0,"nodes":[]}}]}}}}}`}, nil
}

func TestWaitThreadsPages(t *testing.T) {
	host := &pageHost{}
	nodes, ref := waitThreads(t.Context(), &core.ReadRequest{Repo: "acme/app", Pr: "42", Runtime: &core.Runtime{Commander: host}})
	if ref != nil {
		t.Fatal(ref)
	}
	if host.calls != 2 || len(nodes) != 2 {
		t.Fatalf("calls %d nodes %v", host.calls, nodes)
	}
	if jq.Compact(nodes) != `[{"comments":{"nodes":[{"body":"first"}],"totalCount":1},"id":"one","isResolved":false},{"comments":{"nodes":[],"totalCount":0},"id":"two","isResolved":true}]` {
		t.Fatalf("nodes %s", jq.Compact(nodes))
	}
}

func TestWaitRejectsTruncatedComments(t *testing.T) {
	nodes := []any{map[string]any{"comments": map[string]any{"totalCount": 2, "nodes": []any{map[string]any{"body": "first"}}}}}
	if waitCommentsComplete(nodes) {
		t.Fatal("truncated thread comments accepted")
	}
}

func TestWaitRejectsHeadRace(t *testing.T) {
	_, ref := decodeWaitDetail(`{"headRefOid":"new-head","reviews":[]}`, map[string]any{"head": map[string]any{"sha": "old-head"}})
	if ref == nil || ref.Code != "pr-read-failed" {
		t.Fatalf("head race ref %v", ref)
	}
}

func TestWaitParsePullStatusLines(t *testing.T) {
	body := `{"state":"open","head":{"sha":"abc"}}`
	cached := map[string]any{"state": "open", "head": map[string]any{"sha": "cached"}}
	cases := []struct {
		name    string
		status  string
		body    string
		cached  map[string]any
		wantSHA string
		wantErr bool
	}{
		{name: "http2 reasonless 200", status: "HTTP/2 200", body: body, wantSHA: "abc"},
		{name: "http2.0 200 OK", status: "HTTP/2.0 200 OK", body: body, wantSHA: "abc"},
		{name: "http1.1 304 Not Modified", status: "HTTP/1.1 304 Not Modified", cached: cached, wantSHA: "cached"},
		{name: "http2 reasonless 304", status: "HTTP/2 304", cached: cached, wantSHA: "cached"},
		{name: "304 without cache fails", status: "HTTP/1.1 304 Not Modified", wantErr: true},
		{name: "500 fails", status: "HTTP/2 500", body: body, wantErr: true},
		{name: "missing http prefix fails", status: "200 OK", body: body, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probe := &WaitProbe{cached: tc.cached}
			raw := tc.status + "\r\nETag: \"v1\"\r\n\r\n" + tc.body
			got, ref := probe.parsePull(raw)
			if tc.wantErr {
				if ref == nil || ref.Code != "pr-read-failed" {
					t.Fatalf("got doc=%v ref=%v; want pr-read-failed", got, ref)
				}
				return
			}
			if ref != nil {
				t.Fatalf("unexpected refusal: %v", ref)
			}
			head, ok := got["head"].(map[string]any)
			if !ok || head["sha"] != tc.wantSHA {
				t.Fatalf("got head %v; want %s", got["head"], tc.wantSHA)
			}
		})
	}
}

func TestWaitCheckRunConclusions(t *testing.T) {
	cases := []struct {
		conclusion string
		wantKnown  bool
		wantDone   bool
		wantGreen  bool
	}{
		{conclusion: "SUCCESS", wantKnown: true, wantDone: true, wantGreen: true},
		{conclusion: "NEUTRAL", wantKnown: true, wantDone: true, wantGreen: true},
		{conclusion: "SKIPPED", wantKnown: true, wantDone: true, wantGreen: true},
		{conclusion: "FAILURE", wantKnown: true, wantDone: true, wantGreen: false},
		{conclusion: "", wantKnown: false, wantDone: false, wantGreen: false},
	}
	for _, tc := range cases {
		t.Run(tc.conclusion, func(t *testing.T) {
			known, done, green := waitChecks([]map[string]any{{
				"__typename": "CheckRun",
				"status":     "COMPLETED",
				"conclusion": tc.conclusion,
			}})
			if known != tc.wantKnown || done != tc.wantDone || green != tc.wantGreen {
				t.Fatalf("conclusion %q = (%v,%v,%v); want (%v,%v,%v)",
					tc.conclusion, known, done, green, tc.wantKnown, tc.wantDone, tc.wantGreen)
			}
		})
	}
}
