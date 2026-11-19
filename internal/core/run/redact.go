package run

import "github.com/stainless-code/verdict/internal/argvshape"

const (
	// promptPlaceholder stands in for the directive in the recorded argv.
	promptPlaceholder = argvshape.PromptPlaceholder
	// redactedValue replaces a secret-shaped argument.
	redactedValue = argvshape.RedactedValue
)

// recordArgv delegates to [argvshape.Record] so the launcher and the bundle
// journal share one credential redaction rule.
func recordArgv(argv []string, prompt string, environ []string) (shape, secrets []string) {
	return argvshape.Record(argv, prompt, environ)
}

// secretValues delegates to [argvshape.SecretValues].
func secretValues(environ []string) []string {
	return argvshape.SecretValues(environ)
}

// isSecretValue delegates to [argvshape.IsSecretValue].
func isSecretValue(arg string, secrets []string) bool {
	return argvshape.IsSecretValue(arg, secrets)
}
