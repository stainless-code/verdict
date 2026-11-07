package fetchcore

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
)

// TestGroupEstimateWithoutGuardsIsJSON checks the emitted JSON bytes.
func TestGroupEstimateWithoutGuardsIsJSON(t *testing.T) {
	report := config.GroupSpendReport{
		Groups: []config.GroupSpend{{GroupID: "group-a", Effort: "standard"}},
	}
	raw := groupedEstimateObject(&report).MarshalCompact()
	if !json.Valid([]byte(raw)) {
		t.Fatalf("projection is invalid JSON: %s", raw)
	}
	for _, absent := range []string{"guard_usd", "limit_usd", "projected_usd", "total_usd"} {
		if strings.Contains(raw, absent) {
			t.Errorf("unset %s was emitted: %s", absent, raw)
		}
	}
}
