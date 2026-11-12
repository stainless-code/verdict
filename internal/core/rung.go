package core

import "slices"

// The recorded evidence rungs, strongest first.
const (
	RungReproduced = "reproduced"
	RungRan        = "ran"
	RungWalked     = "walked"
	RungPointed    = "pointed"
	RungSaid       = "said"
)

// RungValues lists the five published rungs strongest first. A rung's rank is
// its index here; nothing else orders them, and the gate's eligibility set is
// this list.
var RungValues = []string{RungReproduced, RungRan, RungWalked, RungPointed, RungSaid}

// RungOf reads one entry's recorded rung: the published rung the value names,
// said when it is absent, null, or false (the recording default), and "" when
// it names no rung at all, which is never presented as a rung that was
// recorded.
func RungOf(entry any) string {
	rank := RungRank(entryField(entry, "rigor"))
	if rank < 0 {
		return ""
	}
	return RungValues[rank]
}

// RungRank orders a raw rigor value: its index in RungValues, said's rank for
// absent, null, or false, and -1 for a value no rung names, which sorts ahead
// of every recorded rung.
func RungRank(v any) int {
	if v == nil || v == false {
		return slices.Index(RungValues, RungSaid)
	}
	if s, ok := v.(string); ok {
		if i := slices.Index(RungValues, s); i >= 0 {
			return i
		}
	}
	return -1
}
