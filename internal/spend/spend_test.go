package spend

import (
	"slices"
	"testing"
)

func phase(id string, usd *float64) Phase {
	return Phase{ExecutionID: "execution-A", LaunchID: id, Finder: "finder-1", Phase: "discovery", USD: usd}
}

func TestProjectionPreservesUnknown(t *testing.T) {
	projection, code := Project("execution-A", []Phase{phase("known", new(0.2)), phase("unknown", nil)})
	if code != "" || projection.Total != nil || projection.KnownSubtotal == nil || *projection.KnownSubtotal != 0.2 {
		t.Fatalf("unknown became a total: %+v, %s", projection, code)
	}
	if !slices.Equal(projection.Unknown, []string{"unknown"}) || projection.InputTokens != nil {
		t.Fatalf("missing usage became zero: %+v", projection)
	}
}

func TestProjectionAddsExactDecimalCosts(t *testing.T) {
	phases := []Phase{phase("one", new(0.1)), phase("two", new(0.2))}
	phases[0].InputTokens = new(int64(10))
	phases[1].InputTokens = new(int64(20))
	projection, code := Project("execution-A", phases)
	if code != "" || projection.Total == nil || *projection.Total != 0.3 {
		t.Fatalf("decimal sum = %+v, %s", projection, code)
	}
	if projection.InputTokens == nil || *projection.InputTokens != 30 || projection.OutputTokens != nil {
		t.Fatalf("token rollup = %+v", projection)
	}
}

func TestProjectionRejectsDuplicateLaunch(t *testing.T) {
	_, code := Project("execution-A", []Phase{phase("same", new(1.0)), phase("same", new(2.0))})
	if code != "bad-phase-usage" {
		t.Fatalf("duplicate launch counted: %q", code)
	}
}

func TestProjectionRejectsForeignExecution(t *testing.T) {
	_, code := Project("execution-B", []Phase{phase("launch", new(1.0))})
	if code != "bad-phase-usage" {
		t.Fatalf("foreign execution counted: %q", code)
	}
}

func TestAuthorization(t *testing.T) {
	cases := []struct {
		name       string
		code       string
		projection Projection
		policy     Policy
		consent    bool
		receipt    bool
	}{
		{name: "over-budget", projection: Projection{Total: new(2.0)}, policy: Policy{Budget: "1"}, code: "budget-exceeded"},
		{name: "consented", projection: Projection{Total: new(2.0)}, policy: Policy{Budget: "1"}, consent: true, receipt: true},
		{name: "personal-lower-bound", projection: Projection{KnownSubtotal: new(2.0)}, policy: Policy{Personal: "1"}, code: "spend-capped"},
		{name: "unknown", policy: Policy{RequireKnown: true}, code: "budget-unknown"},
		{name: "blind-consent", policy: Policy{RequireKnown: true}, consent: true, receipt: true},
		{name: "hard-cap", policy: Policy{EnforceCap: true}, consent: true, code: "unsupported-cap"},
		{name: "decimal-boundary", projection: Projection{Total: new(0.3)}, policy: Policy{Budget: "0.3"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Authorize(tc.projection, tc.policy, tc.consent)
			if got.Code != tc.code || got.Receipt != tc.receipt {
				t.Fatalf("authorization = %+v; want code %q receipt %t", got, tc.code, tc.receipt)
			}
		})
	}
}

func TestTopologyKeepsCrossAndRepairUnknown(t *testing.T) {
	phases, code := CompletePlan("execution-A", 2, []Phase{phase("finder-1-discovery-1", new(0.5))})
	if code != "" || len(phases) != 8 {
		t.Fatalf("bounded topology = %d, %s", len(phases), code)
	}
	projection, code := Project("execution-A", phases)
	if code != "" || projection.Total != nil || len(projection.Unknown) != 7 {
		t.Fatalf("unpriced cross/repair invented: %+v, %s", projection, code)
	}
}

func TestMeanPreservesFetchRounding(t *testing.T) {
	mean, literal := MeanUSD(1, 3)
	if mean != 0.3333333333333333 || literal != "0.3333" {
		t.Fatalf("fetch mean = %v, %q", mean, literal)
	}
}
