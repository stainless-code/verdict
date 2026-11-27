package initcore

import (
	"slices"
	"strings"
	"testing"
)

// wiredCase is one agent-to-key wiring expectation: the literal pair the
// render must carry (the variable the agent reads, the repository secret
// holding it), the model flag, the skill agent, and the permission hint, or the
// refusal code and hint it must answer with.
type wiredCase struct {
	agent      string
	keySecret  string
	wantKey    string
	wantSecret string
	wantSkill  string
	wantModel  string
	wantPerm   string
	wantCode   string
	wantInHint string
}

// TestWiredOptions pins the agent-to-key wiring as literal pairs: the catalog
// row's documented variable, the repository secret name beside it, the model
// flag, and the permission hint. The two key names stay distinct, so an
// explicit `--key-secret REVIEW_KEY` renames the secret without renaming the
// variable the agent reads.
func TestWiredOptions(t *testing.T) {
	t.Parallel()
	for _, tc := range []wiredCase{
		{
			agent:      "claude",
			wantKey:    "ANTHROPIC_API_KEY",
			wantSecret: "ANTHROPIC_API_KEY",
			wantSkill:  "claude",
			wantModel:  "--model",
			wantPerm:   "--permission-mode acceptEdits",
		},
		{
			agent:      "claude",
			keySecret:  "REVIEW_KEY",
			wantKey:    "ANTHROPIC_API_KEY",
			wantSecret: "REVIEW_KEY",
			wantSkill:  "claude",
			wantModel:  "--model",
			wantPerm:   "--permission-mode acceptEdits",
		},
		{
			agent:      "codex",
			wantKey:    "OPENAI_API_KEY",
			wantSecret: "OPENAI_API_KEY",
			wantSkill:  "codex",
			wantModel:  "--model",
			wantPerm:   "--sandbox workspace-write",
		},
		{
			agent:      "opencode",
			keySecret:  "OPENCODE_ZEN_KEY",
			wantKey:    "OPENCODE_ZEN_KEY",
			wantSecret: "OPENCODE_ZEN_KEY",
			wantSkill:  "opencode",
			wantModel:  "--model",
		},
		{
			agent:      "amp",
			keySecret:  "AMP_API_KEY",
			wantKey:    "AMP_API_KEY",
			wantSecret: "AMP_API_KEY",
			wantSkill:  "agents",
			wantModel:  "--model",
		},
	} {
		t.Run(tc.agent+"/"+tc.keySecret, func(t *testing.T) {
			t.Parallel()
			checkWiring(t, &tc)
		})
	}
}

// checkWiring pins one agent's rendered key variable, repository secret, model
// flag, and permission hint.
func checkWiring(t *testing.T, tc *wiredCase) {
	t.Helper()
	got, ref := wiredOptions(tc.agent, tc.keySecret)
	if ref != nil {
		t.Fatalf("wiredOptions(%q, %q) refused: %s: %s", tc.agent, tc.keySecret, ref.Code, ref.Message)
	}
	for _, field := range []struct{ name, got, want string }{
		{"keyEnv", got.keyEnv, tc.wantKey},
		{"keySecret", got.keySecret, tc.wantSecret},
		{"skillAgent", got.skillAgent, tc.wantSkill},
		{"modelFlag", got.modelFlag, tc.wantModel},
		{"permission", got.permission, tc.wantPerm},
	} {
		if field.got != field.want {
			t.Fatalf("wiredOptions(%q, %q).%s = %q, want %q",
				tc.agent, tc.keySecret, field.name, field.got, field.want)
		}
	}
}

// TestWiredOptionsRefuse pins the three refusals: an agent with no documented
// key and no explicit secret, a malformed secret name, and an unknown agent.
func TestWiredOptionsRefuse(t *testing.T) {
	t.Parallel()
	for _, tc := range []wiredCase{
		{agent: "amp", wantCode: "bad-settings", wantInHint: "--key-secret"},
		{agent: "claude", keySecret: "anthropic-key", wantCode: "bad-settings", wantInHint: "invalid --key-secret"},
		{agent: "claud", wantCode: "unknown-agent", wantInHint: "known: adal, agy, amp"},
	} {
		t.Run(tc.agent+"/"+tc.keySecret, func(t *testing.T) {
			t.Parallel()
			checkWiringRefusal(t, &tc)
		})
	}
}

