package testenv

import (
	"context"
	"testing"
	"time"
)

// TestPollReturnsWhenTheConditionHolds proves a poll that starts false still
// succeeds once the condition turns true.
func TestPollReturnsWhenTheConditionHolds(t *testing.T) {
	t.Parallel()
	calls := 0
	cond := func() bool {
		calls++
		return calls == 3
	}
	if !poll(t.Context(), time.Second, cond) {
		t.Fatalf("poll = false after %d calls; want true", calls)
	}
	if calls != 3 {
		t.Errorf("poll called cond %d times; want 3 (it stops at the first true)", calls)
	}
}

// TestPollGivesUpAtTheBound proves the bound ends a poll that never holds, so
// a hung child fails the test instead of the suite's global timeout.
func TestPollGivesUpAtTheBound(t *testing.T) {
	t.Parallel()
	start := time.Now()
	if poll(t.Context(), 20*time.Millisecond, func() bool { return false }) {
		t.Fatal("poll = true; want false when the condition never holds")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("poll took %s to give up on a 20ms bound", elapsed)
	}
}

// TestPollStopsWhenTheContextEnds proves a canceled test does not keep polling
// until the bound.
func TestPollStopsWhenTheContextEnds(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if poll(ctx, time.Minute, func() bool { return false }) {
		t.Fatal("poll = true; want false under a canceled context")
	}
}
