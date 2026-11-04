package readcommon_test

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/readcommon"
)

// TestSlugify pins the rule-id slug contract every learn record keys on:
// ASCII lowercasing, one dash per run of non-slug bytes, dashes trimmed off
// both ends, and a 65-byte cap. A run of dashes that collapses the wrong way
// either duplicates a separator or eats one, and two different rules can then
// share a rule id.
func TestSlugify(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 70)
	tests := []struct {
		name, in, want string
	}{
		{"words", "Hello, World!", "hello-world"},
		{"dash run collapses", "a.-b", "a-b"},
		{"dash after a slug byte", "a-b c", "a-b-c"},
		{"surrounding dashes trimmed", "--Foo--Bar--", "foo-bar"},
		{"underscore is not a slug byte", "a_b", "a-b"},
		// The edges of both character ranges: dropping 'z', '0' or '9' from
		// the allowlist deletes a real byte from every slug that carries it.
		{"range edges", "a0z9", "a0z9"},
		{"only separators", "--", ""},
		{"empty", "", ""},
		{"non-ASCII bytes do not lowercase", "Ünïcode", "n-code"},
		{"cap keeps 65 bytes", long, long[:65]},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := readcommon.Slugify(tc.in); got != tc.want {
				t.Errorf("Slugify(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestShortSHA pins the abbreviated-SHA width used in every warning and
// receipt line: seven characters when the input is longer, the whole input
// when it is shorter (a short or empty commit id must render, not panic).
func TestShortSHA(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, in, want string }{
		{"full sha", "0123456789abcdef0123456789abcdef01234567", "0123456"},
		{"eight", "01234567", "0123456"},
		{"exactly seven", "0123456", "0123456"},
		{"short", "abc", "abc"},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := readcommon.ShortSHA(tc.in); got != tc.want {
				t.Errorf("ShortSHA(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}
