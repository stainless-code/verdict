package fetchcore

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
)

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
