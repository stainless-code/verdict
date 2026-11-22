package exclude

import (
	"slices"
	"testing"
)

// TestSecretSetWithholdsEveryDocumentedShape walks the built-in set itself:
// the names, extensions, Terraform states, and environment documents it
// publishes are each withheld at the root and at depth, while their
// documentation twins stay reviewable. A rule dropped from the set fails here,
// not in a fixture that happens not to name it.
func TestSecretSetWithholdsEveryDocumentedShape(t *testing.T) {
	tests := []struct {
		name, path string
		withheld   bool
	}{
		{"env at root", ".env", true},
		{"env nested", "app/.env", true},
		{"env value spelling", ".env.local", true},
		{"env nested value spelling", "app/.env.production", true},
		{"env template", ".env.example", false},
		{"env template nested", "app/.env.template", false},
		{"env defaults", ".env.defaults", false},
		{"credentials document", "credentials.json", true},
		{"nested credentials document", "secrets/credentials.json", true},
		{"kubeconfig", "kubeconfig", true},
		{"nested kubeconfig", "deploy/kubeconfig", true},
		{"netrc", ".netrc", true},
		{"npmrc", "pkg/.npmrc", true},
		{"pypirc", ".pypirc", true},
		{"envrc", ".envrc", true},
		{"pgpass", ".pgpass", true},
		{"htpasswd", "conf/.htpasswd", true},
		{"rsa key", "keys/id_rsa", true},
		{"ed25519 key", "id_ed25519", true},
		{"public rsa key", "keys/id_rsa.pub", false},
		{"pem", "certs/server.pem", true},
		{"key", "tls/client.key", true},
		{"p12", "certs/bundle.p12", true},
		{"pfx", "certs/bundle.pfx", true},
		{"jks", "certs/store.jks", true},
		{"ppk", "keys/putty.ppk", true},
		{"keystore", "certs/app.keystore", true},
		{"terraform state", "prod.tfstate", true},
		{"terraform state backup", "prod.tfstate.backup", true},
		{"terraform state at depth", "infra/prod.tfstate.20260101", true},
		{"state named like a template", "prod.tfstate.env.example", true},
		{"ordinary source", "internal/app.go", false},
		{"ordinary docs", "README.md", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertSecretMatch(t, tt.path, tt.withheld)
		})
	}
}

// assertSecretMatch proves one path's standing under the built-in set alone,
// with a fresh copy and with no policy layer in reach.
func assertSecretMatch(t *testing.T, path string, withheld bool) {
	t.Helper()
	rule, dropped := SecretRule(Secrets(), path, "")
	if dropped != withheld {
		t.Fatalf("SecretRule(%q) = %v/%v; want withheld=%v", path, rule, dropped, withheld)
	}
	if dropped && (rule.ID != SecretID || rule.Reason != SecretReason) {
		t.Fatalf("SecretRule(%q) = %+v; want %s/%s", path, rule, SecretID, SecretReason)
	}
}

// TestSecretRuleFoldsCaseAndCleansThePath proves the floor is not a naming
// convention: a rename that only changes letter case, or a path reaching the
// matcher through `.` and doubled separators, still finds its rule.
func TestSecretRuleFoldsCaseAndCleansThePath(t *testing.T) {
	for _, path := range []string{
		".ENV", ".Env.local", "App/.ENV.PRODUCTION", "ID_RSA", "Keys/Id_Rsa",
		"Server.PEM", "private.KEY", "Credentials.json", "KUBECONFIG",
		"prod.TFSTATE", "PROD.TFSTATE.BACKUP", "certs/STORE.JKS", "conf/.HTPASSWD",
		"./.env", "app//.env", "keys/../keys/id_rsa", "pkg/./credentials.json",
	} {
		assertSecretMatch(t, path, true)
	}
	for _, path := range []string{
		".env.EXAMPLE", ".env.Example", "app/.ENV.SAMPLE", "id_rsa.pub", "README.MD",
	} {
		assertSecretMatch(t, path, false)
	}
}

// TestSecretRuleProbesBothSidesOfARename proves neither side of a rename can
// carry a secret past the floor: renaming a secret to a harmless name and
// renaming a harmless name onto a secret path are both withheld, and the row
// keeps the path the rule matched.
func TestSecretRuleProbesBothSidesOfARename(t *testing.T) {
	tests := []struct{ name, path, old string }{
		{"out of a secret", "notes.txt", ".env"},
		{"onto a secret", ".ENV", "notes.txt"},
		{"template onto a secret", "app/.ENV.EXAMPLE", "app/.env"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule, dropped := SecretRule(Secrets(), tt.path, tt.old)
			if !dropped || rule.ID != SecretID {
				t.Fatalf("SecretRule(%q, %q) = %+v/%v; want the secret rule", tt.path, tt.old, rule, dropped)
			}
		})
	}
	if _, dropped := SecretRule(Secrets(), "app/.env.example", "docs/.env.example"); dropped {
		t.Fatal("a template renamed onto a template was withheld; want kept")
	}
}

// TestSecretsHandsOutACopy proves the caller owns what it gets: mutating the
// returned slice must not reach the next pass, or one filtered run would
// silently rewrite the built-in floor for every later run in the process.
func TestSecretsHandsOutACopy(t *testing.T) {
	first := Secrets()
	if len(first) == 0 {
		t.Fatal("Secrets() is empty")
	}
	first[0].Pattern = "rewritten"
	first[0].Negate = !first[0].Negate
	second := Secrets()
	if second[0].Pattern == "rewritten" {
		t.Fatal("Secrets() shares its backing array with the caller")
	}
	if !slices.ContainsFunc(second, func(r Rule) bool { return r.ID == SecretID && r.Negate }) {
		t.Fatal("Secrets() has no template negation; want the documented exceptions")
	}
}

// TestSecretsOrderPutsNegationsLast proves the set relies on last-match-wins:
// every excluded pattern precedes every template negation, so a template is
// re-included after the value-spelling rule removed it.
func TestSecretsOrderPutsNegationsLast(t *testing.T) {
	rules := Secrets()
	firstNegation := slices.IndexFunc(rules, func(r Rule) bool { return r.Negate })
	if firstNegation < 0 {
		t.Fatal("Secrets() has no negation")
	}
	if slices.ContainsFunc(rules[firstNegation:], func(r Rule) bool { return !r.Negate }) {
		t.Fatal("Secrets() puts an exclusion after a negation; the template would be lost")
	}
}
