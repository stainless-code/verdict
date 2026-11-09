package run

import (
	"slices"
	"strings"
	"testing"
)

// TestArgvShapeRedactsSecrets pins the provenance rule: the directive
// becomes a placeholder, a secret-shaped flag's operand is replaced (both
// spellings), and a bare argument that equals a secret-named environment
// value is replaced too.
func TestArgvShapeRedactsSecrets(t *testing.T) {
	const prompt = "review the bundle"
	environ := []string{"ANTHROPIC_API_KEY=sk-ant-0123456789", "PATH=/usr/bin"}
	argv := []string{
		"claude", "-p", "--model", "opus", "--api-key", "plain-value",
		"--token=inline-value", "sk-ant-0123456789", prompt,
	}
	got := argvShape(argv, prompt, environ)
	want := []string{
		"claude", "-p", "--model", "opus", "--api-key", redactedValue,
		"--token=" + redactedValue, redactedValue, promptPlaceholder,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("argvShape = %q, want %q", got, want)
	}
}

// TestArgvShapeStackedSecretFlags proves a secret-shaped flag with no operand
// does not swallow the next flag's operand: `--token --api-key VALUE` has an
// empty operand for --token, so the value belongs to --api-key and is redacted
// there rather than surviving verbatim.
func TestArgvShapeStackedSecretFlags(t *testing.T) {
	argv := []string{"claude", "-p", "--token", "--api-key", "plain-value"}
	got := argvShape(argv, "", nil)
	want := []string{"claude", "-p", "--token", "--api-key", redactedValue}
	if !slices.Equal(got, want) {
		t.Fatalf("argvShape = %q, want %q", got, want)
	}
}

// TestArgvShapeKeepsOrdinaryFlags proves the redactor does not swallow the
// launcher's own shape: a model name and a turn cap stay readable, and the
// agent's own flags keep their operands.
func TestArgvShapeKeepsOrdinaryFlags(t *testing.T) {
	const prompt = "review the bundle"
	argv := []string{"codex", "exec", "--model", "o3", "--max-turns", "12", prompt}
	got := argvShape(argv, prompt, nil)
	if got[3] != "o3" || got[5] != "12" {
		t.Fatalf("argvShape = %q, want ordinary operands preserved", got)
	}
}

// TestScrubSecrets proves the recorded output tail drops the operator's own
// secret values while leaving the rest of the output intact.
func TestScrubSecrets(t *testing.T) {
	environ := []string{"GH_TOKEN=ghp_examplevalue", "PATH=/usr/bin"}
	got := scrubSecrets("auth with ghp_examplevalue failed at /usr/bin/gh", environ)
	if strings.Contains(got, "ghp_examplevalue") {
		t.Fatalf("scrubSecrets kept the secret: %q", got)
	}
	if !strings.Contains(got, "/usr/bin/gh") {
		t.Errorf("scrubSecrets dropped non-secret text: %q", got)
	}
}

// TestDirectiveNamesTheArtifact pins the prompt's contract: the subject, the
// bundle, the exact review file, and the boundary. The local variant names
// source.json instead of PR.json.
func TestDirectiveNamesTheArtifact(t *testing.T) {
	pr := directive("acme/app", "42", ".verdict/state/42", ".verdict/state/42/review.json", false)
	for _, want := range []string{
		"acme/app PR 42", ".verdict/state/42", "review.json", "PR.json",
		"schema_version", "does not read your prose",
	} {
		if !strings.Contains(strings.ToLower(pr), strings.ToLower(want)) {
			t.Errorf("directive does not name %q:\n%s", want, pr)
		}
	}
	local := directive("acme/app", "local-work", ".verdict/state/local-work",
		".verdict/state/local-work/review.json", true)
	if !strings.Contains(local, "source.json") || strings.Contains(local, "PR.json") {
		t.Errorf("local directive names the wrong metadata file:\n%s", local)
	}
	if !strings.Contains(local, "local change local-work") {
		t.Errorf("local directive misnames the subject:\n%s", local)
	}
}

// TestAgentTableIsSortedAndUnique keeps the data table honest: duplicate or
// unsorted rows would make the known-name list a lie.
func TestAgentTableIsSortedAndUnique(t *testing.T) {
	names := AgentNames()
	if !slices.IsSorted(names) {
		t.Errorf("agent table is not sorted: %v", names)
	}
	if len(slices.Compact(slices.Clone(names))) != len(names) {
		t.Errorf("agent table repeats a name: %v", names)
	}
	for _, name := range names {
		def, ok := lookupAgent(name)
		if !ok || def.Binary == "" || len(def.Args) == 0 {
			t.Errorf("agent %q has no binary or no non-interactive argument shape: %+v", name, def)
		}
	}
	if _, ok := lookupAgent("gpt"); ok {
		t.Error("lookupAgent resolved an agent that is not in the table")
	}
}
