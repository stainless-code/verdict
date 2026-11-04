package kb

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// counterCase is one strict counter-parse leg.
type counterCase struct {
	name string
	in   string
	want int
	ok   bool
}

// fieldCase is one stored-value counter read leg.
type fieldCase struct {
	val  any
	name string
	want int
	ok   bool
}

// TestPositiveInt proves the strict counter gate refuses signs, fractions,
// trailing junk, and overflow instead of wrapping.
func TestPositiveInt(t *testing.T) {
	t.Parallel()
	maxInt := int(^uint(0) >> 1)
	for _, tc := range []counterCase{
		{"empty", "", 0, false},
		{"zero", "0", 0, false},
		{"leading zeros", "007", 7, true},
		{"negative", "-3", 0, false},
		{"plus sign", "+3", 0, false},
		{"fraction", "1.5", 0, false},
		{"exponent", "1e3", 0, false},
		{"trailing junk", "12abc", 0, false},
		{"max int", strconv.Itoa(maxInt), maxInt, true},
		{"max int64 plus one", "9223372036854775808", 0, false},
		{"20 digits", "99999999999999999999", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkPositiveInt(t, tc)
		})
	}
}

// checkPositiveInt asserts one positive-integer leg through both spellings.
func checkPositiveInt(t *testing.T, tc counterCase) {
	t.Helper()
	got, ok := positiveInt(tc.in)
	if got != tc.want || ok != tc.ok {
		t.Errorf("positiveInt(%q) = %d, %v; want %d, %v", tc.in, got, ok, tc.want, tc.ok)
	}
	if got := isPositiveInt(tc.in); got != tc.ok {
		t.Errorf("isPositiveInt(%q) = %v; want %v", tc.in, got, tc.ok)
	}
}

// TestNumAddCounter proves counter updates keep the awk-prefix contract for
// ordinary values and refuse an overflowing stored counter.
func TestNumAddCounter(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"awk-prefix", "12abc", "13", true},
		{"fraction", "12.9", "13", true},
		{"kb-signed-prefix", "-3", "-2", true},
		{"20 digits", "99999999999999999999", "", false},
		{"max int64 plus one", "9223372036854775808", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkNumAdd(t, tc.in, tc.want, tc.ok)
		})
	}
}

// checkNumAdd asserts one counter update.
func checkNumAdd(t *testing.T, in, want string, wantOK bool) {
	t.Helper()
	got, ok := numAdd(jq.NewObj("hits", jq.Number(in)), "hits", 1)
	if ok != wantOK || (wantOK && jq.Compact(got) != want) {
		t.Errorf("numAdd(%s, 1) = %v, %v; want %s, %v", in, got, ok, want, wantOK)
	}
}

// TestNumFieldCounter proves the retire counter read refuses overflow and
// non-digit values while keeping leading zeros numeric.
func TestNumFieldCounter(t *testing.T) {
	t.Parallel()
	for _, tc := range []fieldCase{
		{json.Number("0"), "zero", 0, true},
		{json.Number("00"), "non-canonical zero", 0, false},
		{json.Number("007"), "leading zeros", 7, true},
		{json.Number("-3"), "negative", 0, false},
		{"+3", "plus sign", 0, false},
		{json.Number("1.5"), "fraction", 0, false},
		{"12abc", "trailing junk", 0, false},
		{json.Number("9223372036854775808"), "max int64 plus one", 0, false},
		{json.Number("99999999999999999999"), "20 digits", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkNumField(t, tc)
		})
	}
}

// checkNumField asserts one stored counter read.
func checkNumField(t *testing.T, tc fieldCase) {
	t.Helper()
	got, ok := numField(jq.NewObj("judged", tc.val), "judged")
	if got != tc.want || ok != tc.ok {
		t.Errorf("numField(%v) = %d, %v; want %d, %v", tc.val, got, ok, tc.want, tc.ok)
	}
}
