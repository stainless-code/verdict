package config

import (
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

func TestPhaseUsageMustBindExecution(t *testing.T) {
	record := map[string]any{"execution_id": "execution-A", "lenses": []any{}, "phases": []any{
		map[string]any{"execution_id": "execution-B", "launch_id": "one", "finder": "finder-1", "phase": "discovery", "usd": nil, "input_tokens": nil, "output_tokens": nil},
	}}
	_, state, _ := certifiedEntries(record, "2")
	if state != "malformed" {
		t.Fatalf("foreign phase usage certified: %q", state)
	}
}

func TestPhaseCostBindsCapturedExecution(t *testing.T) {
	capture := &RunState{Name: "7", InputsSHA256: "inputs", Execution: &core.ExecutionReference{ID: "execution-A", Finders: 2}}
	record := map[string]any{"run": "7", "inputs_sha256": "inputs", "execution_id": "execution-B", "phases": []any{}}
	if bindsCapture(record, capture) {
		t.Fatal("phase costs from another execution bound to capture")
	}
}