// checkWiringRefusal pins one refusal's code and the hint it carries.
func checkWiringRefusal(t *testing.T, tc *wiredCase) {
	t.Helper()
	_, ref := wiredOptions(tc.agent, tc.keySecret)
	if ref == nil || ref.Code != tc.wantCode {
		t.Fatalf("wiredOptions(%q, %q) refusal = %v, want %s", tc.agent, tc.keySecret, ref, tc.wantCode)
	}
	if !strings.Contains(ref.Message, tc.wantInHint) {
		t.Fatalf("refusal %q does not name %q", ref.Message, tc.wantInHint)
	}
}

// renderCase is one rendered-workflow expectation: the agent, the explicit
// secret (empty for a documented default), and the lines the render must carry.
type renderCase struct {
	name   string
	agent  string
	secret string
	lines  []string
}

// TestRenderWorkflowCarriesTheAgentKeyWiring pins the lines the wiring owns:
// the agent name, the key as an env-only secret under the variable the agent
// reads, the model flag, the runner prerequisite the workflow never performs,
// and the catalog row's permission hint as a comment.
func TestRenderWorkflowCarriesTheAgentKeyWiring(t *testing.T) {
	t.Parallel()
	for _, tc := range []renderCase{
		{
			name:  "claude",
			agent: "claude",
			lines: []string{
				"          VERDICT_AGENT: claude",
				"          VERDICT_MODEL: ${{ vars.VERDICT_MODEL }}",
				"          ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}",
				`          if [ -n "${VERDICT_MODEL:-}" ]; then agent_args=(-- --model "$VERDICT_MODEL"); fi`,
				"          # verdict adds only a write scoped to the review bundle " +
					"(--allowedTools=Edit(./.verdict/state/<pr>/**));",
				"          # this agent's broader documented flag, --permission-mode acceptEdits, " +
					"stays yours to append inside agent_args.",
				"          # Install claude on the runner before this step: verdict installs",
				"          # itself and never downloads an agent CLI.",
				"          #   curl -fsSL https://claude.ai/install.sh | bash",
			},
		},
		{
			name:   "claude naming another repository secret",
			agent:  "claude",
			secret: "REVIEW_KEY",
			lines: []string{
				"          ANTHROPIC_API_KEY: ${{ secrets.REVIEW_KEY }}",
			},
		},
		{
			name:   "amp with an explicit secret",
			agent:  "amp",
			secret: "AMP_API_KEY",
			lines: []string{
				"          VERDICT_AGENT: amp",
				"          AMP_API_KEY: ${{ secrets.AMP_API_KEY }}",
				"          # This agent has no documented permission flag.",
				"          #   npm install -g @ampcode/cli",
			},
		},
		{
			name:  "codex",
			agent: "codex",
			lines: []string{
				"          OPENAI_API_KEY: ${{ secrets.OPENAI_API_KEY }}",
				"          # verdict never bakes a permission flag: this agent's documented one is " +
					"--sandbox workspace-write,",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkRenderedWorkflow(t, tc)
		})
	}
}

// checkRenderedWorkflow pins one agent's rendered lines for the Actions
// providers: the agent/key seam does not move between them. GitLab renders the
// same seam in its own vocabulary, graded by
// TestGitLabWorkflowCarriesTheAgentKeyWiring.
func checkRenderedWorkflow(t *testing.T, tc renderCase) {
	t.Helper()
	for _, ci := range Providers() {
		if ci.name == "gitlab" {
			continue
		}
		body, ref := RenderWorkflow(ci.name, tc.agent, tc.secret)
		if ref != nil {
			t.Fatalf("render %s: %s: %s", ci.name, ref.Code, ref.Message)
		}
		for _, want := range tc.lines {
			assertWorkflowLine(t, string(body), want)
		}
	}
}

