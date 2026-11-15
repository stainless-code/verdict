package kb_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/kb"
)

func TestBuiltinPolicyDefaults(t *testing.T) {
	policy := kb.New(nil).Policy
	if policy.MinJudged != 5 || policy.MaxFPRate != "0.5" {
		t.Errorf("built-in policy = %d/%q; want 5/\"0.5\"", policy.MinJudged, policy.MaxFPRate)
	}
}
