package testenv

import (
	"context"
	"testing"
	"time"
)

// pollTimeout bounds every [Until] wait and pollInterval is the gap between
// attempts. A condition that outlives the bound is a race in the code under
// test, not a bound to raise (docs/ci-and-tests.md).
const (
	pollTimeout  = 15 * time.Second
	pollInterval = 5 * time.Millisecond
)

// Until polls cond until it reports true, failing the test after pollTimeout
// or when the test's context ends first. It is the only wait a test may use: a
// condition that never holds names a race to fix, never a deadline to widen.
func Until(t *testing.T, cond func() bool) {
	t.Helper()
	if !poll(t.Context(), pollTimeout, cond) {
		t.Fatalf("condition not satisfied within %s", pollTimeout)
	}
}

func poll(ctx context.Context, bound time.Duration, cond func() bool) bool {
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	deadline := time.NewTimer(bound)
	defer deadline.Stop()
	for {
		if cond() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-tick.C:
		}
	}
}