// TestRenderWorkflowInstallsASkillAgentTheInstallerAccepts pins the step that
// used to fail on 36 of the 46 launchable rows: an agent whose catalog row
// declares no skills directory installs the skill into the shared agents
// directory, because `verdict skill install --agent amp` is `unknown-agent`.
func TestRenderWorkflowInstallsASkillAgentTheInstallerAccepts(t *testing.T) {
	t.Parallel()
	for _, tc := range []skillStepCase{
		{name: "shared directory", agent: "amp", secret: "AMP_API_KEY", env: "VERDICT_SKILL_AGENT", value: "agents"},
		{name: "own directory", agent: "claude", env: "VERDICT_AGENT", value: "claude"},
		{name: "own directory, second agent", agent: "codex", env: "VERDICT_AGENT", value: "codex"},
	} {
		t.Run(tc.name+"/"+tc.agent, func(t *testing.T) {
			t.Parallel()
			checkSkillStep(t, &tc)
		})
	}
}

// skillStepCase is one skill-step expectation: the agent, the explicit secret,
// and the env assignment the step must carry.
type skillStepCase struct {
	name   string
	agent  string
	secret string
	env    string
	value  string
}

// checkSkillStep pins one agent's skill step: the env assignment, the argument
// that reads it, and the absence of the other variable.
func checkSkillStep(t *testing.T, tc *skillStepCase) {
	t.Helper()
	for _, ci := range Providers() {
		body, ref := RenderWorkflow(ci.name, tc.agent, tc.secret)
		if ref != nil {
			t.Fatalf("render %s: %s: %s", ci.name, ref.Code, ref.Message)
		}
		checkSkillStepFor(t, ci.name, string(body), tc)
	}
}

// checkSkillStepFor pins one provider's skill step. GitLab has no steps: the
// whole job is one shell, so the agent name lives in the variables block and
// the script entry reads that variable.
func checkSkillStepFor(t *testing.T, ciName, body string, tc *skillStepCase) {
	t.Helper()
	if ciName == "gitlab" {
		checkGitLabSkillStep(t, body, tc)
		return
	}
	step := workflowStep(body, "Install the review skill")
	if step == "" {
		t.Fatalf("%s: the render carries no skill step", ciName)
	}
	for _, want := range []string{
		"          " + tc.env + ": " + tc.value,
		`          verdict skill install --agent "$` + tc.env + `"`,
	} {
		if !strings.Contains(step, want) {
			t.Fatalf("skill step carries no line %q:\n%s", want, step)
		}
	}
	if other := otherSkillEnv(tc.env); strings.Contains(step, other+": ") {
		t.Fatalf("skill step names %s as well:\n%s", other, step)
	}
}

// checkGitLabSkillStep pins the GitLab skill wiring: the variable the installer
// is called with, and whether the mapped variable name is the only one present.
func checkGitLabSkillStep(t *testing.T, body string, tc *skillStepCase) {
	t.Helper()
	call := "      verdict skill install --agent \"$" + tc.env + "\""
	assertWorkflowLine(t, body, call)
	if tc.env == "VERDICT_SKILL_AGENT" {
		assertWorkflowLine(t, body, "    VERDICT_SKILL_AGENT: "+tc.value)
		assertWorkflowLine(t, body, "    VERDICT_AGENT: "+tc.agent)
		return
	}
	assertWorkflowLine(t, body, "    VERDICT_AGENT: "+tc.value)
	if strings.Contains(body, "VERDICT_SKILL_AGENT") {
		t.Fatalf("a row with its own skills directory names the shared one:\n%s", body)
	}
}

// workflowStep returns one step's lines, from its name to the next step.
func workflowStep(body, name string) string {
	lines := strings.Split(body, "\n")
	start := slices.Index(lines, "      - name: "+name)
	if start < 0 {
		return ""
	}
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "      - name: ") {
			return strings.Join(lines[start:i], "\n")
		}
	}
	return strings.Join(lines[start:], "\n")
}

// otherSkillEnv names the skill-step variable the step must not carry.
func otherSkillEnv(env string) string {
	if env == "VERDICT_AGENT" {
		return "VERDICT_SKILL_AGENT"
	}
	return "VERDICT_AGENT"
}

