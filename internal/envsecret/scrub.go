package envsecret

import "strings"

// Redacted replaces a credential value in scrubbed text. It is the one marker
// the launcher's recorded tail and verify's recorded tail share, so a reader
// of either record sees the same word for the same act.
const Redacted = "***REDACTED***"

// ScrubValues replaces every value in values that occurs in text with
// [Redacted], and returns text unchanged when none does.
//
// One left-to-right pass, taking the longest value that starts at each
// position, so a position is consumed once and no replacement can begin inside
// bytes a previous one hid. Nor can two values share a region by halves:
// `API_KEY=secretvalue` with `--token valueEXTRA` makes `secretvalueEXTRA` one
// region, not `***REDACTED***EXTRA`. Sorting the values cannot do that — only a
// prefix is settled by order — and chaining [strings.ReplaceAll] can only
// shrink the text each round, so the overlap is resolved here, against the
// original bytes.
//
// It knows nothing about which values are credentials: the caller decides that
// (secret-named environment variables, argument values the argv redactor hid,
// and whatever length floor it applies), so the policy has room to differ
// while the matching cannot.
func ScrubValues(text string, values []string) string {
	values = dropEmptyValues(values)
	if !anyValueIn(text, values) {
		return text
	}
	var out strings.Builder
	out.Grow(len(text))
	last := 0
	for i := 0; i < len(text); {
		n := longestMatchAt(text[i:], values)
		if n == 0 {
			i++
			continue
		}
		end := coveredEnd(text, i, i+n, values)
		out.WriteString(text[last:i])
		out.WriteString(Redacted)
		i, last = end, end
	}
	out.WriteString(text[last:])
	return out.String()
}

// dropEmptyValues removes the values that hide nothing: an empty operand
// (`--api-key ""`) is no credential, and matching the empty string everywhere
// would shred the text between every rune.
func dropEmptyValues(values []string) []string {
	kept := values[:0]
	for _, value := range values {
		if value != "" {
			kept = append(kept, value)
		}
	}
	return kept
}

// anyValueIn reports whether text holds any value at all, so the common case —
// a tail with nothing to hide — costs no allocation.
func anyValueIn(text string, values []string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

// longestMatchAt returns the length of the longest value that begins s, or 0
// when none does. Longest wins so a prefix of a value cannot be taken as the
// whole one.
func longestMatchAt(s string, values []string) int {
	best := 0
	for _, value := range values {
		if len(value) > best && strings.HasPrefix(s, value) {
			best = len(value)
		}
	}
	return best
}

// coveredEnd extends a matched region — start to end, the value that begins it
// - over every value that starts inside the region and reaches past its end.
// Two values that overlap without being prefixes are one region: the bytes
// between them belong to a value, so leaving them visible would publish the
// head or the tail of one. The walk stops at the region's end, so it never
// crosses text that is already accounted for.
func coveredEnd(text string, start, end int, values []string) int {
	for j := start + 1; j < end; j++ {
		if n := longestMatchAt(text[j:], values); j+n > end {
			end = j + n
		}
	}
	return end
}
