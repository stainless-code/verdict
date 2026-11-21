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
				"          # verdict never bakes a permission flag: this agent's documented one is " +
					"--permission-mode acceptEdits,",
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

// checkRenderedWorkflow pins one agent's rendered lines.
func checkRenderedWorkflow(t *testing.T, tc renderCase) {
	t.Helper()
	body, ref := RenderWorkflow(tc.agent, tc.secret)
	if ref != nil {
		t.Fatalf("render: %s: %s", ref.Code, ref.Message)
	}
	for _, want := range tc.lines {
		assertWorkflowLine(t, string(body), want)
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
	body, ref := RenderWorkflow(tc.agent, tc.secret)
	if ref != nil {
		t.Fatalf("render: %s: %s", ref.Code, ref.Message)
	}
	step := workflowStep(string(body), "Install the review skill")
	if step == "" {
		t.Fatal("the render carries no skill step")
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
	body, ref := RenderWorkflow("claude", "")
	if ref != nil {
		t.Fatalf("render: %s: %s", ref.Code, ref.Message)
	}
	for line := range strings.SplitSeq(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "verdict run ") {
			continue
		}
		if strings.Contains(trimmed, "--permission-mode") {
			t.Fatalf("the agent step bakes the permission flag into argv: %s", trimmed)
		}
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
