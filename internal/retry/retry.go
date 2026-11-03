// Package retry owns the bounded transient retry shared by fetch, post, and
// the provider CLIs: a secondary rate-limit response or the host's
// internal-error retry note earns the next attempt; plain auth, validation,
// and missing-binary failures do not. Only failed calls retry, so a retry can
// never double-write.
package retry

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// MaxAttempts is the shared attempt cap for one bounded retry loop.
const MaxAttempts = 3

// MaxBackoff caps one retry sleep: retries stay bounded (3 attempts, 60s
// each) so a rate-limited run waits its turn instead of dying, without ever
// hanging the operator.
const MaxBackoff = 60 * time.Second

// internalErrorNote is gh's transient host-failure wording, retried with the
// attempt-based sleep like the original post path.
const internalErrorNote = "internal error occurred, please try again"

// retryAfterRe matches a Retry-After hint (`Retry-After: 12`) and the loose
// `retry after 12 seconds` wording, case-insensitively.
var retryAfterRe = regexp.MustCompile(`(?i)retry-after[\s:="]+(\d+)|retry\s+after\s+(\d+)`)

// resetEpochRe matches a rate-limit reset marker with its epoch.
var resetEpochRe = regexp.MustCompile(`(?i)(?:x-)?rate-?limit-reset[\s:="]+(\d+)`)

// Delay reports how long to wait before the next attempt and whether the
// failed call is retryable at all. The transient set is shared by every
// caller: secondary rate limits (403/429 with a rate-limit signal) honor the
// retry-after hint or the reset marker, an unhinted transient falls back to
// the attempt-based sleep, and the host's internal-error retry note always
// sleeps 1s then 2s. Plain failures never retry.
func Delay(stderr string, attempt int) (time.Duration, bool) {
	base := time.Duration(attempt+1) * time.Second
	lower := strings.ToLower(stderr)
	if strings.Contains(lower, internalErrorNote) {
		return base, true
	}
	wait, hinted, ok := rateLimitWait(stderr, lower)
	if !ok {
		return 0, false
	}
	if !hinted {
		wait = base
	}
	return min(wait, MaxBackoff), true
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
	if _, err := fmt.Sscanf(m[1], "%d", &epoch); err != nil {
		return 0, false
	}
	return epoch, true
}
