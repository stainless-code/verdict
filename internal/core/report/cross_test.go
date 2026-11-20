package reportcore

import (
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

func TestTierGateExcludesPendingCross(t *testing.T) {
	entry := jq.NewObj("sig", "abc", "status", "open", "severity", "important", "verification",
		jq.NewObj("quote", "verified", "cross", jq.NewObj("execution_id", "execution-A", "expected", []any{"finder-2"}, "checks", []any{})))
	if tierHit(entry, 2, "") {
		t.Fatal("pending cross check counted as a confirmed blocker")
	}
}
