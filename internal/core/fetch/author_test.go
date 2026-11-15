package fetchcore

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// authorCase is one realistic PR payload carrying exactly the signal named and
// the fingerprint a fetch must record for it.
type authorCase struct {
	model      *string
	name       string
	pr         string
	agent      string
	confidence string
	human      string
	evidence   []core.AuthorEvidence
}

// authorCases are the five signals the ticket publishes, each in the payload a
// real harness writes it in.
func authorCases() []authorCase {
	return []authorCase{
		{
			// Claude Code's commit trailer, the form a squash merge keeps.
			name: "commit trailer",
			pr: `{"number":4701,"title":"Harden the retry budget","body":"",
				"headRefName":"sebastian/harden-retry-budget",
				"commits":[{"oid":"a1b2c3d4e5f60718293a4b5c6d7e8f9012345678",
					"messageHeadline":"fetch: cap retries at three",
					"messageBody":"The budget path retried without limit.\n\nCo-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"}]}`,
			agent:      "claude",
			model:      new("Opus 4.5"),
			confidence: "high",
			evidence: []core.AuthorEvidence{
				{Field: "commits", Signal: "Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>"},
			},
			human: "» author: claude (high confidence) from 1 signal(s)",
		},
		{
			// The line Claude Code appends to a PR body it opened.
			name: "generated with line",
			pr: `{"number":4702,"title":"Keep the retry budget per host",
				"body":"## Summary\n\nRetries stop at three per host.\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)",
				"headRefName":"sebastian/retry-budget","commits":[]}`,
			agent:      "claude",
			confidence: "high",
			evidence: []core.AuthorEvidence{
				{Field: "body", Signal: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"},
			},
			human: "» author: claude (high confidence) from 1 signal(s)",
		},
		{
			// The Codex app's branch prefix.
			name: "codex branch",
			pr: `{"number":4703,"title":"Add a retry budget to fetch",
				"body":"Retries now stop at three.\n","headRefName":"codex/add-retry-budget",
				"commits":[]}`,
			agent:      "codex",
			confidence: "medium",
			evidence: []core.AuthorEvidence{
				{Field: "head_ref", Signal: "codex/"},
			},
			human: "» author: codex (medium confidence) from 1 signal(s)",
		},
		{
			// The Codex app's PR-title tag.
			name: "codex title tag",
			pr: `{"number":4704,"title":"[codex] Add a retry budget to fetch",
				"body":"Retries now stop at three.\n","headRefName":"feat/retry-budget",
				"commits":[]}`,
			agent:      "codex",
			confidence: "medium",
			evidence: []core.AuthorEvidence{
				{Field: "title", Signal: "[codex]"},
			},
			human: "» author: codex (medium confidence) from 1 signal(s)",
		},
		{
			// Cursor's branch prefix.
			name: "cursor branch",
			pr: `{"number":4705,"title":"Speed up role grouping",
				"body":"Roles group in one pass.\n",
				"headRefName":"cursor/speed-up-role-grouping-4f2a","commits":[]}`,
			agent:      "cursor",
			confidence: "medium",
			evidence: []core.AuthorEvidence{
				{Field: "head_ref", Signal: "cursor/"},
			},
			human: "» author: cursor (medium confidence) from 1 signal(s)",
		},
	}
}

// TestWriteAuthorRecordsEachPublishedSignal proves every signal the ticket
// names lands in author.json as the agent, model, confidence, and evidence row
// it carries, and names itself on the human surface.
func TestWriteAuthorRecordsEachPublishedSignal(t *testing.T) {
	t.Parallel()
	for _, tc := range authorCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertAuthorCase(t, &tc)
		})
	}
}

func assertAuthorCase(t *testing.T, tc *authorCase) {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, dir+suffixPRJSON, tc.pr)
	ctx2, human, _ := impactRunCtx(t, dir)
	if err := writeAuthor(ctx2); err != nil {
		t.Fatalf("writeAuthor: %v", err)
	}
	got := readAuthorFile(t, dir)
	if got.Agent != tc.agent {
		t.Errorf("agent = %q, want %q", got.Agent, tc.agent)
	}
	if !reflect.DeepEqual(got.Model, tc.model) {
		t.Errorf("model = %v, want %v", showPtr(got.Model), showPtr(tc.model))
	}
	if got.Confidence != tc.confidence {
		t.Errorf("confidence = %q, want %q", got.Confidence, tc.confidence)
	}
	if !reflect.DeepEqual(got.Evidence, tc.evidence) {
		t.Errorf("evidence = %+v, want %+v", got.Evidence, tc.evidence)
	}
	if !strings.Contains(human.String(), tc.human) {
		t.Errorf("human line = %q, want %q", human.String(), tc.human)
	}
}

