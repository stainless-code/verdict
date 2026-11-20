package recordcore_test

import (
	"context"
	"strings"
	"testing"

	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/testkit"
)

func TestRecordPlainBodySkipsBasisWarning(t *testing.T) {
	plain := `{"schema_version":"1","body":"A plain body with enough length to pass the guard, no quote lead.",` +
		`"approved":false,"comments":[]}`
	bundle(t, "local-plainbody", plain)
	c := &testkit.Collector{}
	resp, ref := recordcore.Run(context.Background(), recordRequest(t, c, "local-plainbody"))
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	for _, w := range resp.Warnings {
		if strings.Contains(w, "Basis line") {
			t.Errorf("warnings = %q, want no basis-insertion warning", resp.Warnings)
		}
	}
}
