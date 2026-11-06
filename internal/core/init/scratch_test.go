package initcore

import "testing"

// TestScratchRenameKeepsSharedPolicyDirty rejects removal of shared controls.
func TestScratchRenameKeepsSharedPolicyDirty(t *testing.T) {
	t.Parallel()
	tests := []struct {
		line string
		want bool
	}{
		{"R  .verdict/config.json -> .verdict/state/local-a/run.json", false},
		{"R  .verdict/state/local-a/run.json -> .verdict/config.json", false},
		{"R  .verdict/state/local-a/run.json -> .verdict/state/local-b/run.json", true},
	}
	for _, tc := range tests {
		t.Run(tc.line, func(t *testing.T) {
			t.Parallel()
			if got := isOwnScratch(tc.line); got != tc.want {
				t.Fatalf("isOwnScratch(%q) = %v; want %v", tc.line, got, tc.want)
			}
		})
	}
}
