package schemagen

import (
	"encoding/json"
	"testing"
)

const (
	drainSchema       = "drain.schema.json"
	drainDryRunSchema = "drain-dryrun.schema.json"
)

func TestDrainDefsAreClosed(t *testing.T) {
	assertGeneratedDefsClosed(t, drainSchema)
}

func TestDrainDryRunDefsAreClosed(t *testing.T) {
	assertGeneratedDefsClosed(t, drainDryRunSchema)
}

func assertGeneratedDefsClosed(t *testing.T, name string) {
	t.Helper()
	files, err := Generate()
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.Name == name {
			assertClosedDefs(t, file.Data, name)
			return
		}
	}
	t.Fatal(name + " was not generated")
}

func schemaDefs(t *testing.T, data []byte, name string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	defs, ok := doc["$defs"].(map[string]any)
	if !ok {
		t.Fatal(name + " has no $defs")
	}
	return defs
}

func assertClosedDefs(t *testing.T, data []byte, name string) {
	t.Helper()
	defs := schemaDefs(t, data, name)
	if len(defs) == 0 {
		t.Fatal(name + " has no $defs")
	}
	for name, raw := range defs {
		def, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("$defs.%s is not an object", name)
		}
		if def["additionalProperties"] != false {
			t.Errorf("$defs.%s additionalProperties = %v, want false", name, def["additionalProperties"])
		}
	}
}
