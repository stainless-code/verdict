package posture

import "testing"

// TestRule pins the rule's whole truth table against literals: local writes
// follow --read-only alone, and a host post needs the surface's opt-in on top
// while --read-only always wins.
//
//nolint:revive // the truth table reads as one assertion block
func TestRule(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		rule      Rule
		wantLocal bool
		wantHost  bool
	}{
		{"default", Rule{}, true, false},
		{"opted into posting", Rule{HostPostOptIn: true}, true, true},
		{"read-only", Rule{ReadOnly: true}, false, false},
		{"read-only beats the opt-in", Rule{ReadOnly: true, HostPostOptIn: true}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.rule.LocalWrites(); got != tc.wantLocal {
				t.Errorf("LocalWrites = %v, want %v", got, tc.wantLocal)
			}
			if got := tc.rule.HostPost(); got != tc.wantHost {
				t.Errorf("HostPost = %v, want %v", got, tc.wantHost)
			}
		})
	}
}
