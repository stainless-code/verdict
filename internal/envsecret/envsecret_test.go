package envsecret_test

import (
	"testing"

	"github.com/stainless-code/verdict/internal/envsecret"
)

// TestIsSecret pins which variable names the verify redactor and the test
// harness scrubber treat as credentials. Rows are literals from the package
// doc (suffix match, case-insensitive, trailing rotation digits/underscores
// stripped), not derived from the implementation.
//
// Mutants killed (gremlins, envsecret.go): 27:16, 29:12, 29:26 (the '9' bound,
// row API_KEY_9) and 29:41
// (rotation-strip loop conditions: the "rotated" rows stay unstripped, the
// bare-digit and empty rows loop wrongly), 28:25 (the `-1` slice step: a
// stripped name keeps its last byte), and the suffix match itself via every
// positive/negative pair.
func TestIsSecret(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		want bool
	}{
		{"GITHUB_TOKEN", true},
		{"gh_token", true},
		{"ANTHROPIC_API_KEY", true},
		{"AWS_SECRET_ACCESS_KEY", true},
		{"AWS_ACCESS_KEY_ID", true},
		{"SSH_AUTH_SOCK", true},
		{"SSH_AGENT_PID", true},
		{"DB_PASSWORD", true},
		{"SIGNING_PASSPHRASE", true},
		{"GITHUB_PAT", true},
		{"NPM_PASS", true},
		{"ADMIN_PWD", true},
		{"GOOGLE_APPLICATION_CREDENTIALS", true},
		{"NPMTOKEN", true},
		{"APIKEY", true},
		// Rotation digits and underscores are stripped before the match.
		{"VERDICT_TOKEN_2", true},
		{"VERDICT_TOKEN_2_", true},
		{"API_KEY_10", true},
		{"API_KEY_9", true},
		{"API_KEY_0", true},
		{"TOKEN_", true},
		// Names that merely resemble a suffix, or hold nothing to match.
		{"PATH", false},
		{"HOME", false},
		{"LANG", false},
		{"MAX_TOKENS", false},
		{"KEYBOARD", false},
		{"SECRETARY", false},
		{"TOKEN2X", false},
		{"", false},
		{"_", false},
		{"12345", false},
		{"EDITOR_2", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := envsecret.IsSecret(tc.name); got != tc.want {
				t.Fatalf("IsSecret(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}
