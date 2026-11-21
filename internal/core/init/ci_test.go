package initcore

import (
	"strings"
	"testing"
)

// wiredCase is one agent-to-key wiring expectation: the literal triple the
// render must carry, or the refusal code and hint it must answer with.
type wiredCase struct {
	agent      string
	keySecret  string
	wantKey    string
	wantModel  string
	wantPerm   string
	wantCode   string
	wantInHint string
}

// TestWiredOptions pins the agent-to-key wiring as literal triples: the
// catalog row's documented variable, the model flag, and the permission hint.
func TestWiredOptions(t *testing.T) {
	t.Parallel()
	for _, tc := range []wiredCase{
		{
			agent:     "claude",
			wantKey:   "ANTHROPIC_API_KEY",
			wantModel: "--model",
			wantPerm:  "--permission-mode acceptEdits",
		},
		{
			agent:     "codex",
			wantKey:   "OPENAI_API_KEY",
			wantModel: "--model",
			wantPerm:  "--sandbox workspace-write",
		},
		{
			agent:     "opencode",
			keySecret: "OPENCODE_ZEN_KEY",
			wantKey:   "OPENCODE_ZEN_KEY",
			wantModel: "--model",
		},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			t.Parallel()
			checkWiring(t, &tc)
		})
	}
}

// checkWiring pins one agent's rendered key, model flag, and permission hint.
func checkWiring(t *testing.T, tc *wiredCase) {
	t.Helper()
	got, ref := wiredOptions(tc.agent, tc.keySecret)
	if ref != nil {
		t.Fatalf("wiredOptions(%q, %q) refused: %s: %s", tc.agent, tc.keySecret, ref.Code, ref.Message)
	}
	want := renderOptions{
		agent:      tc.agent,
		keySecret:  tc.wantKey,
		modelFlag:  tc.wantModel,
		permission: tc.wantPerm,
	}
	if got != want {
		t.Fatalf("wiredOptions(%q, %q) = %+v, want %+v", tc.agent, tc.keySecret, got, want)
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

// TestRenderWorkflowCarriesTheAgentKeyWiring pins the four lines the wiring
// owns: the agent name, the key as an env-only secret, the model flag, and the
// catalog row's permission hint as a comment.
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
