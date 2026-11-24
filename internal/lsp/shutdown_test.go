package lsp

import (
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/testenv"
)

// blockedRefresh starts a session whose next refresh blocks in the read
// model until its context ends, and returns once that refresh is in flight.
func blockedRefresh(t *testing.T, grace time.Duration) (*session, *blockingModel) {
	t.Helper()
	root := fixtureRoot(t)
	cfg := newConfig(root)
	cfg.lim.grace = grace
	blocking := &blockingModel{
		ReadModel: productionLayer(cfg, []servedRoot{newServedRoot(root, false)}),
		entered:   make(chan struct{}, 1),
		release:   make(chan struct{}),
	}
	cfg.Layer = blocking
	s := startSession(t, cfg)
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	s.barrier(2, 0)
	blocking.arm()
	s.request(3, methodExecuteCommand, map[string]any{"command": CommandRefresh})
	testenv.Until(t, func() bool {
		select {
		case <-blocking.entered:
			return true
		default:
			return false
		}
	})
	return s, blocking
}

// TestShutdownDrainKeepsReading pins item 4: while shutdown drains, the read
// loop still reads, so a $/cancelRequest for the blocked call is honored and
// the shutdown answer follows the call's RequestCancelled.
func TestShutdownDrainKeepsReading(t *testing.T) {
	t.Parallel()
	s, _ := blockedRefresh(t, time.Hour)
	s.request(4, methodShutdown, nil)
	s.notify(methodCancel, map[string]any{"id": 3})
	before, shut := s.awaitResponse(4)
	if shut.Error != nil || string(shut.Result) != "null" {
		t.Fatalf("shutdown answered %s", shut.raw)
	}
	_, refresh := s.awaitResponse(3)
	wantError(t, "the canceled refresh", &refresh, rpcRequestCancelled)
	if string(before[len(before)-1].ID) != "3" {
		t.Fatalf("the refresh answer did not precede the shutdown answer: %s", before[len(before)-1].raw)
	}
	s.notify(methodExit, nil)
	if err := s.wait(); err != nil {
		t.Fatal(err)
	}
}

// TestShutdownGraceCancelsStragglers pins the grace period: a call still
// running when it ends is canceled without any client message.
func TestShutdownGraceCancelsStragglers(t *testing.T) {
	t.Parallel()
	s, _ := blockedRefresh(t, 10*time.Millisecond)
	s.request(4, methodShutdown, nil)
	_, shut := s.awaitResponse(4)
	_, refresh := s.awaitResponse(3)
	wantError(t, "the straggling refresh", &refresh, rpcRequestCancelled)
	if shut.Error != nil {
		t.Fatalf("shutdown answered %s", shut.raw)
	}
	s.notify(methodExit, nil)
	if err := s.wait(); err != nil {
		t.Fatal(err)
	}
}

// TestExitDuringDrainIsClean pins that `exit` is read while shutdown still
// drains: the read loop ends at once, the drain still answers the call the
// client sent before shutdown (here canceled at the grace period), and the
// session ends cleanly.
func TestExitDuringDrainIsClean(t *testing.T) {
	t.Parallel()
	s, _ := blockedRefresh(t, 20*time.Millisecond)
	s.request(4, methodShutdown, nil)
	s.notify(methodExit, nil)
	if err := s.wait(); err != nil {
		t.Fatalf("Serve = %v, want a clean exit after shutdown", err)
	}
	_, refresh := s.awaitResponse(3)
	wantError(t, "the refresh outliving the grace", &refresh, rpcRequestCancelled)
	if _, shut := s.awaitResponse(4); shut.Error != nil {
		t.Fatalf("shutdown answered %s", shut.raw)
	}
}

// TestExitDuringDrainDeliversAnswers pins the transcript case: a client that
// pipelines a request, shutdown, and exit without waiting still receives the
// request's real answer and the shutdown answer.
func TestExitDuringDrainDeliversAnswers(t *testing.T) {
	t.Parallel()
	s, blocking := blockedRefresh(t, time.Hour)
	s.request(4, methodShutdown, nil)
	s.notify(methodExit, nil)
	testenv.Until(t, func() bool {
		s.server.callMu.Lock()
		defer s.server.callMu.Unlock()
		return s.server.exited
	})
	close(blocking.release)
	if err := s.wait(); err != nil {
		t.Fatal(err)
	}
	if _, refresh := s.awaitResponse(3); refresh.Error != nil {
		t.Fatalf("the pipelined refresh answered %s, want its result", refresh.raw)
	}
}

// TestShutdownDrainsInFlightRequests pins the drain itself: a request the
// client sent before shutdown gets its real answer, never a cancellation,
// and the shutdown answer follows it.
func TestShutdownDrainsInFlightRequests(t *testing.T) {
	t.Parallel()
	s, blocking := blockedRefresh(t, time.Hour)
	s.request(4, methodShutdown, nil)
	testenv.Until(t, func() bool {
		s.server.callMu.Lock()
		defer s.server.callMu.Unlock()
		return s.server.shutdown
	})
	close(blocking.release)
	before, shut := s.awaitResponse(4)
	if shut.Error != nil {
		t.Fatalf("shutdown answered %s", shut.raw)
	}
	_, refresh := s.awaitResponse(3)
	if refresh.Error != nil || !strings.Contains(string(refresh.Result), `"bundle":"local-1"`) {
		t.Fatalf("in-flight refresh answered %s, want its result", refresh.raw)
	}
	if string(before[len(before)-1].ID) != "3" {
		t.Fatalf("the refresh answer did not precede the shutdown answer: %s", before[len(before)-1].raw)
	}
	s.notify(methodExit, nil)
	if err := s.wait(); err != nil {
		t.Fatal(err)
	}
}
