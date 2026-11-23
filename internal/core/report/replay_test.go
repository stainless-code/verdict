package reportcore_test

import (
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

// writeCapturedPolicy persists a run capture resolved from cfg, so the replay
// reads a real typed policy instead of a hand-built one.
func writeCapturedPolicy(t *testing.T, cfg *core.Config) {
	t.Helper()
	resolved, ref := config.Resolve(&config.Inputs{
		Repo:  cfg,
		Files: []config.FileInput{{Path: "notes.txt"}},
	})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	rs := &config.RunState{
		BaseSHA:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Trusted:  true,
		Resolved: resolved,
	}
	dir := filepath.Join(".", ".verdict", "state", "5101")
	if serr := config.WriteRunState(dir, rs); serr != nil {
		t.Fatalf("WriteRunState: %v", serr)
	}
	if _, cref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: dir, Root: ".", BaseSHA: rs.BaseSHA, AllowHead: true,
	}); cref != nil {
		t.Fatalf("controls.Resolve: %v", cref)
	}
}

// replayOver stores one verified critical security finding, captures cfg, and
// replays candidate over it. The candidate is written after fixture changes
// directory, so it lands beside the bundle rather than in the caller's cwd.
func replayOver(t *testing.T, cfg *core.Config, candidate string) *reportcore.PolicyReplay {
	t.Helper()
	req := fixture(t, "["+securityFinding+"]")
	writeCapturedPolicy(t, cfg)
	writeFile(t, "candidate.json", candidate)
	replay, ref := reportcore.Replay(&reportcore.Request{ReadRequest: req, Flags: reportcore.Flags{Policy: "candidate.json"}})
	if ref != nil {
		t.Fatalf("Replay refusal: %v", ref)
	}
	return replay
}

// dimension finds one gate dimension by its wire ID.
func dimension(t *testing.T, decision reportcore.Decision, id string) core.GateDimension {
	t.Helper()
	for _, dim := range decision.Dimensions {
		if dim.ID == id {
			return dim
		}
	}
	t.Fatalf("decision has no %q dimension: %+v", id, decision.Dimensions)
	return core.GateDimension{}
}

const strictPolicy = `{"schema_version":"1","requirements":{"block_clauses":[` +
	`{"name":"security-critical","threshold":"critical","categories":["security"]}]}}`

// TestReplayStricterPolicyFailsTheGate proves a candidate policy that adds a
// block clause flips the gate the captured policy passed, and names the
// dimension that changed.
func TestReplayStricterPolicyFailsTheGate(t *testing.T) {
	replay := replayOver(t, &core.Config{SchemaVersion: "1"}, strictPolicy)

	if !replay.Captured.GatePassed {
		t.Fatalf("captured policy fails the gate on an empty policy: %+v", replay.Captured)
	}
	if replay.Candidate.GatePassed {
		t.Fatalf("candidate block clause passed the gate over an open critical security finding: %+v", replay.Candidate)
	}
	if got := dimension(t, replay.Candidate, "compliance"); got.Status != "fail" {
		t.Fatalf("candidate compliance status = %q; want fail (dimension %+v)", got.Status, got)
	}
}

// TestReplayLooserPolicyPassesTheGate proves the reverse direction: dropping
// the captured block clause clears the gate that withheld on it.
func TestReplayLooserPolicyPassesTheGate(t *testing.T) {
	strict := &core.Config{
		SchemaVersion: "1",
		Requirements: &core.ConfigRequirements{BlockClauses: []core.BlockClause{
			{Name: "security-critical", Threshold: "critical", Categories: []string{"security"}},
		}},
	}
	replay := replayOver(t, strict, `{"schema_version":"1"}`)

	if replay.Captured.GatePassed {
		t.Fatalf("captured block clause passed over an open critical security finding: %+v", replay.Captured)
	}
	if !replay.Candidate.GatePassed {
		t.Fatalf("candidate without the clause still failed the gate: %+v", replay.Candidate)
	}
}

// TestReplayIdenticalPolicyKeepsDecision proves the derivation is stable: a
// candidate that resolves to the captured policy changes nothing, so any
// difference in the other cases comes from the policy rather than from noise
// in the loader or the gate.
func TestReplayIdenticalPolicyKeepsDecision(t *testing.T) {
	replay := replayOver(t, &core.Config{SchemaVersion: "1"}, `{"schema_version":"1"}`)

	if !replay.Captured.GatePassed || !replay.Candidate.GatePassed {
		t.Fatalf("identical policies disagree on the gate: captured=%+v candidate=%+v",
			replay.Captured, replay.Candidate)
	}
	if len(replay.Captured.Dimensions) != len(replay.Candidate.Dimensions) {
		t.Fatalf("identical policies produced %d and %d dimensions",
			len(replay.Captured.Dimensions), len(replay.Candidate.Dimensions))
	}
	if replay.Captured.Approved != replay.Candidate.Approved {
		t.Fatalf("identical policies disagree on approval: captured=%v candidate=%v",
			replay.Captured.Approved, replay.Candidate.Approved)
	}
}
