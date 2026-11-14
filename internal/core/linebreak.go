package core

import "unicode"

// IsLineBreak reports whether r ends a rendered line. Control runes are the
// obvious half; U+2028 and U+2029 are separators a renderer still breaks on
// and the control test does not cover.
func IsLineBreak(r rune) bool {
	return unicode.IsControl(r) || r == '\u2028' || r == '\u2029'
}