// TestRenderWorkflowKeepsThePermissionHintOutOfArgv proves the STA-463 rule:
// the hint is documentation, never an argument verdict bakes.
func TestRenderWorkflowKeepsThePermissionHintOutOfArgv(t *testing.T) {
	t.Parallel()
	for _, ci := range Providers() {
		assertPermissionHintNotInArgv(t, ci)
	}
}

// assertPermissionHintNotInArgv proves one provider's agent step keeps the
// catalog's permission hint documentation, never argv.
func assertPermissionHintNotInArgv(t *testing.T, ci *ciProvider) {
	t.Helper()
	body, ref := RenderWorkflow(ci.Name(), "claude", "")
	if ref != nil {
		t.Fatalf("render %s: %s: %s", ci.Name(), ref.Code, ref.Message)
	}
	for line := range strings.SplitSeq(string(body), "\n") {
		checkPermissionArgvLine(t, ci.Name(), line)
	}
}

// checkPermissionArgvLine grades one rendered line.
func checkPermissionArgvLine(t *testing.T, ci, line string) {
	t.Helper()
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "verdict run ") {
		return
	}
	if strings.Contains(trimmed, "--permission-mode") {
		t.Fatalf("%s: the agent step bakes the permission flag into argv: %s", ci, trimmed)
	}
}

// assertWorkflowLine fails unless the body carries want as a whole line.
func assertWorkflowLine(t *testing.T, body, want string) {
	t.Helper()
	for line := range strings.SplitSeq(body, "\n") {
		if line == want {
			return
		}
	}
	t.Fatalf("workflow carries no line %q", want)
}

// providerPathCase is one --ci target's literal coordinates: the two directory
// components init pins, the file it writes, and the path the envelope names.
type providerPathCase struct { //nolint:govet // the case mirrors the provider registry it grades
	name string
	dirs []string
	base string
	path string
}

// TestProvidersDeclareTheirPaths pins every provider's coordinates as
// literals, so a new row cannot land in another host's directory.
func TestProvidersDeclareTheirPaths(t *testing.T) {
	t.Parallel()
	want := []providerPathCase{
		{
			name: "github",
			dirs: []string{".github", ".github/workflows"},
			base: "verdict-review.yml",
			path: ".github/workflows/verdict-review.yml",
		},
		{
			name: "forgejo",
			dirs: []string{".forgejo", ".forgejo/workflows"},
			base: "verdict-review.yml",
			path: ".forgejo/workflows/verdict-review.yml",
		},
		{
			name: "gitlab",
			dirs: []string{".gitlab", ".gitlab/ci"},
			base: "verdict-review.yml",
			path: ".gitlab/ci/verdict-review.yml",
		},
	}
	got := Providers()
	if len(got) != len(want) {
		t.Fatalf("Providers() = %d entries, want %d", len(got), len(want))
	}
	for i, tc := range want {
		assertProviderPath(t, got[i], tc)
	}
	assertProviderRoots(t)
}

// assertProviderPath pins one provider's coordinates against its literal row.
func assertProviderPath(t *testing.T, ci *ciProvider, want providerPathCase) {
	t.Helper()
	if ci.name != want.name || !slices.Equal(ci.dirs, want.dirs) {
		t.Fatalf("provider = {name:%q dirs:%q}, want %+v", ci.name, ci.dirs, want)
	}
	if ci.base != want.base || ci.path() != want.path {
		t.Fatalf("provider = {base:%q path:%q}, want %+v", ci.base, ci.path(), want)
	}
}

// assertProviderRoots pins the --ci values and the one provider that plans a
// second file at the repository root.
func assertProviderRoots(t *testing.T) {
	t.Helper()
	if values := CIValues(); values != "github|forgejo|gitlab" {
		t.Fatalf("CIValues() = %q, want %q", values, "github|forgejo|gitlab")
	}
	root, ok := LookupCI("gitlab")
	if !ok || root.root == nil || root.root.name != ".gitlab-ci.yml" {
		t.Fatalf("the gitlab provider plans no .gitlab-ci.yml root")
	}
	for _, name := range []string{"github", "forgejo"} {
		if single, ok := LookupCI(name); !ok || single.root != nil {
			t.Fatalf("%s plans a root file; only GitLab does", name)
		}
	}
}

