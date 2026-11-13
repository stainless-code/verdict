package schemagen

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/schemacheck"
)

func TestProducedExcludedReceiptSchema(t *testing.T) {
	raw, err := os.ReadFile("../core/record/testdata/suppression/excluded.json")
	if err != nil {
		t.Fatal(err)
	}
	var receipt core.ExcludedFile
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	doc := loadSchemaDocument(t, "excluded.schema.json")
	validateWire(t, doc, "$", doc, decodeWire(t, core.Wire(&receipt)))
	if len(receipt.Excluded) != 1 {
		t.Fatal("producer fixture requires one exclusion")
	}
	receipt.Excluded[0].Bytes = -1
	if len(schemacheck.Validate(doc, doc, decodeWire(t, core.Wire(&receipt)))) == 0 {
		t.Fatal("schema accepted negative excluded bytes")
	}
}