// TestWriteAuthorKeepsEverySignalInEvidence proves conflicting signals are
// recorded rather than resolved away: the strongest names the agent, and both
// matches stay readable.
func TestWriteAuthorKeepsEverySignalInEvidence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTestFile(t, dir+suffixPRJSON, `{"number":4706,"title":"[codex] Retry budget",
		"body":"Retries stop at three.\n\n🤖 Generated with [Claude Code](https://claude.com/claude-code)",
		"headRefName":"cursor/retry-budget-77ab","commits":[]}`)
	ctx2, human, _ := impactRunCtx(t, dir)
	if err := writeAuthor(ctx2); err != nil {
		t.Fatalf("writeAuthor: %v", err)
	}
	got := readAuthorFile(t, dir)
	if got.Agent != "claude" || got.Confidence != "high" {
		t.Errorf("fingerprint = %s/%s, want claude/high (the strongest signal)", got.Agent, got.Confidence)
	}
	want := []core.AuthorEvidence{
		{Field: "title", Signal: "[codex]"},
		{Field: "body", Signal: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"},
		{Field: "head_ref", Signal: "cursor/"},
	}
	if !reflect.DeepEqual(got.Evidence, want) {
		t.Errorf("evidence = %+v, want %+v", got.Evidence, want)
	}
	if want := "from 3 signal(s)"; !strings.Contains(human.String(), want) {
		t.Errorf("human line = %q, want %q", human.String(), want)
	}
}

// TestWriteAuthorRecordsNothingWithoutASignal proves an unknown author stays
// unknown: a human co-author trailer and ordinary prose record nothing, and a
// stale claim from an earlier run is removed instead of inherited.
func TestWriteAuthorRecordsNothingWithoutASignal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTestFile(t, dir+suffixPRJSON, `{"number":4707,"title":"Harden the retry budget",
		"body":"Retries stop at three.\n\nCo-authored-by: Alice <alice@example.com>",
		"headRefName":"sebastian/harden-retry-budget",
		"commits":[{"oid":"a1b2c3d4e5f60718293a4b5c6d7e8f9012345678",
			"messageHeadline":"fetch: cap retries at three",
			"messageBody":"The budget path retried without limit."}]}`)
	writeTestFile(t, dir+suffixAuthor, `{
  "schema_version": "1",
  "agent": "claude",
  "model": null,
  "confidence": "high",
  "evidence": [
    {
      "field": "title",
      "signal": "Generated with Claude Code"
    }
  ]
}
`)
	ctx2, human, _ := impactRunCtx(t, dir)
	if err := writeAuthor(ctx2); err != nil {
		t.Fatalf("writeAuthor: %v", err)
	}
	if _, err := os.Stat(dir + suffixAuthor); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("author.json stat err = %v, want not-exist", err)
	}
	if got := human.String(); got != "" {
		t.Errorf("human line = %q, want none", got)
	}
}

// TestNoFingerprintFromPlainProse holds the weak signals to their own fields: a
// human PR that merely mentions a harness path, or names a branch after one,
// writes no fingerprint at all.
func TestNoFingerprintFromPlainProse(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, pr string }{
		{
			name: "cursor rules path in the body",
			pr:   `{"number":4711,"title":"Tidy the rules file","body":"Updated .cursor/rules to cover Go.","headRefName":"sebastian/tidy-rules"}`,
		},
		{
			name: "codex setup path in a commit",
			pr: `{"number":4712,"title":"Document the harness setup","body":"",
				"headRefName":"sebastian/harness-docs",
				"commits":[{"oid":"a1b2c3d4e5f60718293a4b5c6d7e8f9012345678",
					"messageHeadline":"docs: point at docs/codex/setup.md",
					"messageBody":"The old path rotted."}]}`,
		},
		{
			name: "harness name inside a head ref",
			pr:   `{"number":4713,"title":"Fix the cursor","body":"","headRefName":"feat/cursor/fix"}`,
		},
		{
			name: "codex tag inside a title",
			pr:   `{"number":4714,"title":"Stop parsing the [codex] tag","body":"","headRefName":"sebastian/tag-parse"}`,
		},
		{
			name: "a co-author whose name merely starts like an agent",
			pr: `{"number":4715,"title":"Harden the retry budget","body":"",
				"headRefName":"sebastian/harden-retry-budget",
				"commits":[{"oid":"a1b2c3d4e5f60718293a4b5c6d7e8f9012345678",
					"messageHeadline":"fetch: cap retries at three",
					"messageBody":"Co-Authored-By: Claudette <claudette@example.com>"}]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeTestFile(t, dir+suffixPRJSON, tc.pr)
			if file := authorFromBundle(dir); file != nil {
				t.Errorf("fingerprint = %+v, want none", file)
			}
		})
	}
}

// TestAuthorArtifactNamesTheFile proves a detected fingerprint is advertised in
// the bundle's artifact map under the published key.
func TestAuthorArtifactNamesTheFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTestFile(t, dir+suffixPRJSON, `{"number":4708,"title":"[codex] Retry budget","body":"","headRefName":"codex/retry"}`)
	ctx2, _, _ := impactRunCtx(t, dir)
	if err := writeAuthor(ctx2); err != nil {
		t.Fatalf("writeAuthor: %v", err)
	}
	obj := jq.NewObj()
	addAuthorArtifact(obj, dir)
	if got, ok := obj.Get("author.json"); !ok || got != dir+"/author.json" {
		t.Errorf("artifacts[author.json] = %v (present %v), want %q", got, ok, dir+"/author.json")
	}
}

func readAuthorFile(t *testing.T, dir string) *core.AuthorFile {
	t.Helper()
	raw, err := os.ReadFile(dir + suffixAuthor)
	if err != nil {
		t.Fatalf("read author.json: %v", err)
	}
	var file core.AuthorFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("decode author.json: %v", err)
	}
	if file.SchemaVersion != "1" {
		t.Errorf("schema_version = %q, want %q", file.SchemaVersion, "1")
	}
	return &file
}

func showPtr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}
