package core

import (
	"slices"

	"github.com/stainless-code/verdict/internal/jq"
)

// TierUntagged is the metrics bucket for absent, null, or unrecognized
// severities: untagged is honest, never inferred.
const TierUntagged = "untagged"

// TierValues lists the three published severities strongest first. A
// severity's rank is its index here, the untagged rank is one past the last
// tier, and the gate's severity threshold is a rank.
var TierValues = []string{"critical", "important", "informational"}

// ValidTier reports whether s names one of the published severities.
func ValidTier(s string) bool { return slices.Contains(TierValues, s) }

// TierOf reads one entry's severity bucket: its severity when published,
// untagged when absent, null, or unrecognized.
func TierOf(entry any) string {
	if rank := TierRank(entryField(entry, "severity")); rank < len(TierValues) {
		return TierValues[rank]
	}
	return TierUntagged
}

// TierRank orders a raw severity value: its index in TierValues, and the
// untagged rank for absent, non-string, or unpublished values, which sort
// last.
func TierRank(v any) int {
	if s, ok := v.(string); ok {
		if i := slices.Index(TierValues, s); i >= 0 {
			return i
		}
	}
	return len(TierValues)
}

func entryField(entry any, key string) any {
	m, ok := entry.(*jq.Obj)
	if !ok || m == nil {
		return nil
	}
	v, _ := m.Get(key)
	return v
}
