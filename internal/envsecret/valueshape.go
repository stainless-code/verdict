package envsecret

import "regexp"

// valueShape is one secret-shaped value literal: the name a warning or a
// refusal reports and the expression that recognizes it.
type valueShape struct {
	name string
	re   *regexp.Regexp
}

// valueShapes are the literal credential shapes text can carry, in report
// order. Two readers share the table so a new provider shape reaches both
// without a second edit: the fetch sensitive-content scan that flags a bundle
// path, and post's pre-submit scan that refuses to publish one.
//
// Each row is one top-level alternative of the expression the table replaced,
// so "does this text carry a secret-shaped value" answers the same either way.
// The names are the published vocabulary a warning quotes; the matched bytes
// never leave this package.
var valueShapes = []valueShape{
	{"private-key", regexp.MustCompile(`-----BEGIN (RSA |EC |DSA |OPENSSH |ENCRYPTED )?PRIVATE KEY-----`)},
	{"aws-access-key-id", regexp.MustCompile(`(AKIA|ASIA)[A-Za-z0-9]{16}`)},
	{"github-token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}`)},
	{"slack-token", regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`)},
	{"google-api-key", regexp.MustCompile(`AIza[A-Za-z0-9_-]{35}`)},
	{"stripe-key", regexp.MustCompile(`(sk|rk)_(live|test)_[A-Za-z0-9]{16,}`)},
	{"anthropic-api-key", regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{20,}`)},
	{"openai-scoped-key", regexp.MustCompile(`sk-(proj|svcacct|admin)-[A-Za-z0-9]{20,}`)},
	{"openai-api-key", regexp.MustCompile(`sk-[A-Za-z0-9]{40}`)},
}

// SecretValueShapes reports the names of the secret value shapes text carries,
// in table order and each once; nil when it carries none. A caller that has to
// say where a credential sits names the shape and the location, never the
// value: the shape alone tells an operator what to rotate.
func SecretValueShapes(text string) []string {
	var out []string
	for _, shape := range valueShapes {
		if shape.re.MatchString(text) {
			out = append(out, shape.name)
		}
	}
	return out
}

// HasSecretValue reports whether text carries any secret-shaped value. It is
// [SecretValueShapes] without the names, for the callers that only gate.
func HasSecretValue(text string) bool {
	for _, shape := range valueShapes {
		if shape.re.MatchString(text) {
			return true
		}
	}
	return false
}
