package core

import (
	"fmt"
	"regexp"
)

// RefusalError is a typed refusal: machine code plus human message. It is
// the only error type the command layer renders; matching uses [errors.As].
// Warnings carries the warnings accumulated before the refusal so the
// adapter's envelope stays byte-identical to a success run's warning list.
type RefusalError struct {
	Code     string
	Message  string
	Warnings []string
}

// Error implements error.
func (e *RefusalError) Error() string { return e.Code + ": " + e.Message }

// ValidateAttributionPlaceholders ports validate_attribution_placeholders:
// every {name} placeholder in text must come from the v1 inventory, scanned
// in appearance order.
func ValidateAttributionPlaceholders(text, source string) *RefusalError {
	for _, ph := range placeholderRe.FindAllString(text, -1) {
		switch ph {
		case "{version}", "{tier}", "{product_url}", "{files_reviewed}",
			"{head_sha_short}", "{model}", "{cost}", "{attribution}":
		default:
			return &RefusalError{Code: "bad-settings", Message: fmt.Sprintf(msgPlaceholder, source, ph)}
		}
	}
	return nil
}

// placeholderRe matches one {name} attribution placeholder.
var placeholderRe = regexp.MustCompile(`\{[A-Za-z_][A-Za-z0-9_]*\}`)

// msgPlaceholder is the frozen unknown-placeholder message.
const msgPlaceholder = "%s carries unknown placeholder '%s' (want one of {version}, {tier}, " +
	"{product_url}, {files_reviewed}, {head_sha_short}, {model}, {cost}, {attribution})"
