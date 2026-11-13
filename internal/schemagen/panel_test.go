package schemagen

import (
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// TestPanelReceiptSchema validates required evidence and the verdict enum.
func TestPanelReceiptSchema(t *testing.T) {
	schema := loadSchemaDocument(t, "panel.schema.json")
	tests := []struct {
		name, doc string
		valid     bool
	}{
		{"valid", `{"schema_version":"1","head_sha":"head","results":[{"sig":"abc","model":"m","verdict":"refuted","evidence":"counterexample present"}]}`, true},
		{"long evidence", `{"schema_version":"1","head_sha":"head","results":[{"sig":"abc","model":"m","verdict":"refuted","evidence":"` + strings.Repeat("x", 501) + `"}]}`, false},
		{"missing evidence", `{"schema_version":"1","head_sha":"head","results":[{"sig":"abc","model":"m","verdict":"refuted"}]}`, false},
		{"unknown verdict", `{"schema_version":"1","head_sha":"head","results":[{"sig":"abc","model":"m","verdict":"maybe","evidence":"counterexample present"}]}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parityViolations(t, schema, tt.doc)
			if (len(got) == 0) != tt.valid {
				t.Fatalf("valid=%v, violations=%v", tt.valid, got)
			}
		})
	}
}

// TestPanelReportSummarySchema pins the report's generated verifier profile.
func TestPanelReportSummarySchema(t *testing.T) {
	schema := loadSchemaDocument(t, "envelope.schema.json")
	response := runReportFixture(t)
	response.Data.Verification = &core.PanelSummary{Verifiers: 2, Confirmed: 1, Contested: 1, Single: 0}
	validateWire(t, schema, "$", defOf(t, schema, "reportData"), decodeWire(t, response.Data.Wire()))
}
