package fetchcore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

func TestPhaseHistoryDoesNotInventRootPrice(t *testing.T) {
	raw := []byte(`{"effort":"standard","est_usd":0,"execution_id":"execution-A","phases":[{"execution_id":"execution-A","launch_id":"one","finder":"finder-1","phase":"discovery","usd":null,"input_tokens":null,"output_tokens":null}]}`)
	doc, err := jq.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	record := costRecord(doc)
	if record.has || record.usd != "" {
		t.Fatalf("unpriced phase became free: %+v", record)
	}
}

func TestPhaseHistoryCountsOnlyPhaseRollup(t *testing.T) {
	raw := []byte(`{"effort":"standard","est_usd":99,"execution_id":"execution-A","phases":[{"execution_id":"execution-A","launch_id":"one","finder":"finder-1","phase":"discovery","usd":0.3,"input_tokens":null,"output_tokens":null}]}`)
	doc, err := jq.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	record := costRecord(doc)
	if !record.has || record.usd != "0.3" {
		t.Fatalf("root aggregate counted beside phases: %+v", record)
	}
}

func TestPhaseHistoryKnownSubtotalEnforcesBudget(t *testing.T) {
	r := knownCostSession(t, "2")
	raw := []byte(`{"effort":"standard","est_usd":2,"execution_id":"execution-A","phases":[{"execution_id":"execution-A","launch_id":"one","finder":"finder-1","phase":"discovery","usd":2},{"execution_id":"execution-A","launch_id":"two","finder":"finder-2","phase":"discovery","usd":null}]}`)
	if err := os.WriteFile(filepath.Join(".verdict", "state", "1", "cost.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	st := budgetState{hasBudget: true, budgetUSD: "1"}
	err := budgetGate(r, &st, nil, false)
	var ref *core.RefusalError
	if !errors.As(err, &ref) || ref.Code != "budget-exceeded" || st.estUSD != "" {
		t.Fatalf("budget = %v, total = %q", err, st.estUSD)
	}
}
