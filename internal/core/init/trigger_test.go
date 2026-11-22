package initcore

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

// githubTarget is the one provider that carries the opt-in comment trigger.
func githubTarget(t *testing.T) *ciProvider {
	t.Helper()
	ci, ok := LookupCI("github")
	if !ok {
		t.Fatal("the github provider is gone")
	}
	return ci
}

// mustTriggeredRender is the triggered workflow's bytes, the contract every
// assertion below grades.
func mustTriggeredRender(t *testing.T) string {
	t.Helper()
	body, ref := RenderWorkflowSpec(WorkflowSpec{
		CI: "github", Agent: "claude", CommentTrigger: true,
	})
	if ref != nil {
		t.Fatalf("render github with the comment trigger: %s: %s", ref.Code, ref.Message)
	}
	return string(body)
}

// TestCommentTriggerWorkflowCarriesTheOptInContract pins the triggered render
// as literals: the extra event, the maintainer predicate the job and the
// concurrency key share, the run-unique no-op group, the pull request number
// the comment event supplies, and the header line announcing the command.
func TestCommentTriggerWorkflowCarriesTheOptInContract(t *testing.T) {
	t.Parallel()
	workflow := mustTriggeredRender(t)
	for _, want := range []string{
		"# Add a `/verdict` comment on a pull request to re-run the review.",
		"on:",
		"  pull_request:",
		"    types: [opened, synchronize, reopened]",
		"  issue_comment:",
		"    types: [created]",
		"concurrency:",
		"  cancel-in-progress: true",
		"      (github.event_name == 'pull_request' &&",
		"       github.event.pull_request.head.repo.full_name == github.repository) ||",
		"      (github.event_name == 'issue_comment' &&",
		"       github.event.issue.pull_request != null &&",
		`       contains(fromJSON('["OWNER","MEMBER","COLLABORATOR"]'), ` +
			"github.event.comment.author_association) &&",
		`       (github.event.comment.body == '/verdict' || ` +
			`startsWith(github.event.comment.body, '/verdict ')))`,
		"          VERDICT_PR: ${{ github.event.pull_request.number || github.event.issue.number }}",
		"          VERDICT_REPOSITORY: ${{ github.repository }}",
	} {
		assertWorkflowLine(t, workflow, want)
	}
	assertTriggerGroup(t, workflow)
	assertEveryStepReadsThePRFromEitherEvent(t, workflow)
}

// assertEveryStepReadsThePRFromEitherEvent grades every step that hands the
// pipeline a pull request number: an issue_comment run carries it under issue,
// so a step still reading github.event.pull_request.number alone would pass an
// empty argument on a `/verdict` comment.
func assertEveryStepReadsThePRFromEitherEvent(t *testing.T, workflow string) {
	t.Helper()
	const eitherEvent = "${{ github.event.pull_request.number || github.event.issue.number }}"
	steps := 0
	for line := range strings.SplitSeq(workflow, "\n") {
		if !strings.Contains(line, "VERDICT_PR:") {
			continue
		}
		steps++
		if !strings.Contains(line, eitherEvent) {
			t.Fatalf("a step reads the pull request number from one event only: %s", line)
		}
	}
	if steps < 2 {
		t.Fatalf("the render hands VERDICT_PR to %d steps, want a fetched and a readied run", steps)
	}
}

// assertTriggerGroup pins the concurrency key's three routes: the required
// pull_request check keeps the pull request's own group, a matching comment
// gets a comment group of its own, and every other comment gets a run-unique
// one.
func assertTriggerGroup(t *testing.T, workflow string) {
	t.Helper()
	group := concurrencyGroupLine(t, workflow)
	for _, want := range []string{
		"format('verdict-{0}', github.event.pull_request.number)",
		"format('verdict-comment-{0}', github.event.issue.number)",
		"format('verdict-noop-{0}', github.run_id)",
	} {
		if !strings.Contains(group, want) {
			t.Fatalf("the concurrency key carries no %q: %s", want, group)
		}
	}
	for key, want := range map[string]int{
		"verdict-comment-{0}": 1,
		"verdict-noop-{0}":    1,
	} {
		if got := strings.Count(group, key); got != want {
			t.Fatalf("the concurrency key names %q %d times, want %d", key, got, want)
		}
	}
	assertNoSharedEventRoute(t, group)
}

