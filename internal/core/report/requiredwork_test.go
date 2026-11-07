package reportcore_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

// writeResolvedCapture writes the real resolved run bundle for one capture.
func writeResolvedCapture(t *testing.T, resolved *config.Resolved) {
	t.Helper()
	rs := &config.RunState{
		BaseSHA:  "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Trusted:  true,
		Resolved: resolved,
	}
	dir := filepath.Join(".", ".verdict", "state", "5101")
	if serr := config.WriteRunState(dir, rs); serr != nil {
		t.Fatalf("WriteRunState: %v", serr)
	}
	if _, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: dir, Root: ".", BaseSHA: rs.BaseSHA, AllowHead: true,
	}); ref != nil {
		t.Fatalf("controls.Resolve: %v", ref)
	}
}

// resolveCapture resolves one repository configuration over the fixture file.
func resolveCapture(t *testing.T, repo *core.Config, personal *config.PersonalSettings) *config.Resolved {
	t.Helper()
	resolved, ref := config.Resolve(&config.Inputs{
		Repo: repo, Personal: personal,
		Files: []config.FileInput{{Path: "notes.txt"}},
	})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	return resolved
}

// TestReportCleanNoOpenRequirementPassesWithNoPriorPost proves the no-open
// check is decided from the current findings: a clean typed capture passes
// the gate with no prior post and no appended outcome.
func TestReportCleanNoOpenRequirementPassesWithNoPriorPost(t *testing.T) {
	req := fixture(t, "[]")
	repo := &core.Config{SchemaVersion: "1", Requirements: &core.ConfigRequirements{
		BlockClauses:  []core.BlockClause{{Name: "all", Threshold: "critical", Paths: []string{"notes.txt"}}},
		RequireChecks: []string{"no-open-above-block"},
	}}
	writeResolvedCapture(t, resolveCapture(t, repo, nil))
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req, Flags: core.ReportFlags{Check: "critical"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.GateFailed {
		t.Fatalf("clean no-open requirement failed without any prior post: %+v", resp.Data.Compliance)
	}
	if resp.Data.Compliance == nil || !resp.Data.Compliance.Satisfied || resp.Data.Compliance.Pending != 0 {
		t.Fatalf("compliance = %+v, want satisfied with no pending check", resp.Data.Compliance)
	}
}

// TestReportDeepGroupWithoutReceiptNeverPasses proves a deep-only group is
// required work: a missing receipt leaves compliance unknown and the gate
// failing instead of passing from an empty finding list.
func TestReportDeepGroupWithoutReceiptNeverPasses(t *testing.T) {
	req := fixture(t, "[]")
	deep := "deep"
	repo := &core.Config{
		SchemaVersion: "1",
		Profiles:      map[string]core.ConfigProfile{"deep": {Depth: &deep}},
		Scopes:        []core.ConfigScope{{Name: "deep", Include: []string{"notes.txt"}, Profile: &deep}},
	}
	writeResolvedCapture(t, resolveCapture(t, repo, nil))
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req, Flags: core.ReportFlags{Check: "critical"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !resp.GateFailed || resp.Data.Compliance == nil || resp.Data.Compliance.Satisfied {
		t.Fatalf("deep gap passed: gateFailed=%v compliance=%+v", resp.GateFailed, resp.Data.Compliance)
	}
	if resp.Data.Compliance.Gaps == 0 {
		t.Fatalf("compliance = %+v, want a deep-work gap", resp.Data.Compliance)
	}
}

// TestReportWarnsEachProfileGapOnce pins the single warning: the compliance
// line already names the gap with its requirement prefix, so the standalone
// profile warning must not repeat it and make one missing receipt read as two
// separate defects.
func TestReportWarnsEachProfileGapOnce(t *testing.T) {
	req := fixture(t, "[]")
	deep := "deep"
	repo := &core.Config{
		SchemaVersion: "1",
		Profiles:      map[string]core.ConfigProfile{"deep": {Depth: &deep}},
		Scopes:        []core.ConfigScope{{Name: "deep", Include: []string{"notes.txt"}, Profile: &deep}},
	}
	writeResolvedCapture(t, resolveCapture(t, repo, nil))
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req, Flags: core.ReportFlags{Check: "critical"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	named := 0
	for _, warning := range resp.Warnings {
		if strings.Contains(warning, "deep validation pending") {
			named++
		}
	}
	if named != 1 {
		t.Fatalf("warnings = %q, want the gap named once", resp.Warnings)
	}
}

// TestReportWarnsEachLensGapOnce pins the same single-warning rule on the
// attestation path: the capped group's gap reaches the operator through the
// compliance line, not twice.
func TestReportWarnsEachLensGapOnce(t *testing.T) {
	req := fixture(t, "[]")
	capped := "capped"
	limit := 1
	repo := &core.Config{
		SchemaVersion: "1",
		Profiles:      map[string]core.ConfigProfile{"capped": {MaxLenses: &limit}},
		Scopes:        []core.ConfigScope{{Name: "capped", Include: []string{"notes.txt"}, Profile: &capped}},
	}
	writeResolvedCapture(t, resolveCapture(t, repo, nil))
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req, Flags: core.ReportFlags{Check: "critical"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	named := 0
	for _, warning := range resp.Warnings {
		if strings.Contains(warning, "capped appears in more than one lens entry") ||
			strings.Contains(warning, "cost.json recorded no lens usage") {
			named++
		}
	}
	if named != 1 {
		t.Fatalf("warnings = %q, want the lens gap named once", resp.Warnings)
	}
}

// TestReportCappedGroupWithoutAttestationNeverPasses proves a max_lenses-only
// group counts as required work: absent attestation stays unknown, not pass.
func TestReportCappedGroupWithoutAttestationNeverPasses(t *testing.T) {
	req := fixture(t, "[]")
	capped := "capped"
	limit := 1
	repo := &core.Config{
		SchemaVersion: "1",
		Profiles:      map[string]core.ConfigProfile{"capped": {MaxLenses: &limit}},
		Scopes:        []core.ConfigScope{{Name: "capped", Include: []string{"notes.txt"}, Profile: &capped}},
	}
	writeResolvedCapture(t, resolveCapture(t, repo, nil))
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req, Flags: core.ReportFlags{Check: "critical"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !resp.GateFailed || resp.Data.Compliance == nil || resp.Data.Compliance.Satisfied {
		t.Fatalf("capped gap passed: gateFailed=%v compliance=%+v", resp.GateFailed, resp.Data.Compliance)
	}
	if resp.Data.Compliance.Gaps == 0 {
		t.Fatalf("compliance = %+v, want a lens-attestation gap", resp.Data.Compliance)
	}
}

// TestReportPrivacyConflictNeverPassesFromEmptyFindings proves a withheld
// required path is unmet even when the stored finding list is empty, and the
// conflict names path and rule without content.
func TestReportPrivacyConflictNeverPassesFromEmptyFindings(t *testing.T) {
	req := fixture(t, "[]")
	repo := &core.Config{SchemaVersion: "1", Requirements: &core.ConfigRequirements{
		BlockClauses: []core.BlockClause{{Name: "secret", Threshold: "critical", Paths: []string{"notes.txt"}}},
	}}
	personal := &config.PersonalSettings{Privacy: config.PersonalPrivacy{Exclusions: []string{"notes.txt"}}}
	writeResolvedCapture(t, resolveCapture(t, repo, personal))
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req, Flags: core.ReportFlags{Check: "critical"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !resp.GateFailed || resp.Data.Compliance == nil || resp.Data.Compliance.Conflicted != 1 {
		t.Fatalf("privacy conflict passed: gateFailed=%v compliance=%+v", resp.GateFailed, resp.Data.Compliance)
	}
	if resp.Data.Compliance.Satisfied {
		t.Fatalf("compliance = %+v, want conflicted and unsatisfied", resp.Data.Compliance)
	}
}
