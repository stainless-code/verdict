package readcommon

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/noblock"
	"github.com/stainless-code/verdict/internal/overrides"
)

// ValidateOverrides validates the worktree's default
// overrides directory.
func ValidateOverrides() error { return ValidateOverridesAt("") }

// ValidateOverridesAt validates one overrides directory:
// the locked-block scans plus the parsed-shape gates for the four
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

// validateFragments applies the per-file locked-shape gate over the four
// override fragments.
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
	if FragmentEscapes(path) {
		return &core.RefusalError{Code: "bad-override", Message: core.Msgf(
			"override %s is a link that resolves outside its repository (replace it with the file itself)", f)}
	}
	if !IsFile(path) {
		return nil
	}
	text, ref := readFragment(path, f)
	if ref != nil {
		return ref
	}
	return checkFragmentText(f, text)
}

// checkFragmentText applies the locked-shape gate, and for the preamble and
// attribution fragments (the only ones carrying the v1 placeholder
// inventory) the placeholder gate.
func checkFragmentText(f, text string) error {
	if err := overrides.CheckLocked(f, text); err != nil {
		return passRefusal(err)
	}
	if f == overrides.Preamble || f == overrides.Attribution {
		if err := core.ValidateAttributionPlaceholders(text, "override "+f); err != nil {
			return passRefusal(err)
		}
	}
	return nil
}

// readFragment reads one present override fragment with its carriage
// returns dropped, refusing typed when it cannot be read.
func readFragment(path, f string) (string, *core.RefusalError) {
	raw, err := noblock.ReadFile(path)
	if err != nil {
		return "", &core.RefusalError{Code: "bad-override", Message: core.Msgf(
			"override %s is unreadable (fix permissions or remove it)", f)}
	}
	return strings.ReplaceAll(string(raw), "\r", ""), nil
}

// passRefusal returns a sibling package's typed refusal unchanged: refusal
// context lives in Code/Message (rendered at the edge via [errors.As]), so
// the wrapcheck add-context rule is waived here by design.
func passRefusal(err error) error { return err }

// FragmentEscapes reports an override fragment that is a link resolving
// outside the repository its overrides directory belongs to (the parent of
// the nearest .verdict ancestor, else the overrides directory itself), or a
// link that does not resolve at all. A regular file, and a link that stays in
// the repository, never escape.
func FragmentEscapes(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return true
	}
	boundary, err := filepath.EvalSymlinks(fragmentBoundary(filepath.Dir(abs)))
	if err != nil {
		return true
	}
	target, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return true
	}
	rel, err := filepath.Rel(boundary, target)
	return err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// fragmentBoundary is the repository an overrides directory belongs to.
func fragmentBoundary(dir string) string {
	for at := dir; ; {
		if filepath.Base(at) == config.VerdictDir {
			return filepath.Dir(at)
		}
		parent := filepath.Dir(at)
		if parent == at {
			return dir
		}
		at = parent
	}
}
