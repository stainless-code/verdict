package schemagen

import (
	"testing"

	"github.com/stainless-code/verdict/internal/schemacheck"
)

func TestPanelReceiptSchema(t *testing.T) {
	schema := loadSchemaDocument(t, "panel.schema.json")
	tests := []struct {
		name, doc string
		valid     bool
	}{
		{"valid", `{"schema_version":"1","head_sha":"head","results":[{"sig":"abc","model":"m","verdict":"refuted","evidence":"counterexample present"}]}`, true},
		{"missing evidence", `{"schema_version":"1","head_sha":"head","results":[{"sig":"abc","model":"m","verdict":"refuted"}]}`, false},
		{"unknown verdict", `{"schema_version":"1","head_sha":"head","results":[{"sig":"abc","model":"m","verdict":"maybe","evidence":"counterexample present"}]}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := schemacheck.Decode([]byte(tt.doc))
			if err != nil {
				t.Fatal(err)
			}
			got := schemacheck.Validate(schema, schema, v)
			if (len(got) == 0) != tt.valid {
				t.Fatalf("valid=%v, violations=%v", tt.valid, got)
			}
		})
	}
}
