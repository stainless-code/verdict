package recordcore

import (
	"errors"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

func TestMergeRejectsV2WithoutExecution(t *testing.T) {
	err := checkReviewSchema(jq.NewObj("schema_version", "2", "findings", []any{}))
	var ref *core.RefusalError
	if !errors.As(err, &ref) || ref.Code != "bad-review-schema" {
		t.Fatalf("v2 without execution returned %v", err)
	}
}
