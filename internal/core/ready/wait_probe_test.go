package readycore

import (
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
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
			return core.Result{Stdout: "HTTP/2.0 304 Not Modified\r\n\r\n"}, nil
		}
		return core.Result{Stdout: "HTTP/2.0 200 OK\r\nETag: \"revision-1\"\r\n\r\n" + `{"state":"open","merged_at":null,"head":{"sha":"abc"}}`}, nil
	case strings.Contains(argv, "pr view"):
		return core.Result{Stdout: `{"statusCheckRollup":[{"__typename":"CheckRun","status":"COMPLETED","conclusion":"FAILURE"}],"reviews":[{"id":"r1","state":"APPROVED"}]}`}, nil
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
