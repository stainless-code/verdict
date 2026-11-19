package argvshape_test

import (
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/argvshape"
)

// TestRecordRedactsSecrets pins the provenance rule: the directive becomes a
// placeholder, a secret-shaped flag's operand is replaced (both spellings), and
// a bare argument that equals a secret-named environment value is replaced too.
func TestRecordRedactsSecrets(t *testing.T) {
	t.Parallel()
	const prompt = "review the bundle"
	environ := []string{"ANTHROPIC_API_KEY=sk-ant-0123456789", "PATH=/usr/bin"}
	argv := []string{
		"claude", "-p", "--model", "opus", "--api-key", "plain-value",
		"--token=inline-value", "sk-ant-0123456789", prompt,
	}
	got, _ := argvshape.Record(argv, prompt, environ)
	want := []string{
		"claude", "-p", "--model", "opus", "--api-key", "***REDACTED***",
		"--token=***REDACTED***", "***REDACTED***", "<prompt>",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Record = %q, want %q", got, want)
	}
}

// TestRecordStackedSecretFlags proves a secret-shaped flag with no operand does
// not swallow the next flag's operand: `--token --api-key VALUE` has an empty
// operand for --token, so the value belongs to --api-key and is redacted there
// rather than surviving verbatim.
func TestRecordStackedSecretFlags(t *testing.T) {
	t.Parallel()
	argv := []string{"claude", "-p", "--token", "--api-key", "plain-value"}
	got, _ := argvshape.Record(argv, "", nil)
	want := []string{"claude", "-p", "--token", "--api-key", "***REDACTED***"}
	if !slices.Equal(got, want) {
		t.Fatalf("Record = %q, want %q", got, want)
	}
}

// TestRecordKeepsOrdinaryFlags proves the redactor does not swallow ordinary
// flags or empty arguments when no prompt body is supplied.
func TestRecordKeepsOrdinaryFlags(t *testing.T) {
	t.Parallel()
	const prompt = "review the bundle"
	argv := []string{"codex", "exec", "--model", "o3", "--max-turns", "12", prompt}
	got, _ := argvshape.Record(argv, prompt, nil)
	want := []string{"codex", "exec", "--model", "o3", "--max-turns", "12", "<prompt>"}
	if !slices.Equal(got, want) {
		t.Fatalf("Record = %q, want %q", got, want)
	}

	gotEmpty, _ := argvshape.Record([]string{"record", "acme/app", "local-1", "--note", ""}, "", nil)
	wantEmpty := []string{"record", "acme/app", "local-1", "--note", ""}
	if !slices.Equal(gotEmpty, wantEmpty) {
		t.Fatalf("Record(empty arg, empty prompt) = %q, want %q", gotEmpty, wantEmpty)
	}
}

// TestRecordReturnsTheSecretsItHid pins the shared walk's second answer: the
// values the record replaced are exactly the ones the output scrub hides,
// including an inline `--flag=value`'s value and an argument that equals an
// environment credential.
func TestRecordReturnsTheSecretsItHid(t *testing.T) {
	t.Parallel()
	const prompt = "review the bundle"
	argv := []string{
		"claude", "--api-key", "plain-value", "--token=inline-value",
		"sk-ant-0123456789", prompt,
	}
	_, secrets := argvshape.Record(argv, prompt, []string{"ANTHROPIC_API_KEY=sk-ant-0123456789"})
	want := []string{"plain-value", "inline-value", "sk-ant-0123456789"}
	if !slices.Equal(secrets, want) {
		t.Fatalf("secrets = %q, want %q", secrets, want)
	}
}

// TestRecordNeverCollectsTheDirective proves the directive is not a credential:
// a secret-shaped flag immediately before it must not put hundreds of bytes of
// tool-generated prose on the scrub list, which would erase the recorded tail.
func TestRecordNeverCollectsTheDirective(t *testing.T) {
	t.Parallel()
	const prompt = "review the bundle and write exactly one review file"
	shape, secrets := argvshape.Record([]string{"claude", "--token", prompt}, prompt, nil)
	if slices.Contains(secrets, prompt) {
		t.Fatalf("secrets = %q, want the directive excluded", secrets)
	}
	if shape[2] != "<prompt>" {
		t.Errorf("shape = %q, want the directive placeholder", shape)
	}
}
