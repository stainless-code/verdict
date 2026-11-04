package retry_test

import (
	"context"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/retry"
)

// TestDelayTransientSet pins the one transient set fetch and post share: an
// internal-error retry note and 403/429 secondary rate limits retry; plain
// auth, validation, and connection failures never do.
func TestDelayTransientSet(t *testing.T) {
	t.Parallel()
	for _, tc := range []delayLeg{
		{"internal error", "HTTP 500: Internal error occurred, please try again (body)", 0, true, time.Second},
		{"internal error second", "internal ERROR occurred, please try again", 1, true, 2 * time.Second},
		{"secondary limit retry-after", "HTTP 429: secondary rate limit exceeded, retry-after: 0", 0, true, 0},
		{"retry after wording", "HTTP 403: You have exceeded a secondary rate limit. Please retry after 5 seconds.", 0, true, 5 * time.Second},
		{"reset epoch", "HTTP 429: rate limit exceeded, x-ratelimit-reset: 2000000000", 0, true, -1},
		{"unhinted secondary limit", "HTTP 429: secondary rate limit exceeded for installation", 1, true, 2 * time.Second},
		{"absurd hint", "secondary rate limit, retry-after: 9999", 0, true, retry.MaxBackoff},
		{"plain 403", "HTTP 403: Resource not accessible by integration", 0, false, 0},
		{"validation", "HTTP 422: Validation Failed", 0, false, 0},
		{"connection", "connection refused", 0, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkDelayLeg(t, tc)
		})
	}
}

// TestDelayBounded proves the backoff never exceeds the cap even with absurd
// hints.
func TestDelayBounded(t *testing.T) {
	t.Parallel()
	wait, ok := retry.Delay("secondary rate limit retry-after: 3600", 2)
	if !ok || wait != retry.MaxBackoff {
		t.Errorf("wait=%v ok=%v, want %v true", wait, ok, retry.MaxBackoff)
	}
}

// TestSleepHonorsContext proves the shared backoff wait stops at once when
// the run is canceled instead of sleeping through the hint.
func TestSleepHonorsContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if retry.Sleep(ctx, time.Hour) {
		t.Fatal("canceled sleep reported elapsed")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("canceled sleep took %v, want an immediate return", elapsed)
	}
	if !retry.Sleep(context.Background(), 0) {
		t.Error("zero wait on a live context reported canceled")
	}
}

// TestAtFormatsRetryTime proves retry_at uses the injected clock and the
// envelope's RFC 3339 shape.
func TestAtFormatsRetryTime(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	if got, want := retry.At(now, 5*time.Second), "2026-11-02T12:00:05Z"; got != want {
		t.Errorf("At = %q, want %q", got, want)
	}
}

// delayLeg is one backoff leg: want < 0 means any bounded positive wait.
type delayLeg struct {
	name    string
	stderr  string
	attempt int
	wantOK  bool
	want    time.Duration
}

// checkDelayLeg asserts one backoff leg.
func checkDelayLeg(t *testing.T, tc delayLeg) {
	t.Helper()
	wait, ok := retry.Delay(tc.stderr, tc.attempt)
	if ok != tc.wantOK {
		t.Fatalf("ok=%v want %v for %q", ok, tc.wantOK, tc.stderr)
	}
	if tc.want < 0 {
		if wait <= 0 || wait > retry.MaxBackoff {
			t.Errorf("reset wait=%v out of (0,60s] for %q", wait, tc.stderr)
		}
		return
	}
	if !ok {
		return
	}
	if wait != tc.want {
		t.Errorf("wait=%v want %v for %q", wait, tc.want, tc.stderr)
	}
}
