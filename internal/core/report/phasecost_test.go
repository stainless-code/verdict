package reportcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

func TestCostMappingRetainsUnknownTokens(t *testing.T) {
	cost := costFromObj(jq.NewObj("runs", 2, "input_tokens", nil, "output_tokens", nil, "total_tokens", nil, "cached_input_tokens", nil))
	if cost.InputTokens != nil || cost.OutputTokens != nil || cost.TotalTokens != nil || cost.CachedInputTokens != nil {
		t.Fatalf("unknown usage became measured: %+v", cost)
	}
}

func TestCostMappingRetainsKnownInt64PhaseTokens(t *testing.T) {
	cost := costFromObj(jq.NewObj("input_tokens", int64(30), "output_tokens", int64(5), "total_tokens", int64(35), "cached_input_tokens", 0))
	if cost.InputTokens == nil || *cost.InputTokens != 30 || cost.TotalTokens == nil || *cost.TotalTokens != 35 ||
		cost.CachedInputTokens == nil || *cost.CachedInputTokens != 0 {
		t.Fatalf("known usage lost: %+v", cost)
	}
}
