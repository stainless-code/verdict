package verdict_test

import (
	"context"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	verdict "github.com/stainless-code/verdict/pkg/verdict"
)

// TestClientConfigExplain proves the read-only policy surface through the
// SDK: the stored bundle explains with its trust bit and origins, and the
// data matches the envelope profile byte for byte.
func TestClientConfigExplain(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	guard := 9.5
	rs := &config.RunState{
		BaseSHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Trusted: true,
		Evidence: config.Evidence{
			SchemaVersion: "1",
			BaseSHA:       "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			Plan:          config.Plan{Status: config.PendingStatus},
			Values: []config.EvidenceValue{
				{Key: "budget_usd", Value: 9.5, Origin: config.FieldOrigin{Layer: config.LayerEnv, Source: "REVIEW_BUDGET_USD"}},
			},
		},
		Resolved: &config.Resolved{
			Plan:     config.Plan{Status: config.PendingStatus},
			Defaults: config.ResolvedDefaults{Values: config.EffectiveDefaults{BudgetUSD: &guard}},
		},
	}
	if serr := config.WriteRunState(".", "7", rs); serr != nil {
		t.Fatalf("WriteRunState: %v", serr)
	}
	c := newSDKClient(nil, nil)
	defer c.Close()
	resp, err := c.Config(context.Background(), &verdict.ConfigRequest{
		Invocation: sdkInvocation("7"), Explain: true,
	})
	requireNoError(t, err)
	if resp.At != sdkAt || resp.PR != "7" {
		t.Errorf("envelope = at %q pr %q", resp.At, resp.PR)
	}
	doc := decodeData(t, resp.Data)
	if doc["mode"] != "explain" || doc["trusted"] != true {
		t.Errorf("data = %v, want explain/trusted", doc)
	}
	values, ok := doc["values"].([]any)
	if !ok || len(values) != 1 {
		t.Fatalf("values = %v, want the stored budget", doc["values"])
	}
	first, ok := values[0].(map[string]any)
	if !ok || first["key"] != "budget_usd" || first["source"] != "REVIEW_BUDGET_USD" {
		t.Errorf("value = %v, want the stored budget with its origin", values[0])
	}

	_, err = c.Config(context.Background(), &verdict.ConfigRequest{
		Invocation: sdkInvocation("99"), Explain: true,
	})
	requireRefusal(t, err, "config-no-bundle", "try: run the fetch/record step named above first, then re-run")
}
