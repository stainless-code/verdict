package core

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
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
	Code              string
	Message           string
	RetryAt           string
	Warnings          []string
	Candidates        []any
	Problems          []Problem
	ProblemsTruncated int
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

// Review-input problem codes. They publish the checker's enum in
// envelope.schema.json and are never the envelope's error.code, which stays
// bad-review-input.
const (
	ProblemCodeInvalidJSON         = "invalid-json"
	ProblemCodeWrongType           = "wrong-type"
	ProblemCodeMissingField        = "missing-field"
	ProblemCodeBadEnum             = "bad-enum"
	ProblemCodeOutOfRange          = "out-of-range"
	ProblemCodePathNotInDiff       = "path-not-in-diff"
	ProblemCodeUnknownField        = "unknown-field"
	ProblemCodeAnchorOutsideHunk   = "anchor-outside-hunk"
	ProblemCodeRigorUnset          = "rigor-unset"
	ProblemCodeNormalizedFence     = "normalized-fence"
	ProblemCodeNormalizedExtracted = "normalized-extracted"
	ProblemCodeScalarToArray       = "scalar-to-array"
	ProblemCodeKeyAlias            = "key-alias"
	ProblemCodeEnumSynonym         = "enum-synonym"
	ProblemCodeNormalizedTrivia    = "normalized-trivia"
	ProblemCodeBasisReplaced       = "basis-replaced"

	ProblemLevelError   = "error"
	ProblemLevelWarning = "warning"
)

// Problem is one CheckReview finding: its JSONPath in the agent's file, the
// published code, the level, the message, the exact fix, and an optional
// example of the accepted shape.
type Problem struct { //nolint:govet // field order is the published property order
	Path string `json:"path"`
	//nolint:revive // published enum tag exceeds the line limit
	Code    string `enum:"anchor-outside-hunk|bad-enum|basis-replaced|enum-synonym|invalid-json|key-alias|missing-field|normalized-extracted|normalized-fence|normalized-trivia|out-of-range|path-not-in-diff|rigor-unset|scalar-to-array|unknown-field|wrong-type" json:"code"`
	Level   string `enum:"error|warning"                                                                                                                                                                                                                            json:"level"` //nolint:revive // published enum tag exceeds the line limit
	Message string `json:"message"`
	Fix     string `json:"fix"`
	Example any    `json:"example,omitempty"`
}

// problemCodeLevels is the closed code set with the level each code normally
// carries. --strict and controls strict_input promote every warning.
var problemCodeLevels = map[string]string{
	ProblemCodeInvalidJSON:         ProblemLevelError,
	ProblemCodeWrongType:           ProblemLevelError,
	ProblemCodeMissingField:        ProblemLevelError,
	ProblemCodeBadEnum:             ProblemLevelError,
	ProblemCodeOutOfRange:          ProblemLevelError,
	ProblemCodePathNotInDiff:       ProblemLevelError,
	ProblemCodeUnknownField:        ProblemLevelWarning,
	ProblemCodeAnchorOutsideHunk:   ProblemLevelWarning,
	ProblemCodeRigorUnset:          ProblemLevelWarning,
	ProblemCodeNormalizedFence:     ProblemLevelWarning,
	ProblemCodeNormalizedExtracted: ProblemLevelWarning,
	ProblemCodeScalarToArray:       ProblemLevelWarning,
	ProblemCodeKeyAlias:            ProblemLevelWarning,
	ProblemCodeEnumSynonym:         ProblemLevelWarning,
	ProblemCodeNormalizedTrivia:    ProblemLevelWarning,
	ProblemCodeBasisReplaced:       ProblemLevelWarning,
}

// ProblemCodes returns the published problem-code enum in lexical order.
func ProblemCodes() []string {
	return slices.Sorted(maps.Keys(problemCodeLevels))
}

// ProblemCodeLevel reports the default level for a published problem code,
// or "" for an unknown code.
func ProblemCodeLevel(code string) string { return problemCodeLevels[code] }

// CheckSummary describes what a clean dry run would record.
type CheckSummary struct {
	Comments           int `json:"comments"              min:"0"`
	Sections           int `json:"sections"              min:"0"`
	AnchorsMovedToBody int `json:"anchors_moved_to_body" min:"0"`
}