// TestLookupCIRejectsAnUnknownProvider proves the registry is the only list:
// a value no row carries is not silently rendered as github.
func TestLookupCIRejectsAnUnknownProvider(t *testing.T) {
	t.Parallel()
	if _, ok := LookupCI("bogus"); ok {
		t.Fatal("LookupCI accepted an unknown provider")
	}
	_, ref := RenderWorkflow("bogus", "claude", "")
	if ref == nil || ref.Code != "bad-settings" {
		t.Fatalf("RenderWorkflow(bogus) refusal = %v, want bad-settings", ref)
	}
	if !strings.Contains(ref.Message, "github|forgejo|gitlab") {
		t.Fatalf("the refusal does not name the accepted values: %q", ref.Message)
	}
}

// TestForgejoWorkflowCarriesTheProviderContract pins the Forgejo render's
// provider-specific lines as literals: the host context, the automatic token,
// the bundled provider marker, the cleared tokens, the fully-qualified action,
// and the two absences that keep the workflow runnable on Forgejo.
func TestForgejoWorkflowCarriesTheProviderContract(t *testing.T) {
	t.Parallel()
	body, ref := RenderWorkflow("forgejo", "claude", "")
	if ref != nil {
		t.Fatalf("render forgejo: %s: %s", ref.Code, ref.Message)
	}
	workflow := string(body)
	assertForgejoHeader(t, workflow)
	assertForgejoLines(t, workflow)
	assertForgejoAbsences(t, workflow)
	if got := strings.Count(workflow, "--provider forgejo"); got != 1 {
		t.Fatalf("the Forgejo render carries --provider forgejo %d times, want 1 (the fetch step)", got)
	}
	wantSteps := []string{
		"Checkout",
		"Install verdict",
		"Install the review skill",
		"Fetch the PR bundle",
		"Agent review (verdict run --agent, your agent + your key)",
		"Post the review",
		"Review summary (advisory, never gates)",
		"Merge gate (findings + freshness + coverage + live threads)",
	}
	if steps := workflowStepNames(workflow); !slices.Equal(steps, wantSteps) {
		t.Fatalf("Forgejo step names = %q, want %q", steps, wantSteps)
	}
}

// assertForgejoHeader pins the generated header's provider name.
func assertForgejoHeader(t *testing.T, workflow string) {
	t.Helper()
	first, _, _ := strings.Cut(workflow, "\n")
	if want := "# Generated by `verdict init --ci forgejo`. Review before committing."; first != want {
		t.Fatalf("first line = %q, want %q", first, want)
	}
}

// assertForgejoLines pins the provider-specific lines as literals.
func assertForgejoLines(t *testing.T, workflow string) {
	t.Helper()
	for _, want := range []string{
		"          FORGEJO_HOST: ${{ github.server_url }}",
		"          FORGEJO_TOKEN: ${{ github.token }}",
		"          FORGEJO_TOKEN: \"\"",
		"          GITHUB_TOKEN: \"\"",
		"          GH_TOKEN: \"\"",
		`          verdict fetch "$VERDICT_REPOSITORY" "$VERDICT_PR" --provider forgejo`,
		"        uses: https://github.com/stainless-code/verdict/setup-verdict@" +
			SetupVerdictPin + " # " + SetupVerdictVersion,
	} {
		assertWorkflowLine(t, workflow, want)
	}
}

// assertForgejoAbsences pins what the Forgejo render must not carry.
func assertForgejoAbsences(t *testing.T, workflow string) {
	t.Helper()
	for _, forbidden := range []string{"permissions:", "Readiness pre-check", "secrets.GITHUB_TOKEN"} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("the Forgejo render carries %q", forbidden)
		}
	}
}

// TestGitLabWorkflowCarriesTheProviderContract pins the GitLab render's
// provider-specific lines as literals: the merge-request rule with its fork
// clause, the history the controls are read from, the host, the bundled
// provider marker, the runtime token boundary, and the absences that keep the
// file GitLab YAML rather than an Actions workflow.
func TestGitLabWorkflowCarriesTheProviderContract(t *testing.T) {
	t.Parallel()
	body, ref := RenderWorkflow("gitlab", "claude", "")
	if ref != nil {
		t.Fatalf("render gitlab: %s: %s", ref.Code, ref.Message)
	}
	workflow := string(body)
	assertGitLabHeader(t, workflow)
	assertGitLabLines(t, workflow)
	assertGitLabAbsences(t, workflow)
	if got := strings.Count(workflow, "--provider gitlab"); got != 1 {
		t.Fatalf("the GitLab render carries --provider gitlab %d times, want 1 (the fetch block)", got)
	}
}

