package run

import (
	"strings"

	"github.com/stainless-code/verdict/internal/envsecret"
)

const (
	// promptPlaceholder stands in for the directive in the recorded argv: it is
	// tool-generated prose of a few hundred bytes, identical for every run of the
	// same bundle, and not what the provenance record exists to explain.
	promptPlaceholder = "<prompt>"
	// redactedValue replaces a secret-shaped argument.
	redactedValue = "***REDACTED***"
	// secretMinLen is the value length below which an environment match is not a
	// credential worth scrubbing (the verify tail redactor uses the same bound).
	secretMinLen = 8
)

// argvShape records the argv the launcher spawned without the prompt body and
// without secrets: the directive becomes [promptPlaceholder], the operand after
// a secret-shaped flag is replaced (both `--flag value` and `--flag=value`), and
// so is any argument that equals a value the operator's environment holds under
// a secret name. Over-redaction is acceptable in a provenance record; a leaked
// model key is not.
func argvShape(argv []string, prompt string, environ []string) []string {
	secrets := secretValues(environ)
	shape := make([]string, 0, len(argv))
	// operandOfSecretFlag marks the argument that follows a secret-shaped flag,
	// which is the credential itself; it is cleared before the next flag so a
	// redacted operand cannot cascade into the argument after it.
	operandOfSecretFlag := false
	for _, arg := range argv {
		redactedSeparate := operandOfSecretFlag
		operandOfSecretFlag = !operandOfSecretFlag && namesSecret(arg)
		switch {
		case arg == prompt:
			shape = append(shape, promptPlaceholder)
		case redactedSeparate, isSecretValue(arg, secrets):
			shape = append(shape, redactedValue)
		default:
			shape = append(shape, redactFlagValue(arg))
		}
	}
	return shape
}

// namesSecret reports whether an argument is a flag whose operand is a
// credential: `--api-key`, `--token=...`, `-H` short of it stays readable
// unless the long name is secret-shaped.
func namesSecret(arg string) bool {
	if !strings.HasPrefix(arg, "-") {
		return false
	}
	name, _, _ := strings.Cut(arg, "=")
	return envsecret.IsSecret(strings.TrimLeft(name, "-"))
}

// redactFlagValue rewrites the value of an inline `--flag=value` secret flag.
func redactFlagValue(arg string) string {
	name, value, ok := strings.Cut(arg, "=")
	if !ok || value == "" || !namesSecret(name) {
		return arg
	}
	return name + "=" + redactedValue
}

// secretValues collects the environment values that live under a secret name.
func secretValues(environ []string) []string {
	var values []string
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || len(value) < secretMinLen || !envsecret.IsSecret(name) {
			continue
		}
		values = append(values, value)
	}
	return values
}

// isSecretValue reports whether arg is one of the collected secret values.
func isSecretValue(arg string, secrets []string) bool {
	for _, value := range secrets {
		if arg == value {
			return true
		}
	}
	return false
}
