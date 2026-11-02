package fetch

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/cli"
)

// execResult carries a subprocess's captured streams.
type execResult struct {
	stdout string
	stderr string
}

// runCmd runs argv with an empty stdin, capturing both streams. A missing
// binary or a failing call returns the sealed error with stderr attached;
// callers map failures to the fetch warn-or-refuse posture.
func runCmd(ctx context.Context, bin string, args ...string) (execResult, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	res := execResult{}
	if err := cmd.Run(); err != nil {
		res.stdout = stdout.String()
		res.stderr = stderr.String()
		return res, &cmdError{bin: bin, err: err}
	}
	res.stdout = stdout.String()
	res.stderr = stderr.String()
	return res, nil
}

// cmdError seals a subprocess failure; callers report the captured
// streams, never this carrier.
type cmdError struct {
	err error
	bin string
}

// Error implements error.
func (e *cmdError) Error() string {
	return e.bin + ": " + e.err.Error()
}

// Unwrap exposes the underlying process error.
func (e *cmdError) Unwrap() error {
	return e.err
}

// needBinary ports `need`: a missing tool refuses with exit 1 and the
// `missing: <tool>` line on stderr (never a typed envelope).
func needBinary(r *cli.Runner, bin string) error {
	if _, err := exec.LookPath(bin); err != nil {
		fmt.Fprintf(r.Diag, "missing: %s\n", bin)
		return &needError{bin: bin}
	}
	return nil
}

// needError marks a missing-tool exit (exit 1 with the plain line above,
// no envelope even in --json mode — finishRun maps it without a refusal).
type needError struct{ bin string }

// Error implements error.
func (e *needError) Error() string { return "missing tool: " + e.bin }

// writeErrFile plants an .err sidecar with the call's stderr, creating it
// empty on success: the shell's `cmd 2>file` truncates the file either way.
func writeErrFile(path, stderr string) error {
	if err := atomic.WriteFile(path, []byte(stderr)); err != nil {
		return &fetchWriteError{path: path, err: err}
	}
	return nil
}

// fetchWriteError seals a bundle-write failure (unreachable on a writable
// bundle directory; callers refuse typed).
type fetchWriteError struct {
	err  error
	path string
}

// Error implements error.
func (e *fetchWriteError) Error() string {
	return "could not write " + e.path + ": " + e.err.Error()
}

// Unwrap exposes the underlying write error.
func (e *fetchWriteError) Unwrap() error {
	return e.err
}

// internalError seals a fetch-internal failure (disk/decoder edges the
// shell never guards): the message keeps the shell-visible text and the
// verb maps it to a typed fetch-failed refusal at the boundary.
type internalError struct{ msg string }

// Error implements error.
func (e *internalError) Error() string { return e.msg }

// internalErrorf builds an internalError with formatting.
func internalErrorf(format string, args ...any) *internalError {
	return &internalError{msg: fmt.Sprintf(format, args...)}
}

// headBytes ports `$(head -c N file)`: the first N bytes with trailing
// newlines stripped (command-substitution semantics, no tr — unlike the
// post recheck reason, the fetch excerpts keep interior newlines).
func headBytes(s string, n int) string {
	b := []byte(s)
	if len(b) > n {
		b = b[:n]
	}
	return strings.TrimRight(string(b), newline)
}

// readOptional reads a file, returning "" when it is missing or unreadable.
func readOptional(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}

// maxBackoff caps one retry sleep: reads stay bounded (3 attempts, 60s
// each) so a rate-limited fetch waits its turn instead of dying, without
// ever hanging the operator.
const maxBackoff = 60 * time.Second

// maxFetchRetries caps fetch read attempts (the shell has no fetch retry;
// the bound mirrors the post path's 3 attempts).
const maxFetchRetries = 3

// compareErrHeadBytes caps the incremental note, like `head -c 300`.
const compareErrHeadBytes = 300

// retryAfterRe matches a Retry-After hint (`Retry-After: 12`) and the loose
// `retry after 12 seconds` wording, case-insensitively.
var retryAfterRe = regexp.MustCompile(`(?i)retry-after[\s:="]+(\d+)|retry\s+after\s+(\d+)`)

// resetEpochRe matches a rate-limit reset marker with its epoch.
var resetEpochRe = regexp.MustCompile(`(?i)(?:x-)?rate-?limit-reset[\s:="]+(\d+)`)

// retryDelay ports the bounded transient retry and folds audit 21 in as
// structure: secondary rate-limit responses (403/429 with a rate-limit
// signal) honor the retry-after hint or the reset marker. Plain failures
// never retry. Only failed calls retry, so a retry can never double-write
// (the bundle lands only after a success).
func retryDelay(stderr string, attempt int) (time.Duration, bool) {
	base := time.Duration(attempt+1) * time.Second
	lower := strings.ToLower(stderr)
	wait, hinted, ok := rateLimitWait(stderr, lower)
	if !ok {
		return 0, false
	}
	if !hinted {
		wait = base
	}
	return min(wait, maxBackoff), true
}

// rateLimitWait detects a secondary rate-limit response and its wait: the
// retry-after hint wins, else the reset epoch, else no hint (callers fall
// back to the attempt-based sleep).
func rateLimitWait(stderr, lower string) (wait time.Duration, hinted, ok bool) {
	if !isRateLimited(lower) {
		return 0, false, false
	}
	if secs, found := parseRetryAfter(stderr); found {
		return time.Duration(secs) * time.Second, true, true
	}
	if epoch, found := parseResetEpoch(stderr); found {
		wait := max(time.Until(time.Unix(epoch, 0)), 0)
		return wait, true, true
	}
	return 0, false, true
}

// isRateLimited reports whether gh stderr carries a retryable rate-limit
// signal: an explicit secondary-limit note, a retry hint, or a 403/429
// naming the rate limit. Plain auth or validation 403s never match.
func isRateLimited(lower string) bool {
	norm := strings.Map(keepLetters, lower)
	if strings.Contains(norm, "secondaryratelimit") {
		return true
	}
	if strings.Contains(norm, "retryafter") {
		return true
	}
	if strings.Contains(norm, "ratelimitreset") {
		return true
	}
	if strings.Contains(norm, "ratelimit") && (strings.Contains(norm, "429") || strings.Contains(norm, "403")) {
		return true
	}
	return false
}

// keepLetters drops everything but lowercase letters and digits for signal
// matching (`rate limit`, `rate-limit`, and `ratelimit` read identically).
func keepLetters(c rune) rune {
	if c >= 'a' && c <= 'z' {
		return c
	}
	if c >= '0' && c <= '9' {
		return c
	}
	return -1
}

// parseRetryAfter reads a retry-after wait in seconds.
func parseRetryAfter(stderr string) (int64, bool) {
	m := retryAfterRe.FindStringSubmatch(stderr)
	if len(m) == 0 {
		return 0, false
	}
	for _, g := range m[1:] {
		if g == "" {
			continue
		}
		var secs int64
		if _, err := fmt.Sscanf(g, "%d", &secs); err == nil {
			return secs, true
		}
	}
	return 0, false
}

// parseResetEpoch reads a rate-limit reset epoch.
func parseResetEpoch(stderr string) (int64, bool) {
	m := resetEpochRe.FindStringSubmatch(stderr)
	if len(m) < 2 || m[1] == "" {
		return 0, false
	}
	var epoch int64
	if _, err := fmt.Sscanf(m[1], "%d", &epoch); err == nil {
		return epoch, true
	}
	return 0, false
}

// ghFetch runs gh with exactly the given argv and a bounded secondary
// rate-limit retry (audit 21): unhinted failures run up to 3 attempts with
// the attempt-based sleep; only rate-limit signals retry, everything else
// fails fast like the shell. The caller owns the error sidecars and the
// warn-or-refuse posture.
func ghFetch(ctx context.Context, r *cli.Runner, args ...string) (execResult, error) {
	var res execResult
	var err error
	for attempt := range maxFetchRetries {
		res, err = runCmd(ctx, "gh", args...)
		if err == nil {
			return res, nil
		}
		wait, retry := retryDelay(res.stderr, attempt)
		if !retry || attempt >= maxFetchRetries-1 {
			break
		}
		r.Warn(fmt.Sprintf("transient GitHub error — retry %d/2, bundle untouched", attempt+1))
		time.Sleep(wait)
	}
	return res, err
}

// curlFetch runs curl with exactly the given argv and the same bounded
// secondary rate-limit retry as gh (providers share the backoff contract).
func curlFetch(ctx context.Context, r *cli.Runner, args ...string) (execResult, error) {
	var res execResult
	var err error
	for attempt := range maxFetchRetries {
		res, err = runCmd(ctx, "curl", args...)
		if err == nil {
			return res, nil
		}
		wait, retry := retryDelay(res.stderr, attempt)
		if !retry || attempt >= maxFetchRetries-1 {
			break
		}
		r.Warn(fmt.Sprintf("transient GitHub error — retry %d/2, bundle untouched", attempt+1))
		time.Sleep(wait)
	}
	return res, err
}
