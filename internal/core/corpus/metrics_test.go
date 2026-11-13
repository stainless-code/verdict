package corpuscore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

func measuredFixture() []Sample {
	c := &core.CorpusCase{ID: "bugs", Bugs: []core.CorpusBug{
		{Path: "a", Line: 10, Kind: "race", Severity: "critical"},
		{Path: "b", Line: 20, Kind: "race", Severity: "important"},
		{Path: "c", Line: 30, Kind: "race", Severity: "informational"},
	}}
	clean := &core.CorpusCase{ID: "clean", Kind: "control", Bugs: []core.CorpusBug{}}
	finding := func(path string, line int, severity string) any {
		return jq.NewObj("path", path, "line", line, "body", "race", "severity", severity)
	}
	reports := []*Report{
		Evaluate(c, []any{finding("a", 10, "critical"), finding("x", 40, "important"), finding("x", 50, "informational")}),
		Evaluate(c, []any{finding("b", 20, "important"), finding("x", 60, "informational")}),
		Evaluate(clean, []any{finding("x", 40, "informational"), finding("x", 50, "informational")}),
		Evaluate(clean, nil),
	}
	samples := make([]Sample, 0, 4)
	for i, r := range reports {
		cost, latency := float64(i+1)/100, float64(i+1)
		samples = append(samples, Sample{Report: r, CaseID: r.Case.ID, Bugs: len(r.Case.Bugs), Sampling: Sampling{RunID: r.Case.ID, K: 1, Repeats: 4, Sample: i + 1, Weights: config.SeverityWeights(nil)}, CostUSD: &cost, LatencyS: &latency})
	}
	return samples
}

func TestMetricsHandComputed(t *testing.T) {
	m := Measure(measuredFixture())
	tests := []struct {
		name                      string
		got                       Measurement
		value                     float64
		numerator, denominator, n int
	}{
		{"precision_2_over_7", m.Precision, 0.2857, 2, 7, 7},
		{"recall_2_over_6", m.Recall, 0.3333, 2, 6, 6},
		{"false_discovery_rate_5_over_7", m.FalseDiscoveryRate, 0.7143, 5, 7, 7},
		{"control_fp_1_over_2", m.ControlFPRate, 0.5, 1, 2, 2},
		{"control_findings_2_over_2", m.ControlFindingsPerCase, 1, 2, 2, 2},
		{"weighted_f1_2times30_over_60plus9plus32", m.SeverityWeightedPooledF1, 0.5941, 60, 101, 11},
		{"signal_to_noise_2_over_5", m.SignalToNoise, 0.4, 2, 5, 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkMetric(t, tt.got, tt.value, tt.numerator, tt.denominator, tt.n)
		})
	}
	t.Run("cost_001plus002plus003plus004", func(t *testing.T) { checkRecordedTotal(t, m.CostUSD, m.CostN, 0.1) })
	t.Run("latency_1plus2plus3plus4", func(t *testing.T) { checkRecordedTotal(t, m.LatencyS, m.LatencyN, 10) })
}

func TestUnknownMetricsStayNull(t *testing.T) {
	s := measuredFixture()
	s[0].CostUSD = nil
	s[0].LatencyS = nil
	m := Measure(s)
	if m.CostUSD != nil || m.LatencyS != nil || m.CostN != 3 || m.LatencyN != 3 {
		t.Fatalf("partial measurement = %+v; want null totals, n=3", m)
	}
	m = Measure(nil)
	if m.Precision.Value != nil || m.ControlFPRate.Value != nil || m.SignalToNoise.Value != nil || m.SeverityWeightedPooledF1.Value != nil {
		t.Fatalf("empty metrics = %+v; want null rates", m)
	}
}

func checkMetric(t *testing.T, got Measurement, value float64, numerator, denominator, n int) {
	t.Helper()
	if got.Value != value || got.Numerator != numerator || got.Denominator != denominator || got.N != n {
		t.Fatalf("metric = %+v; want value=%v numerator=%d denominator=%d n=%d", got, value, numerator, denominator, n)
	}
}

func checkRecordedTotal(t *testing.T, got *float64, n int, want float64) {
	t.Helper()
	if got == nil {
		t.Fatal("total is null")
	}
	if *got != want || n != 4 {
		t.Fatalf("total=%v n=%d; want %v n=4", *got, n, want)
	}
}

func TestMetricWireUsesExactDefinitions(t *testing.T) {
	doc, ok := Measure(measuredFixture()).Wire().(*jq.Obj)
	if !ok {
		t.Fatal("metrics wire is not an object")
	}
	for _, key := range []string{"false_discovery_rate", "control_fp_rate", "control_findings_per_case", "severity_weighted_pooled_f1"} {
		if !doc.Has(key) {
			t.Errorf("missing metric %q", key)
		}
	}
	for _, key := range []string{"false_positive_rate", "severity_weighted_f1"} {
		if doc.Has(key) {
			t.Errorf("obsolete metric %q remains", key)
		}
	}
}
