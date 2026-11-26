package schemagen

import (
	"context"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	loopcore "github.com/stainless-code/verdict/internal/core/loop"
	"github.com/stainless-code/verdict/internal/core/reviewflow"
)

// TestRuntimeLoopEnvelopeValidates proves the loop envelope validates against
// the generated schema twice: the --print render the service produces (no
// agent, no spawn), and a finished loop's data with every nullable observation
// both present and absent, so a field the schema forgot to widen fails here.
func TestRuntimeLoopEnvelopeValidates(t *testing.T) {
	doc := loadSchemaDocument(t, "envelope.schema.json")
	req := &loopcore.Request{
		Flow:      &reviewflow.Flow{Runtime: fixedRuntime()},
		RepoRoot:  t.TempDir(),
		Repo:      "acme/app",
		Agent:     "claude",
		MaxRounds: 2,
		Timeout:   10 * time.Minute,
		Print:     true,

		MaxSeverity: "important",
	}
	resp, ref := loopcore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("loop --print refusal: %v", ref)
	}
	data := core.Wire(resp.Data)
	assertServiceEnvelope(t, doc, "loop", "acme/app", "", resp.At, resp.Next, resp.Warnings, data)
	assertWireFields(t, data, map[string]any{"stop": "planned", "passed": false, "output": nil})
	if plan := wireList(t, data, "plan"); len(plan) != 10 {
		t.Errorf("plan steps = %d, want 10 for two rounds", len(plan))
	}

	open, exit, cost := 2, 0, 0.42
	output := "/receipts"
	finished := &loopcore.Data{
		Passed: false, Stop: loopcore.StopAgentFailed, Agent: "claude", MaxRounds: 3, MaxSeverity: "important", TimeoutS: 600,
		MaxCostUSD: &cost, CostUSD: &cost, ElapsedMS: 1200, Output: &output,
		Refusal: &loopcore.Refusal{Code: "agent-failed", Message: "claude exited 7"},
		Plan:    []loopcore.PlanStep{{Round: 1, Phase: "fix", When: "gate-failed", Argv: []string{"claude", "-p", "fix"}}},
		Rounds: []loopcore.Round{
			{
				Round: 1, Bundle: "local-loop-x-1", StartedAt: "2026-11-02T12:00:00Z", EndedAt: "2026-11-02T12:01:00Z",
				Open: &open, OpenSigs: []string{"abc"}, Gate: new(false), CostUSD: &cost,
				Fix: &loopcore.Fix{Verify: &loopcore.Verify{Exit: &exit}},
			},
			{
				Round: 2, Bundle: "local-loop-x-2", StartedAt: "2026-11-02T12:01:00Z", EndedAt: "2026-11-02T12:02:00Z",
				OpenSigs: []string{}, Fix: &loopcore.Fix{},
				Refusal: &loopcore.Refusal{Code: "agent-failed", Message: "claude exited 7"},
			},
		},
	}
	assertServiceEnvelope(t, doc, "loop", "acme/app", "", resp.At, "", nil, core.Wire(finished))
}
