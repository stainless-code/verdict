package core

import (
	"fmt"
	"regexp"
)

// RefusalError is a typed refusal: machine code plus human message. It is
// the only error type the command layer renders; matching uses [errors.As].
// Warnings carries the warnings accumulated before the refusal so the
// adapter's envelope stays byte-identical to a success run's warning list.
// Candidates carries the fetch diff-too-large narrowing suggestions the
// refusal owns on the diagnostic stream (nil on every other verb, so the
// envelope stays byte-identical); the adapter copies them onto the runner
// before rendering. RetryAt carries the RFC 3339 time a retryable refusal
// becomes due (empty on every non-retryable refusal, so the envelope key
// stays dropped).
type RefusalError struct {
	Code       string
	Message    string
	RetryAt    string
	Warnings   []string
	Candidates []any
}

// Error implements error.
func (e *RefusalError) Error() string { return e.Code + ": " + e.Message }

// ValidateAttributionPlaceholders requires every {name} placeholder in text
// to come from the v1 inventory, scanned in appearance order.
func ValidateAttributionPlaceholders(text, source string) *RefusalError {
	for _, ph := range placeholderRe.FindAllString(text, -1) {
		switch ph {
		case "{version}", "{tier}", "{product_url}", "{files_reviewed}",
			"{head_sha_short}", "{model}", "{cost}", "{attribution}":
		default:
			return &RefusalError{Code: codeBadSettings, Message: fmt.Sprintf(msgPlaceholder, source, ph)}
		}
	}
	return nil
}

// codeBadSettings is the refusal code for a malformed settings file or an
// unknown attribution placeholder. [refusalCodes] spells it out again because
// the registry is data rendered into a client type, not a lookup table.
const codeBadSettings = "bad-settings"

// placeholderRe matches one {name} attribution placeholder.
var placeholderRe = regexp.MustCompile(`\{[A-Za-z_][A-Za-z0-9_]*\}`)

// msgPlaceholder is the frozen unknown-placeholder message.
const msgPlaceholder = "%s carries unknown placeholder '%s' (want one of {version}, {tier}, " +
	"{product_url}, {files_reviewed}, {head_sha_short}, {model}, {cost}, {attribution})"
