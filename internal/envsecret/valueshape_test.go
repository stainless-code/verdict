package envsecret

import (
	"slices"
	"testing"
)

// TestSecretValueShapes pins one literal per shape, so a provider row that
// drifts (a lost quantifier, a narrowed class) fails here instead of shipping a
// scan that no longer recognizes the credential it exists to catch.
func TestSecretValueShapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		text string
		want []string
	}{
		{"private key header", "-----BEGIN RSA PRIVATE KEY-----\nMIIEow", []string{"private-key"}},
		{"openssh key header", "-----BEGIN OPENSSH PRIVATE KEY-----", []string{"private-key"}},
		{"aws key id", `const id = "AKIAIOSFODNN7EXAMPLE"`, []string{"aws-access-key-id"}},
		{"aws temp key id", "ASIAIOSFODNN7EXAMPLE", []string{"aws-access-key-id"}},
		{"github token", "ghp_0123456789abcdefghijABCDEFGHIJ012345", []string{"github-token"}},
		{"slack token", "xoxb-123456789012-abcdefghij", []string{"slack-token"}},
		{"google api key", "AIzaSyA1234567890abcdefghijklmnopqrstuv", []string{"google-api-key"}},
		{"stripe live key", "sk_live_abcdef0123456789", []string{"stripe-key"}},
		{"anthropic key", "sk-ant-api03-abcdefghij0123", []string{"anthropic-api-key"}},
		{"openai scoped key", "sk-proj-abcdefghij0123456789", []string{"openai-scoped-key"}},
		{"openai api key", "sk-abcdefghij0123456789abcdefghij0123456789", []string{"openai-api-key"}},
		{
			// Two shapes in one text report in table order, not text order.
			name: "two shapes",
			text: "ghp_0123456789abcdefghijABCDEFGHIJ012345 and AKIAIOSFODNN7EXAMPLE",
			want: []string{"aws-access-key-id", "github-token"},
		},
		{"prose", "the token is loaded from the environment", nil},
		{"short aws prefix", "AKIAIOSFODNN7", nil},
		{"short github prefix", "ghp_abc", nil},
		{"certificate header", "-----BEGIN CERTIFICATE-----", nil},
		{"empty", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := SecretValueShapes(c.text)
			if !slices.Equal(got, c.want) {
				t.Fatalf("SecretValueShapes(%q) = %v, want %v", c.text, got, c.want)
			}
			if has := HasSecretValue(c.text); has != (len(c.want) > 0) {
				t.Fatalf("HasSecretValue(%q) = %v, want %v", c.text, has, len(c.want) > 0)
			}
		})
	}
}