// assertGitLabHeader pins the generated header: one command, and the host's own
// word for a proposed change.
func assertGitLabHeader(t *testing.T, workflow string) {
	t.Helper()
	lines := strings.Split(workflow, "\n")
	if want := "# Generated by `verdict init --ci gitlab`. Review before committing."; lines[0] != want {
		t.Fatalf("first line = %q, want %q", lines[0], want)
	}
	if want := "# Commit this file: every merge request then runs verdict on your runner with"; lines[2] != want {
		t.Fatalf("header line 3 = %q, want %q", lines[2], want)
	}
}

// assertGitLabLines pins the provider-specific lines as literals.
func assertGitLabLines(t *testing.T, workflow string) {
	t.Helper()
	for _, want := range []string{
		"verdict-review:",
		"  stage: test",
		`    - if: '$CI_PIPELINE_SOURCE == "merge_request_event" && $CI_MERGE_REQUEST_SOURCE_PROJECT_PATH == $CI_PROJECT_PATH'`,
		`    GIT_DEPTH: "0"`,
		"    VERDICT_REPOSITORY: $CI_PROJECT_PATH",
		"    VERDICT_PR: $CI_MERGE_REQUEST_IID",
		"    VERDICT_AGENT: claude",
		"    GITLAB_HOST: $CI_SERVER_FQDN",
		"  interruptible: true",
		"  timeout: 30 minutes",
		`      curl -fsSL "https://raw.githubusercontent.com/stainless-code/verdict/` + SetupVerdictPin + `/install.sh" ` + `\`,
		`      verdict skill install --agent "$VERDICT_AGENT"`,
		`      verdict fetch "$VERDICT_REPOSITORY" "$VERDICT_PR" --provider gitlab`,
		`      verdict ready "$VERDICT_REPOSITORY" "$VERDICT_PR" --check`,
		"        unset GITLAB_TOKEN GLAB_TOKEN GITLAB_ACCESS_TOKEN OAUTH_TOKEN CI_JOB_TOKEN GLAB_ENABLE_CI_AUTOLOGIN GH_TOKEN GITHUB_TOKEN",
		`        verdict run "$VERDICT_REPOSITORY" "$VERDICT_PR" --agent "$VERDICT_AGENT" "${agent_args[@]}"`,
		"        --max-severity critical --require-head --live auto",
	} {
		assertWorkflowLine(t, workflow, want)
	}
}

// assertGitLabAbsences pins what the GitLab render must not carry: an Actions
// workflow's keys, a step env, or a named model key.
func assertGitLabAbsences(t *testing.T, workflow string) {
	t.Helper()
	for _, forbidden := range []string{
		"uses:", "permissions:", "on:", "env:", "pull_request", "concurrency:",
		"secrets.", "ANTHROPIC_API_KEY", "GITLAB_TOKEN:",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("the GitLab render carries %q", forbidden)
		}
	}
}

// TestGitLabWorkflowCarriesTheAgentKeyWiring pins the agent/key seam in
// GitLab's own vocabulary: the key names no variable on the default row, one
// alias line when the CI/CD variable name differs, and the name to create when
// the catalog row documents no key variable at all.
func TestGitLabWorkflowCarriesTheAgentKeyWiring(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		agent     string
		secret    string
		keyInFile string
		lines     []string
		absent    []string
		keyCount  int
	}{
		{
			name:  "claude with the documented key variable",
			agent: "claude",
			lines: []string{
				"    VERDICT_AGENT: claude",
				"      # verdict adds only a write scoped to the review bundle " +
					"(--allowedTools=Edit(./.verdict/state/<pr>/**));",
				"      #   curl -fsSL https://claude.ai/install.sh | bash",
			},
			absent: []string{"ANTHROPIC_API_KEY", "VERDICT_SKILL_AGENT"},
		},
		{
			name:   "claude naming another CI/CD variable",
			agent:  "claude",
			secret: "REVIEW_KEY",
			lines: []string{
				"    ANTHROPIC_API_KEY: $REVIEW_KEY",
			},
			keyCount:  1,
			keyInFile: "ANTHROPIC_API_KEY",
		},
		{
			name:   "amp with an explicit variable",
			agent:  "amp",
			secret: "AMP_API_KEY",
			lines: []string{
				"    VERDICT_AGENT: amp",
				"    VERDICT_SKILL_AGENT: agents",
				"      verdict skill install --agent \"$VERDICT_SKILL_AGENT\"",
				"    # Set AMP_API_KEY as a masked project variable: this",
				"      # This agent has no documented permission flag.",
				"      #   npm install -g @ampcode/cli",
			},
			absent:    []string{"ANTHROPIC_API_KEY", "AMP_API_KEY: $AMP_API_KEY"},
			keyCount:  1,
			keyInFile: "AMP_API_KEY",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkGitLabWiring(t, tc.agent, tc.secret, tc.lines, tc.absent, tc.keyCount, tc.keyInFile)
		})
	}
}

// checkGitLabWiring pins one GitLab render's wiring lines, its absences, and
// how many times the key variable is named at all.
func checkGitLabWiring(t *testing.T, agent, secret string, lines, absent []string, keyCount int, key string) {
	t.Helper()
	body, ref := RenderWorkflow("gitlab", agent, secret)
	if ref != nil {
		t.Fatalf("render gitlab/%s: %s: %s", agent, ref.Code, ref.Message)
	}
	workflow := string(body)
	for _, want := range lines {
		assertWorkflowLine(t, workflow, want)
	}
	for _, forbidden := range absent {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("the GitLab render carries %q:\n%s", forbidden, workflow)
		}
	}
	if key == "" {
		return
	}
	if got := strings.Count(workflow, key); got != keyCount {
		t.Fatalf("the render names %q %d times, want %d", key, got, keyCount)
	}
}

// TestGitLabRootFileShape pins the second planned file as literals: the
// generated header, the one include, and the snippet an operator adds to a root
// pipeline init left alone.
func TestGitLabRootFileShape(t *testing.T) {
	t.Parallel()
	path, body, ok := RootFile("gitlab")
	if !ok {
		t.Fatal("RootFile(gitlab) reports no second file")
	}
	if path != ".gitlab-ci.yml" {
		t.Fatalf("RootFile(gitlab) path = %q, want .gitlab-ci.yml", path)
	}
	want := "# Generated by `verdict init --ci gitlab`. Review before committing.\n" +
		"#\n" +
		"# Commit this file: every merge request then runs verdict on your runner with\n" +
		"# your agent and your own model key. Regenerate after upgrading verdict.\n" +
		"\n" +
		"# One include, so this file stays yours to edit. Delete the include to turn\n" +
		"# the automatic review off; .gitlab/ci/verdict-review.yml is the pipeline.\n" +
		"\n" +
		"include:\n" +
		"  - local: .gitlab/ci/verdict-review.yml\n"
	if string(body) != want {
		t.Fatalf("RootFile(gitlab) body = %q, want %q", body, want)
	}
	if snippet := IncludeSnippet("gitlab"); snippet != "include:\n  - local: .gitlab/ci/verdict-review.yml\n" {
		t.Fatalf("IncludeSnippet(gitlab) = %q", snippet)
	}
	if path, body, ok := RootFile("github"); ok || path != "" || body != nil {
		t.Fatalf("RootFile(github) = %q, %q, %v; want no second file", path, body, ok)
	}
	if snippet := IncludeSnippet("forgejo"); snippet != "" {
		t.Fatalf("IncludeSnippet(forgejo) = %q, want the empty string", snippet)
	}
}

// workflowStepNames answers one render's step names in order.
func workflowStepNames(workflow string) []string {
	var steps []string
	for line := range strings.SplitSeq(workflow, "\n") {
		if name, ok := strings.CutPrefix(line, "      - name: "); ok {
			steps = append(steps, name)
		}
	}
	return steps
}
