package recordcore_test

import (
	"context"
	"strings"
	"testing"

	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/testkit"
)

func TestRecordOmitsSchemaVersionWarning(t *testing.T) {
	review := `{"body":"> [!IMPORTANT]\n> Record service check with enough length.","approved":false,"comments":[]}`
	bundle(t, "local-noschema", review)
	c := &testkit.Collector{}
	resp, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-noschema"))
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	for _, w := range resp.Warnings {
		if strings.Contains(w, "schema_version") {
			t.Errorf("warnings = %q, want none about the optional schema_version", resp.Warnings)
		}
	}
}
