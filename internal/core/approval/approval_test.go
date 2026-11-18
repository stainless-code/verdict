package approval

import (
	"testing"

	"github.com/stainless-code/verdict/internal/jq"
)

// TestNoConfigDefaultThreshold pins the operator decision: with no policy,
// important and critical findings withhold approval and informational ones do
// not. Each row is a literal severity, never a value derived from the code
// under test.
func TestNoConfigDefaultThreshold(t *testing.T) {
	tests := []struct {
		name     string
		severity string
		approved bool
	}{
		{"critical withholds", "critical", false},
		{"important withholds", "important", false},
		{"informational approves", "informational", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { checkDefaultThreshold(t, tc.severity, tc.approved) })
	}
}

// checkDefaultThreshold proves one severity's default decision and the named
// tier that decided it.
func checkDefaultThreshold(t *testing.T, severity string, approved bool) {
	t.Helper()
	got := Evaluate([]any{openFinding(severity, "unchecked")}, "", "", nil, false)
	if got.Approved != approved {
		t.Fatalf("approved = %v, want %v (receipt %+v)", got.Approved, approved, got)
	}
	if got.Tier != DefaultApprovalTier {
		t.Fatalf("tier = %q, want the named default %q", got.Tier, DefaultApprovalTier)
	}
}

// openFinding builds one stored open finding with the given severity and
// quote standing.
func openFinding(severity, quote string) *jq.Obj {
	return jq.NewObj("status", "open", "severity", severity, "path", "notes.txt",
		"line", 2, "verification", jq.NewObj("quote", quote))
}

func TestUnverifiedDoesNotWithhold(t *testing.T) {
	if got := Evaluate([]any{openFinding("critical", "unverified")}, "", "", nil, false); !got.Approved {
		t.Fatalf("unverified finding withheld approval: %+v", got)
	}
}

func TestViolatedSpecCheckWithholds(t *testing.T) {
	if got := Evaluate(nil, "", "", nil, true); got.Approved {
		t.Fatalf("violated spec check approved: %+v", got)
	}
}
