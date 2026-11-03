package readcommon_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/readcommon"
)

// TestIsDigits pins the shared digit gate: empty and non-digit bytes refuse,
// leading zeros and long digit runs accept (magnitude is the caller's job).
func TestIsDigits(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"zero", "0", true},
		{"leading zeros", "007", true},
		{"negative", "-3", false},
		{"plus sign", "+3", false},
		{"fraction", "1.5", false},
		{"exponent", "1e3", false},
		{"trailing junk", "12abc", false},
		{"20 digits", "99999999999999999999", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := readcommon.IsDigits(tc.in); got != tc.want {
				t.Errorf("IsDigits(%q) = %v; want %v", tc.in, got, tc.want)
			}
		})
	}
}
