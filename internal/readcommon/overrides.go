package readcommon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/overrides"
)

// ValidateOverrides ports validate_overrides over the worktree's default
// overrides directory.
func ValidateOverrides() error { return ValidateOverridesAt("") }

// ValidateOverridesAt ports validate_overrides over one overrides directory:
// the locked-block scans plus the parsed-shape gates for the four P5
// fragments. Absent fragments pass silently; every violation fails closed
// typed. "" reads the worktree default, a bundle passes its controls
// snapshot's overrides dir, and messages keep the logical fragment labels.
func ValidateOverridesAt(dir string) error {
	set := overrides.At(dir)
	if err := validateFragments(set); err != nil {
		return err
	}
	if _, err := set.FooterOrder(); err != nil {
		return passRefusal(err)
	}
	if _, _, err := set.Thresholds(); err != nil {
		return passRefusal(err)
	}
	if _, err := set.AttributionText(); err != nil {
		return passRefusal(err)
	}
	return nil
}

// validateFragments ports the per-file locked-shape gate over the four P5
// fragments.
func validateFragments(set overrides.Set) error {
	fragments := []string{
		overrides.Preamble, overrides.FooterOrderPath, overrides.Threshold, overrides.Attribution,
	}
	for _, f := range fragments {
		if ref := validateFragment(set, f); ref != nil {
			return ref
		}
	}
	return nil
}

// validateFragment validates one override fragment file, if present. A
// missing path — or a non-regular file such as a directory in the
// fragment's place — reads as no fragment.
func validateFragment(set overrides.Set, f string) error {
	path := set.Path(filepath.Base(f))
	if !IsFile(path) {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return &core.RefusalError{Code: "bad-override", Message: fmt.Sprintf("override %s is unreadable (fix permissions or remove it)", f)}
	}
	text := strings.ReplaceAll(string(raw), "\r", "")
	if err := overrides.CheckLocked(f, text); err != nil {
		return passRefusal(err)
	}
	// Only the preamble and attribution fragments carry the v1 placeholder
	// inventory.
	if f == overrides.Preamble || f == overrides.Attribution {
		if err := core.ValidateAttributionPlaceholders(text, "override "+f); err != nil {
			return passRefusal(err)
		}
	}
	return nil
}

// passRefusal returns a sibling package's typed refusal unchanged: refusal
// context lives in Code/Message (rendered at the edge via [errors.As]), so
// the wrapcheck add-context rule is waived here by design.
func passRefusal(err error) error { return err }
