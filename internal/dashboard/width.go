package dashboard

import (
	"slices"
	"strings"
	"unicode"
)

// runeRange is one inclusive code point interval.
type runeRange struct{ lo, hi rune }

// wideRanges are the East Asian Wide and Fullwidth blocks plus the emoji
// presentation blocks a terminal draws two cells wide, sorted by lo. Keeping
// the table here, not in a module, is what keeps go.mod at zero requirements.
var wideRanges = []runeRange{
	{0x1100, 0x115F},
	{0x231A, 0x231B},
	{0x2329, 0x232A},
	{0x23E9, 0x23EC},
	{0x23F0, 0x23F0},
	{0x23F3, 0x23F3},
	{0x25FD, 0x25FE},
	{0x2614, 0x2615},
	{0x2648, 0x2653},
	{0x267F, 0x267F},
	{0x2693, 0x2693},
	{0x26A1, 0x26A1},
	{0x26AA, 0x26AB},
	{0x26BD, 0x26BE},
	{0x26C4, 0x26C5},
	{0x26CE, 0x26CE},
	{0x26D4, 0x26D4},
	{0x26EA, 0x26EA},
	{0x26F2, 0x26F3},
	{0x26F5, 0x26F5},
	{0x26FA, 0x26FA},
	{0x26FD, 0x26FD},
	{0x2705, 0x2705},
	{0x270A, 0x270B},
	{0x2728, 0x2728},
	{0x274C, 0x274C},
	{0x274E, 0x274E},
	{0x2753, 0x2755},
	{0x2757, 0x2757},
	{0x2795, 0x2797},
	{0x27B0, 0x27B0},
	{0x27BF, 0x27BF},
	{0x2B1B, 0x2B1C},
	{0x2B50, 0x2B50},
	{0x2B55, 0x2B55},
	{0x2E80, 0x303E},
	{0x3041, 0x33FF},
	{0x3400, 0x4DBF},
	{0x4E00, 0x9FFF},
	{0xA000, 0xA4CF},
	{0xA960, 0xA97F},
	{0xAC00, 0xD7A3},
	{0xF900, 0xFAFF},
	{0xFE10, 0xFE19},
	{0xFE30, 0xFE6F},
	{0xFF00, 0xFF60},
	{0xFFE0, 0xFFE6},
	{0x16FE0, 0x16FE4},
	{0x17000, 0x18CFF},
	{0x1AFF0, 0x1B2FF},
	{0x1F004, 0x1F004},
	{0x1F0CF, 0x1F0CF},
	{0x1F18E, 0x1F18E},
	{0x1F191, 0x1F19A},
	{0x1F200, 0x1F251},
	{0x1F300, 0x1F64F},
	{0x1F680, 0x1F6FF},
	{0x1F7E0, 0x1F7EB},
	{0x1F90C, 0x1F9FF},
	{0x1FA70, 0x1FAFF},
	{0x20000, 0x2FFFD},
	{0x30000, 0x3FFFD},
}

// runeWidth is the number of terminal cells r occupies: 0 for controls,
// combining marks, and format runes (a ZWJ, a variation selector), 2 for a
// wide rune, 1 otherwise.
func runeWidth(r rune) int {
	switch {
	case unicode.IsControl(r) || unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf):
		return 0
	case isWide(r):
		return 2
	default:
		return 1
	}
}

func isWide(r rune) bool {
	if r < wideRanges[0].lo {
		return false
	}
	_, found := slices.BinarySearchFunc(wideRanges, r, func(rr runeRange, target rune) int {
		switch {
		case rr.hi < target:
			return -1
		case rr.lo > target:
			return 1
		default:
			return 0
		}
	})
	return found
}

// stringWidth is the cell width of s.
func stringWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

// truncate fits s into limit cells. A cut ends in ellipsis and never splits a
// wide rune: when the next wide rune would cross the budget the text stops
// one cell short, so the result is never wider than limit.
func truncate(s string, limit int, ellipsis rune) string {
	if limit <= 0 {
		return ""
	}
	if stringWidth(s) <= limit {
		return s
	}
	budget := limit - 1
	var sb strings.Builder
	used := 0
	for _, r := range s {
		w := runeWidth(r)
		if used+w > budget {
			break
		}
		used += w
		sb.WriteRune(r)
	}
	sb.WriteRune(ellipsis)
	return sb.String()
}
