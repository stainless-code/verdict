package readycore

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
)

type sequenceHost struct {
	field string
	polls int
	calls []string
}

func (h *sequenceHost) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	argv := strings.Join(cmd.Argv, " ")
	h.calls = append(h.calls, argv)
	switch {
	case strings.Contains(argv, "--include"):
		h.polls++
		state := "open"
		if h.field == "merged" && h.polls >= 2 {
			return core.Result{Stdout: "HTTP/2.0 200 OK\r\n\r\n" + `{"state":"closed","merged_at":"2026-11-13","head":{"sha":"abc"}}`}, nil
		}
		return core.Result{Stdout: "HTTP/2.0 200 OK\r\n\r\n" + fmt.Sprintf(`{"state":%q,"head":{"sha":"abc"}}`, state)}, nil
	case strings.Contains(argv, "pr view"):
		return h.detail(), nil
	case strings.Contains(argv, "graphql"):
		return core.Result{Stdout: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}`}, nil
	}
	return core.Result{ExitCode: 1}, nil
}

func waitTestRequest(t *testing.T, until string) (*WaitRequest, *sequenceHost, *time.Duration) {
	t.Helper()
	now := time.Unix(0, 0)
	elapsed := time.Duration(0)
	host := &sequenceHost{field: until}
	req := &WaitRequest{Until: until, Timeout: "5s", Repo: "acme/app", Pr: "42", RepoRoot: t.TempDir(), Runtime: &core.Runtime{Commander: host, Now: func() time.Time { return now }}}
	req.PollRuntime = PollRuntime{Now: func() time.Time { return now }, Sleep: func(_ context.Context, d time.Duration) error { now = now.Add(d); elapsed += d; return nil }, Jitter: func(time.Duration) time.Duration { return 0 }}
	return req, host, &elapsed
}

func TestWaitChangesAndConditions(t *testing.T) {
	for _, until := range []string{"merged", "closed", "checks-green", "checks-done", "review-changed"} {
		t.Run(until, func(t *testing.T) {
			assertWaitCondition(t, until)
		})
	}
}

func TestWaitTimeoutIsData(t *testing.T) {
	req, host, elapsed := waitTestRequest(t, "merged")
	host.field = "never"
	resp, ref := Wait(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	if resp.Data.Outcome != "wait-timeout" || *elapsed != 5*time.Second || host.polls != 3 {
		t.Fatalf("response=%+v elapsed=%v polls=%d", resp.Data, *elapsed, host.polls)
	}
}

func TestWaitOnceDoesNotSleep(t *testing.T) {
	req, host, elapsed := waitTestRequest(t, "merged")
	req.Once = true
	resp, ref := Wait(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	if resp.Data.Outcome != "condition-unmet" || *elapsed != 0 || host.polls != 1 {
		t.Fatalf("response=%+v elapsed=%v polls=%d", resp.Data, *elapsed, host.polls)
	}
}

func TestWaitDoneAllowsFailedBuild(t *testing.T) {
	req, host, _ := waitTestRequest(t, "checks-done")
	host.field = "failed"
	resp, ref := Wait(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	if resp.Data.Outcome != "condition-met" || resp.Data.Snapshot.ChecksGreen {
		t.Fatalf("data %+v", resp.Data)
	}
}

func TestWaitInvalidBoundsAndCondition(t *testing.T) {
	for _, tc := range []struct{ until, timeout, interval string }{{"unknown", "5s", "1s"}, {"merged", "31m", "1s"}, {"merged", "5s", "1ms"}} {
		req, host, _ := waitTestRequest(t, tc.until)
		req.Timeout = tc.timeout
		req.Interval = tc.interval
		_, ref := Wait(t.Context(), req)
		if ref == nil || ref.Code != "bad-flags" || host.polls != 0 {
			t.Fatalf("case %+v: ref=%v polls=%d", tc, ref, host.polls)
		}
	}
}

func (h *sequenceHost) detail() core.Result {
	status, conclusion, review := "IN_PROGRESS", "", "APPROVED"
	if h.polls >= 2 && h.field != "never" {
		status = "COMPLETED"
		conclusion = "SUCCESS"
		if h.field == "failed" {
			conclusion = "FAILURE"
		}
		review = "CHANGES_REQUESTED"
	}
	return core.Result{Stdout: fmt.Sprintf(`{"headRefOid":"abc","statusCheckRollup":[{"__typename":"CheckRun","status":%q,"conclusion":%q}],"reviews":[{"state":%q}]}`, status, conclusion, review)}
}

func assertWaitCondition(t *testing.T, until string) {
	t.Helper()
	req, host, elapsed := waitTestRequest(t, until)
	if until == "closed" {
		host.field = "merged"
	}
	observed := 0
	req.Observe = func(*WaitSnapshot) { observed++ }
	resp, ref := Wait(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	if resp.Data.Outcome != "condition-met" || host.polls != 2 || *elapsed != time.Second || observed != 2 {
		t.Fatalf("outcome=%s polls=%d elapsed=%v observed=%d", resp.Data.Outcome, host.polls, *elapsed, observed)
	}
}

func TestWaitOnceDeadlineStillTimeout(t *testing.T) {
	req, _, _ := waitTestRequest(t, "merged")
	req.Once = true
	req.Observe = func(*WaitSnapshot) {
		if err := req.PollRuntime.Sleep(t.Context(), 5*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	resp, ref := Wait(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	if resp.Data.Outcome != "wait-timeout" {
		t.Fatalf("late one-shot outcome %s; want wait-timeout", resp.Data.Outcome)
	}
}

func TestWaitRefusesLocalBundle(t *testing.T) {
	req, host, _ := waitTestRequest(t, "merged")
	req.Pr = "local-branch"
	_, ref := Wait(t.Context(), req)
	if ref == nil || ref.Code != "no-pr-target" || host.polls != 0 {
		t.Fatalf("local-branch ref=%v polls=%d; want no-pr-target", ref, host.polls)
	}
}

func TestWaitSkipsThreadsUnlessReviewChanged(t *testing.T) {
	req, host, _ := waitTestRequest(t, "merged")
	req.Once = true
	if _, ref := Wait(t.Context(), req); ref != nil {
		t.Fatal(ref)
	}
	for _, call := range host.calls {
		if strings.Contains(call, "graphql") {
			t.Fatalf("merged wait queried threads: %q", host.calls)
		}
	}
}
