package readcommon_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/readcommon"
)

// TestASCIILower pins that lowercasing touches A-Z only: a Unicode-aware
// fold would rewrite the bytes of non-ASCII text, and the slug and effort
// tiers both key on the byte form.
func TestASCIILower(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, in, want string
	}{
		{"upper edge", "Z", "z"},
		{"mixed", "AbC", "abc"},
		{"non-letters pass through", "A[Z", "a[z"},
		{"bytes outside ASCII pass through", "\xc3\x89", "\xc3\x89"},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := readcommon.ASCIILower(tc.in); got != tc.want {
				t.Errorf("ASCIILower(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestCanonFloat pins the canonical float spelling jq prints and the cost
// and calibrate text embed: full precision (1.25 stays 1.25, never a rounded
// "1") and the general format's exponent form at magnitude.
func TestCanonFloat(t *testing.T) {
	t.Parallel()
	check := func(name string, in float64, want string) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := readcommon.CanonFloat(in); got != want {
				t.Errorf("CanonFloat(%v) = %q; want %q", in, got, want)
			}
		})
	}
	check("two decimals", 1.25, "1.25")
	check("third decimal survives", 0.125, "0.125")
	check("integral", 1000, "1000")
	check("negative", -0.5, "-0.5")
	check("zero", 0, "0")
}

// assertCmp checks one pair both ways: a against b returns want, and b
// against a returns its negation, so a comparison cannot answer the same sign
// in both directions.
func assertCmp(t *testing.T, a, b any, want int) {
	t.Helper()
	if got := readcommon.Cmp(a, b); got != want {
		t.Errorf("Cmp(%v, %v) = %d; want %d", a, b, got, want)
	}
	if got := readcommon.Cmp(b, a); got != -want {
		t.Errorf("Cmp(%v, %v) = %d; want %d", b, a, got, want)
	}
}

// TestCmpOrdersBooleans pins jq's boolean leg of the total order: false
// before true, equal values tie. This is the order `sort_by` gives a
// boolean key, so an inverted leg reorders every `anchored`-style tie.
func TestCmpOrdersBooleans(t *testing.T) {
	t.Parallel()
	t.Run("false before true", func(t *testing.T) {
		t.Parallel()
		assertCmp(t, false, true, -1)
	})
	t.Run("false ties false", func(t *testing.T) {
		t.Parallel()
		assertCmp(t, false, false, 0)
	})
	t.Run("true ties true", func(t *testing.T) {
		t.Parallel()
		assertCmp(t, true, true, 0)
	})
}

// TestCmpOrdersAcrossTypeRanks pins the cross-type legs: null < false < true
// < numbers < strings < arrays < objects, and within a container the order is
// the canonical bytes' order, so an inverted compare reorders every tie the
// store sorts.
func TestCmpOrdersAcrossTypeRanks(t *testing.T) {
	t.Parallel()
	check := func(name string, a, b any, want int) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertCmp(t, a, b, want)
		})
	}
	check("null before false", nil, false, -1)
	check("false before number", false, 0.0, -1)
	check("number before string", 9.0, "a", -1)
	check("string before array", "a", []any{}, -1)
	check("array before object", []any{}, map[string]any{}, -1)
	check("arrays by canonical bytes", []any{2.0}, []any{1.0}, 1)
	check("objects by canonical bytes", map[string]any{"a": 1.0}, map[string]any{"b": 1.0}, -1)
	check("numbers order", 1.0, 2.0, -1)
	check("equal numbers tie", 1.0, 1.0, 0)
}
