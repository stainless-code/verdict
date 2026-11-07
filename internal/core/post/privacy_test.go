package postcore

import (
	"context"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
)

// privacyRunState resolves a real capture whose single file is both required
// by a clause and withheld by the personal exclusions.
func privacyRunState(t *testing.T) *config.RunState {
	t.Helper()
	repo := &core.Config{SchemaVersion: "1", Requirements: &core.ConfigRequirements{
		BlockClauses:  []core.BlockClause{{Name: "secret", Threshold: "critical", Paths: []string{"secret/**"}}},
		RequireChecks: []string{"no-open-above-block"},
	}}
	personal := &config.PersonalSettings{Privacy: config.PersonalPrivacy{Exclusions: []string{"secret/**"}}}
	resolved, ref := config.Resolve(&config.Inputs{
		Repo: repo, Personal: personal,
		Files: []config.FileInput{{Path: "secret/keys.go"}},
	})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	return &config.RunState{
		BaseSHA: "bbbb", Trusted: true, Resolved: resolved,
		Root: config.RootSet(repo, "bbbb"), RequiresEvidence: true, InputsSHA256: "feedface",
	}
}

// TestApproveGateRefusesPrivacyConflictDespiteForce proves a positive binding
// approval refuses requirement-unmet when required review is withheld, and
// --force cannot bypass it.
func TestApproveGateRefusesPrivacyConflictDespiteForce(t *testing.T) {
	for _, force := range []bool{false, true} {
		st := policyTestState(nil, privacyRunState(t))
		st.flags.Approve = approveTrue
		st.flags.Force = force
		ref := st.approveGate()
		if ref == nil || ref.Code != CodeRequirementUnmet {
			t.Fatalf("force=%v: approveGate = %v, want requirement-unmet", force, ref)
		}
	}
}

// TestCommentsStayAvailableForPrivacyConflict proves an unbinding comment
// run is not refused by the conflict gate and appends no passed no-open
// observation for the conflicted scope.
func TestCommentsStayAvailableForPrivacyConflict(t *testing.T) {
	rs := privacyRunState(t)
	st := policyTestState(nil, rs)
	st.flags.Approve = approveFalse
	st.flags.DryRun = true
	if ref := st.bindGate(context.Background()); ref != nil {
		t.Fatalf("comment bindGate = %v, want nil (comments stay available)", ref)
	}
	st.recordCompliance(rs, nil)
	for i := range rs.Outcomes {
		obs := rs.Outcomes[i]
		if obs.Check == config.CheckNoOpenAboveBlock && obs.Passed {
			t.Fatalf("conflicted scope recorded a passing no-open observation: %+v", obs)
		}
	}
	if len(rs.Outcomes) == 0 {
		t.Fatal("conflicted scope recorded no failed no-open observation")
	}
}
