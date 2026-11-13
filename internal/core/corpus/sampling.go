package corpuscore

import (
	"github.com/stainless-code/verdict/internal/core"
)

// Sampling pins the run cohort and the ordered samples considered for recall.
type Sampling struct {
	LatencyS *float64             `json:"latency_s"`
	RunID    string               `json:"run_id"`
	Weights  core.SeverityWeights `json:"severity_weights"`
	K        int                  `json:"k"`
	Repeats  int                  `json:"repeats"`
	Sample   int                  `json:"sample"`
}

// Sample is one deterministic score in a run cohort.
type Sample struct {
	CostUSD  *float64
	LatencyS *float64
	Report   *Report
	Group    string
	CaseID   string
	Hits     []int
	Sampling Sampling
	Bugs     int
}

// Measurement publishes a value with its observations and exact denominator.
type Measurement struct {
	Value       any    `json:"value"`
	Unit        string `json:"unit"`
	Numerator   int    `json:"numerator"`
	Denominator int    `json:"denominator"`
	N           int    `json:"n"`
}

// AnyOfKRecall counts each bug once in complete first-k cohorts.
func AnyOfKRecall(samples []Sample) (Measurement, int) {
	groups := map[string]*sampleCohort{}
	for i := range samples {
		s := &samples[i]
		key := s.Sampling.RunID + "\x00" + s.CaseID + "\x00" + s.Group
		g := groups[key]
		if g == nil {
			g = &sampleCohort{bugs: s.Bugs, k: s.Sampling.K, repeats: s.Sampling.Repeats, seen: map[int]bool{}, hits: map[int]bool{}}
			groups[key] = g
		}
		g.add(s)
	}
	return recallOfCohorts(groups)
}

type sampleCohort struct {
	seen    map[int]bool
	hits    map[int]bool
	bugs    int
	k       int
	repeats int
	invalid bool
}

func (g *sampleCohort) add(s *Sample) {
	if s.Sampling.K != g.k || s.Bugs != g.bugs || s.Sampling.Repeats != g.repeats {
		g.invalid = true
	}
	if s.Sampling.Sample > g.k {
		return
	}
	if g.seen[s.Sampling.Sample] || s.Sampling.Sample < 1 {
		g.invalid = true
	}
	g.seen[s.Sampling.Sample] = true
	for _, hit := range s.Hits {
		g.hits[hit] = true
	}
}

func recallOfCohorts(groups map[string]*sampleCohort) (Measurement, int) {
	numerator, denominator, incomplete := 0, 0, 0
	for _, g := range groups {
		if g.invalid || len(g.seen) != g.k {
			incomplete++
			continue
		}
		numerator += len(g.hits)
		denominator += g.bugs
	}
	return measurement(numerator, denominator, denominator, "unique bugs in complete first-k cohorts"), incomplete
}
