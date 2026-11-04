// Package sig holds the finding signature derivation shared by every verb.
//
// Signatures are sig_version "1": the SHA-1 hex of the key with every
// whitespace run squashed to one space and leading/trailing spaces stripped.
// This is a wire-compatibility identifier, not a security boundary: finding
// sigs must stay stable so existing stores keep
// merging. FIPS-only environments are unsupported for this path (SHA-1 fails
// at runtime under GODEBUG=fips140=only); a sig_version "2" on SHA-256 with
// migration is the documented alternative, out of scope here.
package sig

import (
	"crypto/sha1" //nolint:gosec // wire-compat identifiers (sig_version "1"), not a security boundary.
	"encoding/hex"
	"strings"
)

// isASCIISpace reports whether c is in the C-locale [:space:] set the
// tr uses: space, tab, newline, vertical tab, form feed, carriage return.
// Notably absent: non-breaking space and every other non-ASCII blank —
// those bytes survive the squash.
func isASCIISpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	default:
		return false
	}
}

// squashASCII collapses every ASCII-whitespace run to one space.
func squashASCII(key string) string {
	var sb strings.Builder
	sb.Grow(len(key))
	space := false
	for i := range len(key) {
		if isASCIISpace(key[i]) {
			if !space {
				sb.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
		sb.WriteByte(key[i])
	}
	return sb.String()
}

// Of renders the sig_version "1" signature for key, the
// sig_of (printf without trailing newline, tr -s '[:space:]' in the C
// locale, sed edge trim, shasum -a 1).
func Of(key string) string {
	squashed := squashASCII(key)
	squashed = trimSpaces(squashed)
	sum := sha1.Sum([]byte(squashed)) //nolint:gosec // wire-compat identifiers, see package doc.
	return hex.EncodeToString(sum[:])
}

// Sum renders the raw SHA-1 hex of data with no squashing, mirroring the
// `printf '%s' "$payload" | shasum -a 1` form for the post verb's
// payload hash and double-post marker. It lives in this wire-compat package
// (not a security boundary) so the depguard exception stays confined here.
func Sum(data []byte) string {
	sum := sha1.Sum(data) //nolint:gosec // wire-compat identifiers, see package doc.
	return hex.EncodeToString(sum[:])
}

// trimSpaces strips leading/trailing ASCII spaces only, the
// sed -e 's/^ //' -e 's/ $//'. The squash step already reduced every
// whitespace run (tabs, newlines) to a space, so only spaces can sit at the
// edges; Trim with a space cutset matches exactly.
func trimSpaces(s string) string {
	start := 0
	for start < len(s) && s[start] == ' ' {
		start++
	}
	end := len(s)
	for end > start && s[end-1] == ' ' {
		end--
	}
	return s[start:end]
}
