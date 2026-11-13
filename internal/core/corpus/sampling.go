package corpuscore

import (
	"github.com/stainless-code/verdict/internal/core"
)

// Sampling pins the run cohort and the ordered samples considered for recall.
type Sampling struct {
	Weights core.SeverityWeights `json:"severity_weights"`
	RunID   string               `json:"run_id"`
	K       int                  `json:"k"`
	Repeats int                  `json:"repeats"`
	Sample  int                  `json:"sample"`
}

// Sample is one deterministic score in a run cohort.
type Sample struct {
	Report   *Report
	Sampling Sampling
	Hits     []int
	CaseID   string
	Bugs     int
}

// Measurement publishes a value with its observations and exact denominator.
type Measurement struct {
	Value       any    `json:"value"`
	Numerator   int    `json:"numerator"`
	Denominator int    `json:"denominator"`
	N           int    `json:"n"`
	Unit        string `json:"unit"`
}

// AnyOfKRecall counts each bug once when any of the first k samples found it.
// An incomplete cohort contributes no denominator; Incomplete counts these cohorts.
func AnyOfKRecall(samples []Sample) (Measurement, int) {
	type cohort struct {
		bugs int
		k    int
		seen map[int]bool
		hits map[int]bool
	}
	groups := map[string]*cohort{}
	for _, s := range samples {
		key := s.Sampling.RunID + "\x00" + s.CaseID
		g := groups[key]
		if g == nil {
			g = &cohort{bugs: s.Bugs, k: s.Sampling.K, seen: map[int]bool{}, hits: map[int]bool{}}
			groups[key] = g
		}
		if s.Sampling.Sample > g.k {
			continue
		}
		g.seen[s.Sampling.Sample] = true
		for _, hit := range s.Hits {
			g.hits[hit] = true
		}
	}
	numerator, denominator, incomplete := 0, 0, 0
	for _, g := range groups {
		if len(g.seen) != g.k {
			incomplete++
			continue
		}
		numerator += len(g.hits)
		denominator += g.bugs
	}
	return Measurement{Value: RateOrNil(numerator, denominator), Numerator: numerator, Denominator: denominator, N: denominator, Unit: "unique bugs in complete first-k cohorts"}, incomplete
}
