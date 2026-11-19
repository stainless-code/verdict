// Package argvshape redacts argv slices for audit records (the launcher's
// launcher.json and the bundle journal's journal.jsonl) using one shared
// credential rule so recorded command lines and scrubbed output tails never
// disagree about what a credential is.
package argvshape

import (
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/envsecret"
)

const (
	// PromptPlaceholder stands in for a non-empty prompt body in the recorded
	// argv: it is tool-generated prose of a few hundred bytes, identical for
	// every run of the same bundle, and not what an audit record exists to
	// explain.
	PromptPlaceholder = "<prompt>"
	// RedactedValue replaces a secret-shaped argument. It is the same marker
	// every scrubber in the binary writes, so an argv redaction and a tail
	// redaction are never distinguishable in the record.
	RedactedValue = envsecret.Redacted
	// secretMinLen is the value length below which an environment match is not a
	// credential worth scrubbing (the verify tail redactor uses the same bound).
	secretMinLen = 8
	// envSeparator is the key/value separator in one environ entry.
	envSeparator = "="
)

// Record records argv without a non-empty prompt body and without secrets: an
// element matching prompt becomes [PromptPlaceholder], the operand after a
// secret-shaped flag is replaced (both `--flag value` and `--flag=value`), and
// so is any argument that equals a value the operator's environment holds under
// a secret name. Over-redaction is acceptable in an audit record; a leaked
// credential is not.
//
// It also returns the credential values it hid, so a caller can scrub child
// output with the same answer: a credential typed on the command line may be in
// neither the environment nor the prompt, and a child that echoes its own argv
// prints it verbatim. One walk feeds both scrubs, so the recorded argv and any
// scrubbed output can never disagree about what a credential is.
func Record(argv []string, prompt string, environ []string) (shape, secrets []string) {
	known := SecretValues(environ)
	shape = make([]string, 0, len(argv))
	// operandOfSecretFlag marks the argument that follows a secret-shaped flag,
	// which is the credential itself; it is cleared before the next flag so a
	// redacted operand cannot cascade into the argument after it.
	operandOfSecretFlag := false
	for i, arg := range argv {
		redactedSeparate := operandOfSecretFlag
		// The next argument is the operand only when it is not itself a flag:
		// `--token --api-key VALUE` has an empty operand for --token, and
		// consuming --api-key as its value would leak VALUE verbatim.
		operandOfSecretFlag = !operandOfSecretFlag && namesSecret(arg) && nextIsOperand(argv, i)
		rendered, secret, hid := recordArg(arg, prompt, known, redactedSeparate)
		shape = append(shape, rendered)
		if hid {
			secrets = append(secrets, secret)
		}
	}
	return shape, secrets
}

// recordArg renders one argument and reports any credential value it hid.
func recordArg(arg, prompt string, known []string, redactedSeparate bool) (rendered, secret string, hid bool) {
	if prompt != "" && arg == prompt {
		// The directive is tool-generated prose, not a credential, and is
		// never collected even when a flag precedes it: scrubbing hundreds
		// of bytes of prose out of the tail would erase the tail.
		return PromptPlaceholder, "", false
	}
	if redactedSeparate || IsSecretValue(arg, known) {
		return RedactedValue, arg, true
	}
	if name, value, ok := inlineSecretValue(arg); ok {
		return name + envSeparator + RedactedValue, value, true
	}
	return arg, "", false
}

// SecretValues collects the environment values that live under a secret name
// and meet the minimum length floor.
func SecretValues(environ []string) []string {
	var values []string
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, envSeparator)
		if !ok || len(value) < secretMinLen || !envsecret.IsSecret(name) {
			continue
		}
		values = append(values, value)
	}
	return values
}

// IsSecretValue reports whether arg is one of the collected secret values.
func IsSecretValue(arg string, secrets []string) bool {
	return slices.Contains(secrets, arg)
}

// nextIsOperand reports whether argv has an argument after i that is not
// itself a flag, so a secret-shaped flag followed by another flag redacts
// nothing rather than swallowing the next flag's operand.
func nextIsOperand(argv []string, i int) bool {
	return i+1 < len(argv) && !strings.HasPrefix(argv[i+1], "-")
}

// namesSecret reports whether an argument is a flag whose operand is a
// credential: `--api-key`, `--token=...`, `-H` short of it stays readable
// unless the long name is secret-shaped.
func namesSecret(arg string) bool {
	if !strings.HasPrefix(arg, "-") {
		return false
	}
	name, _, _ := strings.Cut(arg, envSeparator)
	return envsecret.IsSecret(strings.TrimLeft(name, "-"))
}

// inlineSecretValue splits the value out of an inline `--flag=value` secret
// flag, reporting ok false for every other argument. It is the one recognition
// rule for the inline spelling: the record replaces the value with
// [RedactedValue] and the output scrub hides the value itself.
func inlineSecretValue(arg string) (name, value string, ok bool) {
	name, value, found := strings.Cut(arg, envSeparator)
	if !found || value == "" || !namesSecret(name) {
		return "", "", false
	}
	return name, value, true
}
