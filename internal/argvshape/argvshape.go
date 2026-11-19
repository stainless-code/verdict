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
// secret-shaped flag is replaced (both `--flag value` and `--flag=value`), the
// value of a secret-named NAME=value token is replaced wherever it appears (a
// positional assignment or one set inside a recorded command string), and so
// is any argument that equals a value the operator's environment holds under a
// secret name. Over-redaction is acceptable in an audit record; a leaked
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
		rendered, hid := recordArg(arg, prompt, known, redactedSeparate)
		shape = append(shape, rendered)
		secrets = append(secrets, hid...)
	}
	return shape, secrets
}

// recordArg renders one argument and reports every credential value it hid.
func recordArg(arg, prompt string, known []string, redactedSeparate bool) (rendered string, secrets []string) {
	if prompt != "" && arg == prompt {
		// The directive is tool-generated prose, not a credential, and is
		// never collected even when a flag precedes it: scrubbing hundreds
		// of bytes of prose out of the tail would erase the tail.
		return PromptPlaceholder, nil
	}
	if redactedSeparate || IsSecretValue(arg, known) {
		return RedactedValue, []string{arg}
	}
	return maskAssignments(arg)
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

// maskAssignments replaces the value of every secret-named NAME=value token in
// arg with [RedactedValue]: the inline `--flag=value` spelling, a positional
// `API_TOKEN=...`, and a credential set inside a recorded command string such as
// `--command 'API_KEY="two words" make test'`. The name, the separator, and any
// quotes around the value survive, so the record still reads like the command it
// describes, and the hidden values are returned for the output scrub.
func maskAssignments(arg string) (string, []string) {
	var (
		out     strings.Builder
		secrets []string
	)
	for i := 0; i < len(arg); {
		name, valueAt, ok := assignmentAt(arg, i)
		if !ok {
			out.WriteByte(arg[i])
			i++
			continue
		}
		value, quote, closed, end := assignmentValue(arg, valueAt)
		if !envsecret.IsSecret(name) || value == "" {
			out.WriteByte(arg[i])
			i++
			continue
		}
		out.WriteString(arg[i:valueAt])
		if quote != 0 {
			out.WriteByte(quote)
		}
		out.WriteString(RedactedValue)
		if closed {
			out.WriteByte(quote)
		}
		secrets = append(secrets, value)
		i = end
	}
	return out.String(), secrets
}

// assignmentAt reports the name and the value offset of a NAME=value token
// starting at i. A token starts at the beginning of the string or after a
// non-name byte, and its name starts with a letter or an underscore.
func assignmentAt(s string, i int) (name string, valueAt int, ok bool) {
	if i > 0 && isNameByte(s[i-1]) {
		return "", 0, false
	}
	j := i
	for j < len(s) && isNameByte(s[j]) {
		if j == i && !isNameLead(s[j]) {
			return "", 0, false
		}
		j++
	}
	if j == i || j >= len(s) || s[j] != '=' {
		return "", 0, false
	}
	return s[i:j], j + 1, true
}

// assignmentValue returns the value bytes starting at valueAt, the quote byte
// wrapping them (0 when unquoted), whether that quote is closed, and the offset
// just past the value. A quoted value runs to its closing quote, so it may hold
// spaces; an unquoted one ends at the next space.
func assignmentValue(s string, valueAt int) (value string, quote byte, closed bool, end int) {
	if valueAt >= len(s) {
		return "", 0, false, valueAt
	}
	if q := s[valueAt]; q == '"' || q == '\'' {
		for j := valueAt + 1; j < len(s); j++ {
			if s[j] == '\\' && q == '"' {
				j++
				continue
			}
			if s[j] == q {
				return s[valueAt+1 : j], q, true, j + 1
			}
		}
		return s[valueAt+1:], q, false, len(s)
	}
	j := valueAt
	for j < len(s) && !isSpaceByte(s[j]) {
		j++
	}
	return s[valueAt:j], 0, false, j
}

func isNameLead(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isNameByte(b byte) bool { return isNameLead(b) || (b >= '0' && b <= '9') }

func isSpaceByte(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }
