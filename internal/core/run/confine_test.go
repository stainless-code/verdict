package run

import "testing"

// TestTargetEscapes pins which symlink targets leave the tree a review
// snapshot copies: an absolute or rooted target, and a relative one whose
// `..` climbs above the root; a target that stays below the root is kept.
func TestTargetEscapes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		rel, target string
		want        bool
	}{
		{rel: "l", target: "/etc", want: true},
		{rel: "l", target: `\rooted`, want: true},
		{rel: "l", target: "..", want: true},
		{rel: "l", target: "../x", want: true},
		{rel: "d/l", target: "../../x", want: true},
		{rel: "l", target: "a/../../x", want: true},
		{rel: "l", target: "src", want: false},
		{rel: "l", target: "a/../x", want: false},
		{rel: "d/l", target: "../x", want: false},
		{rel: "d/l", target: ".", want: false},
	} {
		if got := targetEscapes(tc.rel, tc.target); got != tc.want {
			t.Errorf("targetEscapes(%q, %q) = %v, want %v", tc.rel, tc.target, got, tc.want)
		}
	}
}
