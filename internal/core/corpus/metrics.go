package corpuscore

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
)

// Metrics keeps the denominator and observation count beside every corpus rate.
type Metrics struct {
	CostUSD            *float64    `json:"cost_usd"`
	LatencyS           *float64    `json:"latency_s"`
	SignalToNoise      Measurement `json:"signal_to_noise"`
	AnyOfKRecall       Measurement `json:"any_of_k_recall"`
	ControlFPRate      Measurement `json:"control_fp_rate"`
	SeverityWeightedF1 Measurement `json:"severity_weighted_f1"`
	Precision          Measurement `json:"precision"`
	FalsePositiveRate  Measurement `json:"false_positive_rate"`
	Recall             Measurement `json:"recall"`
	CostN              int         `json:"cost_n"`
	LatencyN           int         `json:"latency_n"`
	Samples            int         `json:"samples"`
	IncompleteCohorts  int         `json:"incomplete_cohorts"`
}

type metricCounts struct{ bugs, reported, tp, fp, controls, controlFP, weightedTP, weightedFP, weightedFN int }

func (c *metricCounts) add(s *Sample) {
	r, w := s.Report, s.Sampling.Weights
	c.bugs += len(r.Case.Bugs)
	c.reported += r.Reported
	c.tp += r.Matched
	c.fp += r.FalsePositives
	if r.Case.Kind == kindControl {
		c.controls++
		c.controlFP += r.FalsePositives
	}
	for _, o := range r.Outcomes {
		if o.HasBugHit {
			c.weightedTP += w.Weight(r.Case.Bugs[o.BugIndex].Severity)
		} else {
			c.weightedFP += w.Weight(o.Severity)
		}
	}
	for _, b := range r.Missed {
		c.weightedFN += w.Weight(b.Severity)
	}
}

// Measure aggregates scores; incomplete evidence yields null totals.
func Measure(samples []Sample) *Metrics {
	c := metricCounts{}
	m := &Metrics{Samples: len(samples)}
	cost, latency := 0.0, 0.0
	for i := range samples {
		s := &samples[i]
		c.add(s)
		cost, latency = m.addEvidence(s, cost, latency)
	}
	m.Precision = measurement(c.tp, c.reported, c.reported, "reported findings")
	m.Recall = measurement(c.tp, c.bugs, c.bugs, "bug samples")
	m.FalsePositiveRate = measurement(c.fp, c.reported, c.reported, "reported findings")
	m.ControlFPRate = measurement(c.controlFP, c.controls, c.controls, "control case samples; false-positive findings per control")
	m.SeverityWeightedF1 = measurement(2*c.weightedTP,
		2*c.weightedTP+c.weightedFP+c.weightedFN,
		c.bugs+c.fp,
		"weighted F1; n=bug samples plus false-positive findings",
	)
	m.SignalToNoise = measurement(c.tp, c.fp, c.reported, "false-positive findings; n=reported findings")
	m.AnyOfKRecall, m.IncompleteCohorts = AnyOfKRecall(samples)
	m.CostUSD = completeTotal(cost, m.CostN, len(samples))
	m.LatencyS = completeTotal(latency, m.LatencyN, len(samples))
	return m
}

func measurement(numerator, denominator, n int, unit string) Measurement {
	return Measurement{Value: RateOrNil(numerator, denominator), Numerator: numerator, Denominator: denominator, N: n, Unit: unit}
}

// Wire renders metrics as ordered JSON.
func (m *Metrics) Wire() any {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	out, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil
	}
	return out
}

// Summary renders the same denominators in the human corpus report.
func (m *Metrics) Summary() string {
	rates := []struct {
		name string
		m    Measurement
	}{
		{"precision", m.Precision},
		{"recall", m.Recall},
		{"false_positive_rate", m.FalsePositiveRate},
		{"any_of_k_recall", m.AnyOfKRecall},
		{"control_fp_rate", m.ControlFPRate},
		{"severity_weighted_f1", m.SeverityWeightedF1},
		{"signal_to_noise", m.SignalToNoise},
	}
	var text strings.Builder
	text.WriteString("metrics:")

	for _, r := range rates {
		fmt.Fprintf(&text, " %s=%s (%d/%d, n=%d)", r.name, RateText(r.m.Value), r.m.Numerator, r.m.Denominator, r.m.N)
	}
	return text.String()
}

func (m *Metrics) addEvidence(s *Sample, cost, latency float64) (float64, float64) {
	if s.CostUSD != nil {
		cost += *s.CostUSD
		m.CostN++
	}
	if s.LatencyS != nil {
		latency += *s.LatencyS
		m.LatencyN++
	}
	return cost, latency
}

const measurementScale = 1e6

func completeTotal(total float64, n, samples int) *float64 {
	if n == 0 || n != samples {
		return nil
	}
	v := math.Round(total*measurementScale) / measurementScale
	return &v
}
