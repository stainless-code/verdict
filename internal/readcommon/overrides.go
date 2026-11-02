package readcommon

import (
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/overrides"
)

// ValidateOverrides ports validate_overrides: the locked-block scans plus
// the parsed-shape gates for the four P5 fragments. Absent fragments pass
// silently; every violation fails closed typed (bad-override, or
// bad-settings for unknown placeholders).
func ValidateOverrides() error {
	fragments := []string{
		overrides.Preamble, overrides.FooterOrderPath, overrides.Threshold, overrides.Attribution,
	}
	for _, f := range fragments {
		if ref := validateFragment(f); ref != nil {
			return ref
		}
	}
	if _, err := overrides.FooterOrder(); err != nil {
		return passRefusal(err)
	}
	if _, _, err := overrides.Thresholds(); err != nil {
		return passRefusal(err)
	}
	if exists(overrides.Attribution) {
		if _, err := overrides.AttributionText(); err != nil {
			return passRefusal(err)
		}
	}
	return nil
}

// validateFragment validates one override fragment file, if present. A
// missing path — or a non-regular file such as a directory in the
// fragment's place — reads as no fragment.
func validateFragment(f string) error {
	if fi, err := os.Stat(f); err != nil || !fi.Mode().IsRegular() {
		return nil
	}
	raw, err := os.ReadFile(f)
	if err != nil {
		return &cli.RefusalError{Code: "bad-override", Message: fmt.Sprintf("override %s is unreadable (fix permissions or remove it)", f)}
	}
	text := strings.ReplaceAll(string(raw), "\r", "")
	if err := overrides.CheckLocked(f, text); err != nil {
		return passRefusal(err)
	}
	if f == overrides.Preamble || f == overrides.Attribution {
		if err := cli.ValidateAttributionPlaceholders(text, "override "+f); err != nil {
			return passRefusal(err)
		}
	}
	return nil
}

// passRefusal returns a sibling package's typed refusal unchanged: refusal
// context lives in Code/Message (rendered at the edge via [errors.As]), so
// the wrapcheck add-context rule is waived here by design.
func passRefusal(err error) error { return err }
