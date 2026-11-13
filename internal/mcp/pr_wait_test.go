package mcp

import (
	"strings"
	"testing"

	readycore "github.com/stainless-code/verdict/internal/core/ready"
)

func TestPRWaitTimeoutCap(t *testing.T) {
	req := &readycore.WaitRequest{Timeout: "51s"}
	if err := wirePRWait(t.Context(), &bindings{}, req); err == nil || !strings.Contains(err.Error(), "50s") {
		t.Fatalf("got %v; want 50s cap refusal", err)
	}
}
