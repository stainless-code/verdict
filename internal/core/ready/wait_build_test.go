package readycore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stainless-code/verdict/internal/core"
)

type buildHost struct {
	sequenceHost
	stall       bool
	stallOnPoll int
}

func (h *buildHost) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	argv := strings.Join(cmd.Argv, " ")
	if h.stallOnPoll > 0 && h.polls >= h.stallOnPoll && strings.Contains(argv, "pr view") {
		<-ctx.Done()
		return core.Result{ExitCode: 1}, nil
	}
	if strings.Contains(argv, "isDraft,mergeable") {
		if h.stall {
			<-ctx.Done()
			return core.Result{ExitCode: 1}, nil
		}
		return core.Result{Stdout: `{"isDraft":false,"mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","body":"A sufficiently long description to make this change reviewable today.","state":"OPEN","headRefOid":"abc"}`}, nil
	}
	if strings.Contains(argv, "pr checks") {
		return core.Result{Stdout: `[{"bucket":"fail","name":"build","state":"FAILURE"}]`}, nil
	}
	return h.sequenceHost.Run(ctx, cmd)
}

func buildRequest(t *testing.T) (*Request, *buildHost, *time.Duration) {
	t.Helper()
	wait, host, elapsed := waitTestRequest(t, "checks-done")
	bundle := filepath.Join(wait.RepoRoot, ".verdict", "state", "42")
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "PR.json"), []byte(`{"headRefOid":"abc"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	build := &buildHost{sequenceHost: *host}
	build.field = "failed"
	wait.Commander = build
	return &Request{ReadRequest: wait.ReadRequest, WaitFor: "build", Timeout: "5s", PollRuntime: wait.PollRuntime}, build, elapsed
}

func TestReadyWaitBuildFailureFinishes(t *testing.T) {
	req, host, elapsed := buildRequest(t)
	resp, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	if resp.Ready || resp.Data.Wait.Outcome != "condition-met" || *elapsed != time.Second || host.polls != 2 || resp.Data.Blockers[0].Kind != "ci-failing" {
		t.Fatalf("ready=%v wait=%+v elapsed=%v polls=%d blockers=%+v", resp.Ready, resp.Data.Wait, *elapsed, host.polls, resp.Data.Blockers)
	}
}

func TestReadyWaitTimeoutNotReady(t *testing.T) {
	req, host, elapsed := buildRequest(t)
	req.Check = true
	req.Timeout = "1s"
	host.field = "never"
	resp, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatal(ref)
	}
	if resp.Ready || !resp.GateFailed || resp.Data.Wait.Outcome != "wait-timeout" || resp.Data.Blockers[0].Kind != "wait-timeout" || *elapsed != time.Second {
		t.Fatalf("response %+v wait=%+v blockers=%+v elapsed=%v", resp, resp.Data.Wait, resp.Data.Blockers, *elapsed)
	}
}

func TestReadyWaitBoundsReportingReads(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		req, host, _ := buildRequest(t)
		host.polls = 1
		host.stall = true
		req.PollRuntime = PollRuntime{}
		req.Timeout = "1s"
		start := time.Now()
		resp, ref := Run(t.Context(), req)
		if ref != nil {
			t.Fatal(ref)
		}
		if resp.Ready || resp.Data.Wait.Outcome != "wait-timeout" || time.Since(start) != time.Second {
			t.Fatalf("response %+v wait=%+v elapsed=%v", resp, resp.Data.Wait, time.Since(start))
		}
	})
}

func TestReadyWaitWallClockTimeoutPreservesWaitData(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		req, host, _ := buildRequest(t)
		host.field = "never"
		host.stallOnPoll = 2
		req.PollRuntime = PollRuntime{}
		req.Timeout = "2s"
		start := time.Now()
		resp, ref := Run(t.Context(), req)
		assertWallClockTimeout(t, resp, ref, start)
	})
}

func assertWallClockTimeout(t *testing.T, resp *Response, ref *core.RefusalError, start time.Time) {
	t.Helper()
	if ref != nil {
		t.Fatal(ref)
	}
	if resp.Ready || resp.Data.Wait.Outcome != "wait-timeout" || time.Since(start) != 2*time.Second {
		t.Fatalf("response %+v wait=%+v elapsed=%v", resp, resp.Data.Wait, time.Since(start))
	}
	if resp.Data.Wait.Observations != 1 || resp.Data.Wait.Snapshot == nil || resp.Data.Wait.Snapshot.Head != "abc" {
		t.Fatalf("lost wait data on wall-clock timeout: %+v", resp.Data.Wait)
	}
}

func TestReadyWaitRequiresBundleAndBuild(t *testing.T) {
	req, _, _ := buildRequest(t)
	req.WaitFor = "deploy"
	_, ref := Run(t.Context(), req)
	if ref == nil || ref.Code != "bad-flags" {
		t.Fatalf("got %v; want bad-flags", ref)
	}
	req.WaitFor = "build"
	req.Pr = "99"
	_, ref = Run(t.Context(), req)
	if ref == nil || ref.Code != "missing-bundle" {
		t.Fatalf("got %v; want missing-bundle", ref)
	}
}

func TestWaitKeepsPresentEmptyProviderMarker(t *testing.T) {
	req, host, _ := buildRequest(t)
	file := filepath.Join(req.RepoRoot, ".verdict", "state", "42", "PR.json")
	if err := os.WriteFile(file, []byte(`{"provider":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, ref := Wait(t.Context(), &WaitRequest{ReadRequest: req.ReadRequest, Until: "merged", Timeout: "1s", Once: true})
	if ref == nil || ref.Code != "provider-unimplemented" || host.polls != 0 {
		t.Fatalf("ref=%v polls=%d", ref, host.polls)
	}
}
