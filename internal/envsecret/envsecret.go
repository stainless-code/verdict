// Package envsecret classifies environment-variable names as secret-shaped,
// and owns the one scrubber that hides a collected credential value in text.
// The verify redactor, the launcher's tail scrub, and the test harnesses'
// environment scrubber share it, so "which variables carry credentials" and
// "how a value is hidden once found" each have one answer.
package envsecret

import "strings"

// IsSecret reports whether an environment variable name looks like it carries
// a credential: a case-insensitive suffix match after trailing rotation digits
// and underscores are stripped. The suffix list is deliberately suffix-shaped
// (not an exact allowlist) so a new *_TOKEN or *_API_KEY is dropped without a
// code change.
func IsSecret(name string) bool {
	lower := strings.ToLower(stripRotationSuffix(name))
	for _, suffix := range suffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// stripRotationSuffix removes trailing rotation digits and underscores
// (VERDICT_TOKEN_2 → VERDICT_TOKEN) before the suffix match.
func stripRotationSuffix(name string) string {
	norm := name
	for len(norm) > 0 {
		last := norm[len(norm)-1]
		if (last < '0' || last > '9') && last != '_' {
			break
		}
		norm = norm[:len(norm)-1]
	}
	return norm
}

// suffixes are the credential-shaped endings; the bare forms catch
// ANTHROPIC_API_KEY, NPM_TOKEN and friends, the _-prefixed forms catch
// GH_TOKEN style names without swallowing unrelated words.
var suffixes = []string{
	"_token", "_secret", "_key", "_password", "_passphrase", "_pat",
	"_auth", "_credential", "_credentials", "_pass", "_pwd",
	"token", "secret", "key", "password",
	"aws_access_key_id", "ssh_auth_sock", "ssh_agent_pid",
}
