package fetchcore

import (
	"testing"

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
