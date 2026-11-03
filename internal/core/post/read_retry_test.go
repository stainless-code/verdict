package postcore

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/retry"
)

// retryResult is one canned read attempt: the streams plus the failure flag.
type retryResult struct {
	stdout string
	stderr string
	fail   bool
}

// retryCommander replays read attempts in order and records every argv, so a
// retry leg can prove the exact attempt count and the unchanged command.
type retryCommander struct {
	t       *testing.T
	results []retryResult
	calls   [][]string
}

// Run records argv and replays the next canned attempt.
func (s *retryCommander) Run(_ context.Context, cmd core.Command) (core.Result, error) {
	s.t.Helper()
	s.calls = append(s.calls, append([]string{}, cmd.Argv...))
	if len(s.results) == 0 {
		s.t.Fatalf("unexpected extra call: %q", cmd.Argv)
	}
	res := s.results[0]
	s.results = s.results[1:]
	out := core.Result{Stdout: res.stdout, Stderr: res.stderr}
	if res.fail {
		return out, errors.New("exit 1")
	}
	return out, nil
}

// retryHead is the reviewed SHA the live lookup returns.
const retryHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// TestReadRetryLiveSHA proves the drift read honors a secondary rate limit:
// a 403 retry-after hint and a reset marker retry, then the third attempt's
// head lands with every attempt using the exact same argv.
func TestReadRetryLiveSHA(t *testing.T) {
	t.Parallel()
	cmdr := &retryCommander{t: t, results: []retryResult{
		{stderr: "HTTP 403: You have exceeded a secondary rate limit. Please retry after 0 seconds.", fail: true},
		{stderr: "HTTP 429: rate limit exceeded, x-ratelimit-reset: 1", fail: true},
		{stdout: retryHead + "\n"},
	}}
	sha, err := ghLiveSHA(context.Background(), cmdr, "acme/app", "7")
	if err != nil {
		t.Fatalf("live SHA retry failed: %v", err)
	}
	if sha != retryHead {
		t.Errorf("sha = %q, want the third attempt's head", sha)
	}
	assertRetryCalls(t, cmdr, 3,
		[]string{"gh", "api", "repos/acme/app/pulls/7", "--jq", ".head.sha"})
}

// TestReadRetryAuthorship proves the authorship read retries a 429 carrying
// a retry-after hint, then uses the successful attempt's login.
func TestReadRetryAuthorship(t *testing.T) {
	t.Parallel()
	cmdr := &retryCommander{t: t, results: []retryResult{
		{stderr: "HTTP 429: secondary rate limit exceeded, retry-after: 0", fail: true},
		{stdout: "someone-else\n"},
	}}
	login, err := ghSelfLogin(context.Background(), cmdr)
	if err != nil {
		t.Fatalf("authorship retry failed: %v", err)
	}
	if login != "someone-else" {
		t.Errorf("login = %q, want the second attempt's login", login)
	}
	assertRetryCalls(t, cmdr, 2, []string{"gh", "api", "user", "--jq", ".login"})
}

// TestReadRetryRecheck proves the live-thread recheck retries a 403 carrying
// a retry-after hint instead of downgrading a bindable APPROVE: the second
// attempt's thread payload is counted and the staged stderr stays empty.
func TestReadRetryRecheck(t *testing.T) {
	t.Parallel()
	cmdr := &retryCommander{t: t, results: []retryResult{
		{stderr: "HTTP 403: You have exceeded a secondary rate limit. Please retry after 0 seconds.", fail: true},
		{stdout: `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"isResolved":false},{"isResolved":true}]}}}}}`},
	}}
	dir := t.TempDir()
	open, reason, err := postRecheck(context.Background(), cmdr, "acme/app", "7", dir)
	if err != nil {
		t.Fatalf("recheck retry failed: %v", err)
	}
	if open != 1 || reason != "" {
		t.Errorf("open=%d reason=%q, want 1 and no downgrade reason", open, reason)
	}
	assertRetryCalls(t, cmdr, 2, append([]string{"gh"}, threadsArgv("acme", "app", "7")...))
	if got := readRetrySidecar(t, dir+"/.recheck.err"); got != "" {
		t.Errorf(".recheck.err = %q, want empty after the successful attempt", got)
	}
}

// TestReadRetryFailsFast proves a plain 403 (no rate-limit signal) never
// retries: the first failure is the result, like the shell.
func TestReadRetryFailsFast(t *testing.T) {
	t.Parallel()
	cmdr := &retryCommander{t: t, results: []retryResult{
		{stderr: "HTTP 403: Resource not accessible by integration", fail: true},
	}}
	if _, err := ghLiveSHA(context.Background(), cmdr, "acme/app", "7"); err == nil {
		t.Fatal("plain 403 proceeded")
	}
	assertRetryCalls(t, cmdr, 1,
		[]string{"gh", "api", "repos/acme/app/pulls/7", "--jq", ".head.sha"})
}

// TestReadRetryBounded proves the read retry stops at three attempts even
// when every response carries a retry hint, then returns the last error.
func TestReadRetryBounded(t *testing.T) {
	t.Parallel()
	limited := retryResult{stderr: "HTTP 429: secondary rate limit exceeded, retry-after: 0", fail: true}
	cmdr := &retryCommander{t: t, results: []retryResult{limited, limited, limited}}
	if _, err := ghLiveSHA(context.Background(), cmdr, "acme/app", "7"); err == nil {
		t.Fatal("exhausted retry proceeded")
	}
	assertRetryCalls(t, cmdr, retry.MaxAttempts,
		[]string{"gh", "api", "repos/acme/app/pulls/7", "--jq", ".head.sha"})
}

// assertRetryCalls proves the attempt count and that every attempt ran the
// exact same argv.
func assertRetryCalls(t *testing.T, cmdr *retryCommander, want int, argv []string) {
	t.Helper()
	if len(cmdr.calls) != want {
		t.Fatalf("calls = %d, want %d: %q", len(cmdr.calls), want, cmdr.calls)
	}
	for _, call := range cmdr.calls {
		if !slices.Equal(call, argv) {
			t.Fatalf("argv = %q, want %q", call, argv)
		}
	}
}

// readRetrySidecar reads one staged sidecar.
func readRetrySidecar(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}
