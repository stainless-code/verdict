package run

import (
	"slices"
	"strings"
	"testing"
)

// TestRecordArgvRedactsSecrets pins the provenance rule: the directive
// becomes a placeholder, a secret-shaped flag's operand is replaced (both
// spellings), and a bare argument that equals a secret-named environment
// value is replaced too.
func TestRecordArgvRedactsSecrets(t *testing.T) {
	const prompt = "review the bundle"
	environ := []string{"ANTHROPIC_API_KEY=sk-ant-0123456789", "PATH=/usr/bin"}
	argv := []string{
		"claude", "-p", "--model", "opus", "--api-key", "plain-value",
		"--token=inline-value", "sk-ant-0123456789", prompt,
	}
	got, _ := recordArgv(argv, prompt, environ)
	want := []string{
		"claude", "-p", "--model", "opus", "--api-key", redactedValue,
		"--token=" + redactedValue, redactedValue, promptPlaceholder,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("recordArgv = %q, want %q", got, want)
	}
}

// TestRecordArgvStackedSecretFlags proves a secret-shaped flag with no operand
// does not swallow the next flag's operand: `--token --api-key VALUE` has an
// empty operand for --token, so the value belongs to --api-key and is redacted
// there rather than surviving verbatim.
func TestRecordArgvStackedSecretFlags(t *testing.T) {
	argv := []string{"claude", "-p", "--token", "--api-key", "plain-value"}
	got, _ := recordArgv(argv, "", nil)
	want := []string{"claude", "-p", "--token", "--api-key", redactedValue}
	if !slices.Equal(got, want) {
		t.Fatalf("recordArgv = %q, want %q", got, want)
	}
}

// TestRecordArgvKeepsOrdinaryFlags proves the redactor does not swallow the
// launcher's own shape: a model name and a turn cap stay readable, and the
// agent's own flags keep their operands.
func TestRecordArgvKeepsOrdinaryFlags(t *testing.T) {
	const prompt = "review the bundle"
	argv := []string{"codex", "exec", "--model", "o3", "--max-turns", "12", prompt}
	got, _ := recordArgv(argv, prompt, nil)
	if got[3] != "o3" || got[5] != "12" {
		t.Fatalf("recordArgv = %q, want ordinary operands preserved", got)
	}
}

// TestScrubSecrets proves the recorded output tail drops the operator's own
// secret values - the environment's and the argument ones the argv redactor
// hid - while leaving the rest of the output intact.
func TestScrubSecrets(t *testing.T) {
	environ := []string{"GH_TOKEN=ghp_examplevalue", "PATH=/usr/bin"}
	got := scrubSecrets("auth with ghp_examplevalue failed at /usr/bin/gh", environ, nil)
	if strings.Contains(got, "ghp_examplevalue") {
		t.Fatalf("scrubSecrets kept the environment secret: %q", got)
	}
	if !strings.Contains(got, "/usr/bin/gh") {
		t.Errorf("scrubSecrets dropped non-secret text: %q", got)
	}
	got = scrubSecrets("arg[2]=sk-repro-secret-123\nexit=0", nil, []string{"sk-repro-secret-123"})
	if strings.Contains(got, "sk-repro-secret-123") {
		t.Fatalf("scrubSecrets kept the argument secret: %q", got)
	}
	if got != "arg[2]="+redactedValue+"\nexit=0" {
		t.Errorf("scrubSecrets = %q, want only the credential replaced", got)
	}
}

// TestRecordArgvReturnsTheSecretsItHid pins the shared walk's second answer:
// the values the record replaced are exactly the ones the output scrub hides,
// including an inline `--flag=value`'s value and an argument that equals an
// environment credential.
func TestRecordArgvReturnsTheSecretsItHid(t *testing.T) {
	const prompt = "review the bundle"
	argv := []string{
		"claude", "--api-key", "plain-value", "--token=inline-value",
		"sk-ant-0123456789", prompt,
	}
	_, secrets := recordArgv(argv, prompt, []string{"ANTHROPIC_API_KEY=sk-ant-0123456789"})
	want := []string{"plain-value", "inline-value", "sk-ant-0123456789"}
	if !slices.Equal(secrets, want) {
		t.Fatalf("secrets = %q, want %q", secrets, want)
	}
}

