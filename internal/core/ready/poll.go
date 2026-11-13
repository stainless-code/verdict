package readycore

import (
	"context"
	"crypto/rand"
	"math/big"
	"time"

	"github.com/stainless-code/verdict/internal/jq"
)

// PollRuntime supplies time and delays for bounded foreground waits.
type PollRuntime struct {
	Now    func() time.Time
	Sleep  func(context.Context, time.Duration) error
	Jitter func(time.Duration) time.Duration
}

const (
	pollMaxTimeout    = 30 * time.Minute
	pollMaxDelay      = 30 * time.Second
	pollJitterDivisor = 4
	pollCanceled      = "wait canceled: %w"
)

// Poll waits synchronously for condition, returning false on timeout. Condition
// must honor its context. Errors preserve their [errors.Is] identity.
func Poll(ctx context.Context, timeout, interval time.Duration, runtime PollRuntime,
	condition func(context.Context) (bool, error),
) (bool, error) {
	if timeout <= 0 || timeout > pollMaxTimeout || interval < time.Second || interval > timeout {
		return false, jq.NewErr("wait requires timeout in (0,30m] and interval in [1s,timeout]")
	}
	now := runtime.Now
	if now == nil {
		now = time.Now
	}
	sleep := runtime.Sleep
	if sleep == nil {
		sleep = pollSleep
	}
	jitter := runtime.Jitter
	if jitter == nil {
		jitter = pollJitter
	}
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return pollLoop(bounded, ctx, now().Add(timeout), interval, PollRuntime{Now: now, Sleep: sleep, Jitter: jitter}, condition)
}

func pollLoop(ctx, parent context.Context, deadline time.Time, delay time.Duration, runtime PollRuntime,
	condition func(context.Context) (bool, error),
) (bool, error) {
	for {
		done, stop, err := pollProbe(ctx, parent, deadline, runtime, condition)
		if stop {
			return done, err
		}
		if err := pollDelay(ctx, parent, deadline, delay, runtime); err != nil {
			return false, err
		}
		delay = min(delay*2, pollMaxDelay)
	}
}

func pollDelay(ctx, parent context.Context, deadline time.Time, delay time.Duration, runtime PollRuntime) error {
	remaining := deadline.Sub(runtime.Now())
	jitterLimit := delay / pollJitterDivisor
	wait := min(remaining, delay+max(0, min(runtime.Jitter(jitterLimit), jitterLimit)))
	err := runtime.Sleep(ctx, wait)
	if parent.Err() != nil {
		return jq.NewErrf(pollCanceled, parent.Err())
	}
	if pollExpired(ctx, deadline, runtime) {
		return nil
	}
	return err
}

func pollSleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return jq.NewErrf(pollCanceled, ctx.Err())
	case <-timer.C:
		return nil
	}
}

func pollJitter(limit time.Duration) time.Duration {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(limit)+1))
	if err != nil {
		return 0
	}
	return time.Duration(n.Int64())
}

func pollExpired(ctx context.Context, deadline time.Time, runtime PollRuntime) bool {
	return !runtime.Now().Before(deadline) || ctx.Err() != nil
}

func pollProbe(ctx, parent context.Context, deadline time.Time, runtime PollRuntime,
	condition func(context.Context) (bool, error),
) (bool, bool, error) {
	if stop, err := pollStopped(ctx, parent, deadline, runtime); stop {
		return false, true, err
	}
	done, err := condition(ctx)
	if stop, canceled := pollStopped(ctx, parent, deadline, runtime); stop {
		return false, true, canceled
	}
	return done, done || err != nil, err
}

func pollStopped(ctx, parent context.Context, deadline time.Time, runtime PollRuntime) (bool, error) {
	if err := parent.Err(); err != nil {
		return true, jq.NewErrf(pollCanceled, err)
	}
	return pollExpired(ctx, deadline, runtime), nil
}
