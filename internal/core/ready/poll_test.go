package readycore

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestPollBoundedBackoff(t *testing.T) {
	now := time.Unix(0, 0)
	var waits []time.Duration
	calls := 0
	done, err := Poll(t.Context(), 5*time.Second, time.Second, PollRuntime{
		Now:    func() time.Time { return now },
		Sleep:  func(_ context.Context, d time.Duration) error { waits = append(waits, d); now = now.Add(d); return nil },
		Jitter: func(time.Duration) time.Duration { return time.Hour },
	}, func(context.Context) (bool, error) { calls++; return false, nil })
	if err != nil || done || calls != 3 {
		t.Fatalf("got done=%v err=%v calls=%d; want false nil 3", done, err, calls)
	}
	if len(waits) != 3 || waits[0] != 1250*time.Millisecond || waits[1] != 2500*time.Millisecond || waits[2] != 1250*time.Millisecond {
		t.Fatalf("waits %v; want [1.25s 2.5s 1.25s]", waits)
	}
}

func TestPollValidation(t *testing.T) {
	for _, tc := range []struct{ timeout, interval time.Duration }{
		{0, time.Second}, {31 * time.Minute, time.Second}, {time.Second, time.Millisecond}, {time.Second, 2 * time.Second},
	} {
		_, err := Poll(t.Context(), tc.timeout, tc.interval, PollRuntime{}, func(context.Context) (bool, error) { t.Fatal("invalid wait probed"); return true, nil })
		if err == nil {
			t.Fatalf("accepted timeout=%v interval=%v", tc.timeout, tc.interval)
		}
	}
}

func TestPollHardDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		done, err := Poll(t.Context(), time.Second, time.Second, PollRuntime{}, func(ctx context.Context) (bool, error) { <-ctx.Done(); return false, ctx.Err() })
		if time.Since(start) != time.Second {
			t.Fatalf("deadline took %v; want 1s", time.Since(start))
		}
		if done || err != nil {
			t.Fatalf("got %v %v; want false nil", done, err)
		}
	})
}

func TestPollCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := Poll(ctx, time.Second, time.Second, PollRuntime{}, func(context.Context) (bool, error) { t.Fatal("canceled wait probed"); return true, nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v; want canceled", err)
	}
}

func TestPollConditionAndError(t *testing.T) {
	sentinel := errors.New("probe failed")
	for _, tc := range []struct {
		err  error
		done bool
	}{{nil, true}, {sentinel, false}} {
		done, err := Poll(t.Context(), time.Second, time.Second, PollRuntime{}, func(context.Context) (bool, error) { return tc.done, tc.err })
		if done != tc.done || !errors.Is(err, tc.err) {
			t.Fatalf("got %v %v; want %v %v", done, err, tc.done, tc.err)
		}
	}
}