// assertNoSharedEventRoute refuses a key a pull_request run and a comment run
// would both join: the comment reports against the default branch SHA, so that
// would cancel the required check pinned to the pull request head.
func assertNoSharedEventRoute(t *testing.T, group string) {
	t.Helper()
	for _, forbidden := range []string{
		"github.event.pull_request.number || github.event.issue.number",
		"format('verdict-{0}', github.event.issue.number)",
	} {
		if strings.Contains(group, forbidden) {
			t.Fatalf("the concurrency key shares one route across events: %s", group)
		}
	}
}

// concurrencyGroupLine answers the rendered group line, or fails.
func concurrencyGroupLine(t *testing.T, workflow string) string {
	t.Helper()
	for line := range strings.SplitSeq(workflow, "\n") {
		if strings.HasPrefix(line, "  group: ") {
			return line
		}
	}
	t.Fatal("the render carries no concurrency group")
	return ""
}

// TestCommentTriggerWorkflowNeverChecksOutAForkHead proves the security half:
// the trigger variant still declares no `pull_request_target`, and the comment
// body is read only inside an expression — so no comment text reaches a shell.
func TestCommentTriggerWorkflowNeverChecksOutAForkHead(t *testing.T) {
	t.Parallel()
	workflow := mustTriggeredRender(t)
	if strings.Contains(workflow, "pull_request_target") {
		t.Fatal("the triggered render names pull_request_target")
	}
	assertCommentBodyStaysInExpressions(t, workflow)
	if strings.Contains(workflow, "${{ github.event.comment") {
		t.Fatal("the render interpolates the comment body into a value")
	}
}

// assertCommentBodyStaysInExpressions grades every line that reads the comment:
// it must be the concurrency key or the job gate, never a script body.
func assertCommentBodyStaysInExpressions(t *testing.T, workflow string) {
	t.Helper()
	for line := range strings.SplitSeq(workflow, "\n") {
		if !strings.Contains(line, "github.event.comment.body") {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "group:") || strings.HasPrefix(trimmed, "(github.event.comment.body") {
			continue
		}
		t.Fatalf("the comment body reaches a line that is not the guard or the key: %s", line)
	}
}

