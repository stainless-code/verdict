package core

import (
	"slices"

	"github.com/stainless-code/verdict/internal/jq"
)

// CategoryUntagged is the metrics bucket for absent, null, or unrecognized
// categories: untagged is honest, never inferred.
const CategoryUntagged = "untagged"

// CategoryValues lists the eight published categories in wire order. Labels
// are agent-supplied evidence, never inferred, and never part of the sig.
var CategoryValues = []string{
	"logic",
	"edge-case",
	"security",
	"performance",
	"accidental-commit",
	"style",
	"docs",
	"other",
}

// ValidCategory reports whether s is a published category.
func ValidCategory(s string) bool {
	return slices.Contains(CategoryValues, s)
}

// CategoryOf reads one entry's metrics bucket: its category when published,
// untagged when absent, null, or unrecognized (a value the merge normalizer
// never wrote, read honestly instead of refused).
func CategoryOf(entry any) string {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return CategoryUntagged
	}
	v, _ := m.Get("category")
	if s, ok := v.(string); ok && ValidCategory(s) {
		return s
	}
	return CategoryUntagged
}

// CategoryCounts buckets entries by CategoryOf with every published key plus
// untagged zero-filled, matching the by_tier fixed-key style.
func CategoryCounts(entries []any) map[string]int {
	counts := make(map[string]int, len(CategoryValues)+1)
	for _, c := range CategoryValues {
		counts[c] = 0
	}
	counts[CategoryUntagged] = 0
	for _, e := range entries {
		counts[CategoryOf(e)]++
	}
	return counts
}
