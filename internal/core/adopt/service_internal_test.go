package adoptcore

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// TestNumVal proves the anchor reader accepts decoded JSON numbers and
// strict digit strings, and refuses signs, fractions, trailing junk, and
// overflow instead of wrapping.
func TestNumVal(t *testing.T) {
	t.Parallel()
	maxInt := int(^uint(0) >> 1)
	for _, tc := range []struct { //nolint:govet // table layout prioritizes readability
		name string
		in   any
		want int
		ok   bool
	}{
		{"json number", json.Number("2"), 2, true},
		{"constructed number", jq.Number("3"), 3, true},
		{"digit string", "007", 7, true},
		{"empty", "", 0, false},
		{"negative", "-3", 0, false},
		{"plus sign", "+3", 0, false},
		{"fraction", "1.5", 0, false},
		{"exponent", "1e3", 0, false},
		{"trailing junk", "12abc", 0, false},
		{"max int", strconv.Itoa(maxInt), maxInt, true},
		{"max int64 plus one", "9223372036854775808", 0, false},
		{"20 digits", "99999999999999999999", 0, false},
		{"number overflow", jq.Number("99999999999999999999"), 0, false},
		{"bool", true, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := numVal(tc.in)
			if got != tc.want || ok != tc.ok {
				t.Errorf("numVal(%v) = %d, %v; want %d, %v", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestParseHunkRowOverflow proves a corrupt hunks.tsv row with an
// unparseable number is skipped, never wrapped into a bogus range.
func TestParseHunkRowOverflow(t *testing.T) {
	t.Parallel()
	if h, ok := parseHunkRow("notes.txt\tNEW\t002\t3"); !ok || h.Start != 2 || h.End != 3 {
		t.Errorf("leading zeros row = %+v, %v; want start 2 end 3", h, ok)
	}
	for _, row := range []string{
		"notes.txt\tNEW\t99999999999999999999\t3",
		"notes.txt\tNEW\t1\t9223372036854775808",
	} {
		if h, ok := parseHunkRow(row); ok {
			t.Errorf("overflow row %q = %+v, true; want skipped", row, h)
		}
	}
}
