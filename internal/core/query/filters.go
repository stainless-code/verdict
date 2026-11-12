package query

import (
	"regexp"

	"github.com/stainless-code/verdict/internal/config"
)

// sigRe is one finding sig or prefix: lower-case hex, 4 to 40 characters.
var sigRe = regexp.MustCompile(`^[0-9a-f]{4,40}$`)

// ValidSig reports whether a route's sig is addressable.
func ValidSig(sig string) bool { return sigRe.MatchString(sig) }

// ValidBundleGlob reports whether a listing filter is a portable relative
// glob; the same gate the configuration parser applies.
func ValidBundleGlob(pattern string) bool { return config.ValidGlob(pattern) }

// BundleMatches reports whether one bundle name matches a listing glob.
func BundleMatches(pattern, bundle string) bool { return config.Match(pattern, bundle) }