// TestCommentTriggerKeepsThePipelineUnchanged proves the opt-in moves only the
// event, the guard, and the concurrency key: every step below `steps:` and both
// permission blocks are the default render's own bytes, and the default carries
// none of the opt-in tokens.
func TestCommentTriggerKeepsThePipelineUnchanged(t *testing.T) {
	t.Parallel()
	defaultRender := string(mustRender(t, githubTarget(t)))
	triggered := mustTriggeredRender(t)
	if got, want := workflowStepNames(triggered), workflowStepNames(defaultRender); !slices.Equal(got, want) {
		t.Fatalf("triggered step names = %q, want %q", got, want)
	}
	for _, want := range []string{
		"permissions:\n  contents: read\n",
		"      pull-requests: write",
		`verdict fetch "$VERDICT_REPOSITORY" "$VERDICT_PR"`,
		`verdict post "$VERDICT_REPOSITORY" "$VERDICT_PR"`,
		`verdict gate "$VERDICT_REPOSITORY" "$VERDICT_PR" \`,
	} {
		if !strings.Contains(triggered, want) {
			t.Fatalf("the triggered render carries no %q", want)
		}
	}
	for _, forbidden := range []string{"issue_comment", "verdict-noop"} {
		if strings.Contains(defaultRender, forbidden) {
			t.Fatalf("the default render carries %q", forbidden)
		}
	}
}

// TestCommentTriggerRefusesEveryOtherProvider pins the fail-closed rule at the
// service level: the trigger is a GitHub event, so any other host refuses with
// bad-settings instead of rendering a file that host would ignore.
func TestCommentTriggerRefusesEveryOtherProvider(t *testing.T) {
	t.Parallel()
	for _, ci := range []string{"forgejo", "gitlab"} {
		_, ref := RenderWorkflowSpec(WorkflowSpec{CI: ci, Agent: "claude", CommentTrigger: true})
		if ref == nil || ref.Code != codeBadSettings {
			t.Fatalf("RenderWorkflowSpec(%s, trigger) refusal = %v, want bad-settings", ci, ref)
		}
		want := "--comment-trigger needs --ci github: the /verdict trigger is a GitHub issue comment"
		if ref.Message != want {
			t.Fatalf("refusal message = %q, want %q", ref.Message, want)
		}
	}
}

// TestCommentTriggerRunWritesTheTriggeredWorkflow proves the service writes the
// triggered bytes and refuses another provider before touching disk.
func TestCommentTriggerRunWritesTheTriggeredWorkflow(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	req := ciRequest(repo, githubTarget(t))
	req.CommentTrigger = true
	resp := mustRunInit(t, req)
	if resp.Data.WorkflowStatus != statusCreate {
		t.Fatalf("workflow_status = %q, want %q", resp.Data.WorkflowStatus, statusCreate)
	}
	wrote, err := os.ReadFile(filepath.Join(repo, ".github", "workflows", "verdict-review.yml"))
	if err != nil {
		t.Fatalf("the workflow did not land: %v", err)
	}
	if !bytes.Equal(wrote, []byte(mustTriggeredRender(t))) {
		t.Fatalf("the written workflow is not the triggered render:\n%s", wrote)
	}

	refused := t.TempDir()
	req = ciRequest(refused, gitlabTarget(t))
	req.CommentTrigger = true
	_, ref := Run(t.Context(), req)
	if ref == nil || ref.Code != codeBadSettings {
		t.Fatalf("gitlab with the trigger refusal = %v, want bad-settings", ref)
	}
	if _, err := os.Lstat(filepath.Join(refused, reviewDotName)); !os.IsNotExist(err) {
		t.Fatalf("the refused run created %s (err=%v)", reviewDotName, err)
	}
}

// TestScopedFieldsWithoutAProviderRefuse proves the service is the second gate
// the CLI's --ci-scoped rule needs: a facade or MCP caller hands these fields
// over without argv, and a silently ignored `CommentTrigger: true` would be a
// promise the run never kept.
func TestScopedFieldsWithoutAProviderRefuse(t *testing.T) {
	t.Parallel()
	const anyProvider = "github|forgejo|gitlab"
	for _, tc := range []scopedFieldCase{
		{
			name: "comment trigger", want: "--comment-trigger needs --ci github",
			set: func(r *Request) { r.CommentTrigger = true },
		},
		{name: "print", want: "--print needs --ci " + anyProvider, set: func(r *Request) { r.Print = true }},
		{name: "agent", want: "--agent needs --ci " + anyProvider, set: func(r *Request) { r.Agent = "claude" }},
		{
			name: "key secret", want: "--key-secret needs --ci " + anyProvider,
			set: func(r *Request) { r.KeySecret = "REVIEW_KEY" },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertScopedFieldRefused(t, &tc)
		})
	}
}

// scopedFieldCase is one workflow setting the service must refuse when the
// request names no provider.
type scopedFieldCase struct {
	set  func(*Request)
	name string
	want string
}

// assertScopedFieldRefused proves one unscoped field refuses typed and leaves
// the tree untouched.
func assertScopedFieldRefused(t *testing.T, tc *scopedFieldCase) {
	t.Helper()
	repo := t.TempDir()
	req := &Request{Preset: "standard"}
	req.ReadRequest = core.ReadRequest{
		Runtime:  &core.Runtime{Commander: cleanCommander{}},
		RepoRoot: repo,
	}
	tc.set(req)
	_, ref := Run(t.Context(), req)
	if ref == nil || ref.Code != codeBadSettings {
		t.Fatalf("refusal = %v, want %s", ref, codeBadSettings)
	}
	if ref.Message != tc.want {
		t.Fatalf("refusal message = %q, want %q", ref.Message, tc.want)
	}
	if _, err := os.Lstat(filepath.Join(repo, reviewDotName)); !os.IsNotExist(err) {
		t.Fatalf("the refused run created %s (err=%v)", reviewDotName, err)
	}
}

// TestPrintCommandReproducesTheCommentTrigger proves the --print next line
// names the flag: the command it hands back renders the bytes it printed.
func TestPrintCommandReproducesTheCommentTrigger(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	var out bytes.Buffer
	req := ciRequest(repo, githubTarget(t))
	req.Print = true
	req.CommentTrigger = true
	req.Human = &out
	resp := mustRunInit(t, req)
	if want := "write it with: verdict init --ci github --comment-trigger"; resp.Next != want {
		t.Fatalf("next = %q, want %q", resp.Next, want)
	}
	if !strings.Contains(out.String(), "  issue_comment:") {
		t.Fatalf("--print did not render the triggered workflow:\n%s", out.String())
	}
}
