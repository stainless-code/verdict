package ready

import (
	"testing"

	readycore "github.com/stainless-code/verdict/internal/core/ready"
)

func TestParseFilterFlag(t *testing.T) {
	q := &readycore.Request{}
	if err := parseFlags([]string{"--filter", "--check"}, q); err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if !q.Filter || !q.Check {
		t.Fatalf("Filter=%v Check=%v, want both true", q.Filter, q.Check)
	}
}
