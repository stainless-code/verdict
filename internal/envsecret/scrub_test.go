package envsecret_test

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/envsecret"
)

// TestScrubValuesOverlaps proves two credential values that overlap are
// redacted as one region. Sorting longest first settles only the prefix case:
// `API_KEY=secretvalue` with `--token valueEXTRA` still records
// `***REDACTED***EXTRA`, because replacing `secretvalue` consumed the bytes the
// second value needed to match. The mirror case leaks the head instead. One
// left-to-right pass that takes the longest match at each position, and extends
// a match over every value starting inside it, hides both in full.
func TestScrubValuesOverlaps(t *testing.T) {
	t.Parallel()
	redacted := envsecret.Redacted
	cases := []struct {
		name   string
		text   string
		want   string
		values []string
	}{
		{
			// The shorter value's tail is the longer one's head.
			name:   "overlap-tail-into-head",
			text:   "token=secretvalueEXTRA",
			values: []string{"secretvalue", "valueEXTRA"},
			want:   "token=" + redacted,
		},
		{
			// The mirror: the longer value starts inside the shorter one and
			// reaches past its end, so replacing the longer one first would
			// leave the shorter one's head in the clear.
			name:   "overlap-head-into-tail",
			text:   "token=a1b2c3d4e5f6",
			values: []string{"a1b2c3d4", "b2c3d4e5f6"},
			want:   "token=" + redacted,
		},
		{
			name:   "prefix",
			text:   "token=secretvalueEXTRA",
			values: []string{"secretvalue", "secretvalueEXTRA"},
			want:   "token=" + redacted,
		},
		{
			name:   "prefix-reverse-order",
			text:   "token=secretvalueEXTRA",
			values: []string{"valueEXTRA", "secretvalue", "secretvalueEXTRA"},
			want:   "token=" + redacted,
		},
		{
			// Three values each overlapping the next: one region, one marker,
			// and no four-byte tail left readable.
			name:   "three-deep-chain",
			text:   "token=abcdefghijkl",
			values: []string{"abcdefgh", "cdefghij", "efghijkl"},
			want:   "token=" + redacted,
		},
		{
			name:   "same-value-twice",
			text:   "a=secretvalue b=secretvalue",
			values: []string{"secretvalue", "secretvalue"},
			want:   "a=" + redacted + " b=" + redacted,
		},
		{
			// A one-byte value still hides itself everywhere it stands alone,
			// and the longer one beside it is taken whole rather than as a
			// prefix.
			name:   "single-byte-against-longer",
			text:   "a=ab b=a",
			values: []string{"a", "ab"},
			want:   redacted + "=" + redacted + " b=" + redacted,
		},
		{
			name:   "empty-value",
			text:   "exit=0",
			values: []string{""},
			want:   "exit=0",
		},
		{
			// Overlap resolved against the original bytes, not against text a
			// previous replacement already shrank: the region covers both
			// values even though the second begins mid-way through the first.
			name:   "adjacent-regions",
			text:   "a=secretvalue b=othersecret ok",
			values: []string{"secretvalue", "othersecret"},
			want:   "a=" + redacted + " b=" + redacted + " ok",
		},
		{
			name:   "no-value-present",
			text:   "exit=0\nok\n",
			values: []string{"secretvalue"},
			want:   "exit=0\nok\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := envsecret.ScrubValues(tc.text, tc.values); got != tc.want {
				t.Errorf("ScrubValues(%q, %q) = %q, want %q", tc.text, tc.values, got, tc.want)
			}
		})
	}
}

// TestScrubValuesLeavesNoOverlappingByte pins the property the table rows
// illustrate: for any two values where one overlaps the other, no byte of
// either survives the scrub.
func TestScrubValuesLeavesNoOverlappingByte(t *testing.T) {
	t.Parallel()
	shorter, longer := "secretvalue", "valueEXTRA"
	got := envsecret.ScrubValues("token=secretvalueEXTRA", []string{shorter, longer})
	if got != "token="+envsecret.Redacted {
		t.Fatalf("ScrubValues = %q, want only the marker", got)
	}
	for _, leaked := range []string{"value", "VALUE", "EXTRA"} {
		if strings.Contains(got, leaked) {
			t.Errorf("ScrubValues leaked %q: %q", leaked, got)
		}
	}
}

// TestScrubValuesTouchesNothingElse proves the scrub is a replacement, not a
// filter: text around a credential is copied byte for byte and the marker
// stands exactly where the value stood.
func TestScrubValuesTouchesNothingElse(t *testing.T) {
	t.Parallel()
	in := "line one\nleak=supersecretvalue line two\n"
	got := envsecret.ScrubValues(in, []string{"supersecretvalue"})
	want := "line one\nleak=" + envsecret.Redacted + " line two\n"
	if got != want {
		t.Fatalf("ScrubValues = %q, want %q", got, want)
	}
}
