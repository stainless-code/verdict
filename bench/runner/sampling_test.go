package main

import "testing"

func TestSampleSpreadZeroHalfOneMedianHalf(t *testing.T) {
	zero, half, one := 0.0, 0.5, 1.0
	s := stat([]*float64{&one, &zero, &half, nil})
	if s.N != 3 || *s.Median != 0.5 || *s.Min != 0 || *s.Max != 1 {
		t.Fatalf("spread = %+v; want n=3 median=.5 min=0 max=1", s)
	}
}

func TestSamplingFlagsThreeRepeats(t *testing.T) {
	cfg, err := parseFlags([]string{"-model", "fixture", "-agent", "true", "-repeats", "3", "-k", "3"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.K != 3 || cfg.Runs != 3 {
		t.Fatalf("k=%d repeats=%d; want 3,3", cfg.K, cfg.Runs)
	}
}

func TestSampleSpreadPreservesMicroUSD(t *testing.T) {
	one, two, three := 0.000001, 0.000002, 0.000003
	got := stat([]*float64{&three, &one, &two})
	if *got.Min != 0.000001 || *got.Median != 0.000002 || *got.Max != 0.000003 {
		t.Fatalf("cost spread min=%g median=%g max=%g; want .000001/.000002/.000003", *got.Min, *got.Median, *got.Max)
	}
}