// TestRecordArgvNeverCollectsTheDirective proves the directive is not a
// credential: a secret-shaped flag immediately before it must not put hundreds
// of bytes of tool-generated prose on the scrub list, which would erase the
// recorded tail.
func TestRecordArgvNeverCollectsTheDirective(t *testing.T) {
	const prompt = "review the bundle and write exactly one review file"
	shape, secrets := recordArgv([]string{"claude", "--token", prompt}, prompt, nil)
	if slices.Contains(secrets, prompt) {
		t.Fatalf("secrets = %q, want the directive excluded", secrets)
	}
	if shape[2] != promptPlaceholder {
		t.Errorf("shape = %q, want the directive placeholder", shape)
	}
}

// TestScrubSecretsKeepsItsPolicy proves what the launcher's scrub owns now
// that the matching lives in [envsecret.ScrubValues]: environment values under
// a secret-shaped name, argument secrets the argv redactor hid, and an empty
// argument value that must not shred the tail.
func TestScrubSecretsKeepsItsPolicy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		text    string
		want    string
		environ []string
		args    []string
	}{
		{
			// The operator named the flag, so the value is a credential by
			// construction: no length gate, unlike the environment path.
			name: "argument-secret-below-the-environment-floor",
			text: "arg[3]=short",
			args: []string{"short"},
			want: "arg[3]=" + redactedValue,
		},
		{
			// Environment and argument values both reach the one scrubber, and
			// the overlap between them is resolved there.
			name:    "environment-and-argument-overlap",
			text:    "token=secretvalueEXTRA",
			environ: []string{"API_KEY=secretvalue"},
			args:    []string{"valueEXTRA"},
			want:    "token=" + redactedValue,
		},
		{
			name: "empty-argument-value",
			text: "exit=0",
			args: []string{""},
			want: "exit=0",
		},
		{
			name:    "non-secret-variable-untouched",
			text:    "path=/usr/bin/gh",
			environ: []string{"PATH=/usr/bin/gh"},
			want:    "path=/usr/bin/gh",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := scrubSecrets(tc.text, tc.environ, tc.args); got != tc.want {
				t.Errorf("scrubSecrets(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

// TestDirectiveNamesTheArtifact pins the prompt's contract: the subject, the
// bundle, the exact review file, and the boundary. The local variant names
// source.json instead of PR.json.
func TestDirectiveNamesTheArtifact(t *testing.T) {
	pr := directive("acme/app", "42", ".verdict/state/42", ".verdict/state/42/review.json", false,
		builtinPlan(t))
	for _, want := range []string{
		"acme/app PR 42", ".verdict/state/42", "review.json", "PR.json",
		"schema_version", "does not read your prose",
	} {
		if !strings.Contains(strings.ToLower(pr), strings.ToLower(want)) {
			t.Errorf("directive does not name %q:\n%s", want, pr)
		}
	}
	local := directive("acme/app", "local-work", ".verdict/state/local-work",
		".verdict/state/local-work/review.json", true, builtinPlan(t))
	if !strings.Contains(local, "source.json") || strings.Contains(local, "PR.json") {
		t.Errorf("local directive names the wrong metadata file:\n%s", local)
	}
	if !strings.Contains(local, "local change local-work") {
		t.Errorf("local directive misnames the subject:\n%s", local)
	}
}

// pipelineDirective is the whole-pipeline directive for a PR bundle, byte for
// byte: the built-in default declares no lens, no model hint, and no budget, so
// every sentence here is fixed text. It is one literal on purpose — a permuted
// Sprintf argument degrades a sentence into nonsense and every substring pin
// below survives it.
const pipelineDirective = "Review acme/app PR 42 and write exactly one review file.\n\n" +
	"Read the installed verdict review skill first, then the bundle at .verdict/state/42: PR.json, " +
	"roles.txt, and toc.txt indexing full.diff, plus kb.txt, sensitive.txt, and panel when the run " +
	"carries them.\n\n" +
	"Write .verdict/state/42/review.json with {\"schema_version\": \"1\", \"body\": ..., " +
	"\"approved\": ..., \"comments\": [...]} (optional \"resolved\" and \"triage\"). Write nothing " +
	"else in the bundle.\n\n" +
	"verdict launched this one process and validates that file; it does not read your prose, retry " +
	"you, or post. Do not post, merge, or push: publishing is an explicit verdict post.\n\n" +
	"The review pipeline is recipe default@1.0.0 (builtin): lenses none; synthesize; dedupe; rank. " +
	"These are declarations, not selections: verdict launched this one process and never chose a model.\n"

// TestDirectiveIsByteExact pins the whole-pipeline directive against that
// literal. Every other pin in this package is a substring, and a bundle and
// metadata file swapped in the argument list reads as "the bundle at PR.json:
// .verdict/state/42" while all of them still pass.
func TestDirectiveIsByteExact(t *testing.T) {
	got := directive("acme/app", "42", ".verdict/state/42", ".verdict/state/42/review.json", false,
		builtinPlan(t))
	if got != pipelineDirective {
		t.Fatalf("directive =\n%q\nwant\n%q", got, pipelineDirective)
	}
}

// TestDirectiveKeepsAPercentPathLiteral proves neither directive is a format
// string assembled from data: a checkout path may contain %s, %d, and %%, and
// must reach the agent byte for byte with the write instruction intact. A
// bundle under a directory literally named "%s" used to consume the artifact
// argument of directive's outer Sprintf and render "Write %!s(MISSING) with
// ...".
func TestDirectiveKeepsAPercentPathLiteral(t *testing.T) {
	bundle := "/checkouts/%s/%d/100%%/42"
	artifact := bundle + "/review.json"
	plan := builtinPlan(t)

	whole := directive("acme/app", "42", bundle, artifact, false, plan)
	if want := "then the bundle at " + bundle + ": PR.json"; !strings.Contains(whole, want) {
		t.Errorf("directive does not keep the path literally, want %q in:\n%s", want, whole)
	}
	if want := "Write " + artifact + " with {\"schema_version\": \"1\""; !strings.Contains(whole, want) {
		t.Errorf("directive mangles the write instruction, want %q in:\n%s", want, whole)
	}
	if strings.Contains(whole, "%!") {
		t.Errorf("directive rendered a format operand as data:\n%s", whole)
	}

	at := staged{index: 2, stage: plan.Stages[1]}
	stage := stageDirective("acme/app", "42", bundle, artifact, false, plan, &at)
	if want := "Read " + artifact + ", the review the previous stage wrote."; !strings.Contains(stage, want) {
		t.Errorf("stage directive does not keep the path literally, want %q in:\n%s", want, stage)
	}
	if want := "Write " + artifact + " with {\"schema_version\": \"1\""; !strings.Contains(stage, want) {
		t.Errorf("stage directive mangles the write instruction, want %q in:\n%s", want, stage)
	}
	if strings.Contains(stage, "%!") {
		t.Errorf("stage directive rendered a format operand as data:\n%s", stage)
	}
}

// TestAgentTableIsSortedAndUnique keeps the data table honest: duplicate or
// unsorted rows would make the known-name list a lie.
func TestAgentTableIsSortedAndUnique(t *testing.T) {
	table := make([]string, 0, len(agentDefs))
	for _, def := range agentDefs {
		table = append(table, def.Name)
	}
	if !slices.IsSorted(table) {
		t.Errorf("agent table is not sorted: %v", table)
	}
	if len(slices.Compact(slices.Clone(table))) != len(table) {
		t.Errorf("agent table repeats a name: %v", table)
	}
	known := AgentNames()
	slices.Sort(known)
	if !slices.Equal(known, table) {
		t.Errorf("known names %v do not enumerate the table %v", known, table)
	}
	for _, name := range table {
		def, ok := lookupAgent(name)
		if !ok || def.Binary == "" || def.ModelFlag == "" || def.Install == "" {
			t.Errorf("agent %q has no binary, model flag, or install hint: %+v", name, def)
		}
	}
	if _, ok := lookupAgent("gpt"); ok {
		t.Error("lookupAgent resolved an agent that is not in the table")
	}
}
