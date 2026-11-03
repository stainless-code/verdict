package fetchcore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/retry"
)

// TestFetchRetriesInternalError proves fetch now shares post's transient set:
// gh's internal-error retry note runs the full attempt budget instead of
// failing on the first call.
func TestFetchRetriesInternalError(t *testing.T) {
	t.Parallel()
	const argv = "gh\x00api\x00user"
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		argv: {stderr: "HTTP 500: Internal error occurred, please try again", fail: true},
	}}
	var warnings []string
	s := &session{commander: cmdr, warnings: &warnings}
	if _, err := s.ghFetch(context.Background(), "api", "user"); err == nil {
		t.Fatal("exhausted retry proceeded")
	}
	if len(cmdr.calls) != retry.MaxAttempts {
		t.Errorf("calls = %d, want %d", len(cmdr.calls), retry.MaxAttempts)
	}
	if len(warnings) != retry.MaxAttempts-1 {
		t.Fatalf("warnings = %q, want %d retry notices", warnings, retry.MaxAttempts-1)
	}
	for i, warning := range warnings {
		if want := fmt.Sprintf("retry %d/2", i+1); !strings.Contains(warning, want) {
			t.Errorf("warning %d = %q, want %s", i, warning, want)
		}
	}
}

// TestFetchRetryAtOnExhaustion proves an exhausted retryable fetch carries
// the next retry time onto its typed refusal, using the injected clock.
func TestFetchRetryAtOnExhaustion(t *testing.T) {
	t.Parallel()
	const argv = "gh\x00api\x00user"
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		argv: {stderr: "HTTP 429: secondary rate limit exceeded, retry-after: 0", fail: true},
	}}
	var warnings []string
	s := &session{commander: cmdr, warnings: &warnings, Now: func() time.Time { return fetchTestTime }}
	if _, err := s.ghFetch(context.Background(), "api", "user"); err == nil {
		t.Fatal("exhausted retry proceeded")
	}
	if s.retryAt != "2026-11-02T12:00:00Z" {
		t.Fatalf("retryAt = %q, want the injected clock", s.retryAt)
	}
	st := &state{warnings: warnings}
	err := st.finalizeErr(&core.RefusalError{Code: CodeFetchFailed, Message: "fetch failed"}, s)
	ref, ok := errors.AsType[*core.RefusalError](err)
	if !ok {
		t.Fatalf("err = %v, want a typed refusal", err)
	}
	if ref.RetryAt != s.retryAt {
		t.Fatalf("refusal retry_at = %q, want %q", ref.RetryAt, s.retryAt)
	}
}

// TestFetchRetryCanceledDuringBackoff proves cancellation ends the wait at
// once and maps to the cancel refusal instead of a fetch-failed one.
func TestFetchRetryCanceledDuringBackoff(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cmdr := &cancelCommander{cancel: cancel, stderr: "HTTP 429: secondary rate limit exceeded, retry-after: 30"}
	var warnings []string
	s := &session{commander: cmdr, warnings: &warnings}
	start := time.Now()
	_, err := s.ghFetch(ctx, "api", "user")
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("canceled retry took %v, want a prompt return", elapsed)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if cmdr.calls != 1 {
		t.Errorf("calls = %d, want 1", cmdr.calls)
	}
	ref, ok := errors.AsType[*core.RefusalError]((&state{}).fail(ctx, err, s))
	if !ok || ref.Code != codeCanceled {
		t.Fatalf("ref = %v, want canceled", ref)
	}
}

// cancelCommander cancels the run inside its first failed spawn, so the
// backoff sees a dead context before it can sleep.
type cancelCommander struct {
	cancel context.CancelFunc
	stderr string
	calls  int
}

// Run cancels and reports one retryable failure.
func (c *cancelCommander) Run(context.Context, core.Command) (core.Result, error) {
	c.calls++
	c.cancel()
	return core.Result{Stderr: c.stderr}, errors.New("exit 1")
}

// LookPath reports every tool present.
func (c *cancelCommander) LookPath(string) error { return nil }

// TestFetchDoesNotRetryPlainFailure proves a plain auth failure still fails
// on the first attempt: only the shared transient set retries.
func TestFetchDoesNotRetryPlainFailure(t *testing.T) {
	t.Parallel()
	const argv = "gh\x00api\x00user"
	cmdr := &scriptCommander{t: t, out: map[string]scriptResult{
		argv: {stderr: "HTTP 403: Resource not accessible by integration", fail: true},
	}}
	var warnings []string
	s := &session{commander: cmdr, warnings: &warnings}
	if _, err := s.ghFetch(context.Background(), "api", "user"); err == nil {
		t.Fatal("plain auth failure proceeded")
	}
	if len(cmdr.calls) != 1 {
		t.Errorf("calls = %d, want 1", len(cmdr.calls))
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %q, want none", warnings)
	}
}
