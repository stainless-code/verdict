package recordcore_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

const (
	dirtyCoverageDigest = "318d35b3738f5e9aadfdaef4e9c0f2b1772e60605db9a0b29f7008fd67c6e268"
	dirtyCoverageDir    = ".verdict/state/local-cov"
	dirtyCoverageSource = `{"kind":"worktree","dirty":true,"worktree_diff_sha256":"318d35b3738f5e9aadfdaef4e9c0f2b1772e60605db9a0b29f7008fd67c6e268","head_sha":null,"base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","diff_sha256":"318d35b3738f5e9aadfdaef4e9c0f2b1772e60605db9a0b29f7008fd67c6e268"}`
)

func dirtyCoverageBundle(t *testing.T, attestation string) {
	t.Helper()
	review := `{"schema_version":"1","body":"Reviewed the entire tracked change.","approved":true,"comments":[],"coverage":` + attestation + `}`
	coverageBundle(t, coverageDiff, review)
	writeFile(t, dirtyCoverageDir+"/source.json", dirtyCoverageSource)
}

// TestRequiredCoverageDirtyLocalEmptyReview requires attestation before
// a dirty review can be recorded.
func TestRequiredCoverageDirtyLocalEmptyReview(t *testing.T) {
	dirtyCoverageBundle(t, `[]`)
	ref := runDirtyCoverage(t, &dirtyDiffCommander{diff: coverageDiff})
	if ref == nil || ref.Code != "coverage-incomplete" {
		t.Fatalf("record refusal = %v, want coverage-incomplete", ref)
	}
}

// TestRequiredCoverageDirtyLocalBundle binds required coverage to matching
// bundle and receipt diff identities.
func TestRequiredCoverageDirtyLocalBundle(t *testing.T) {
	dirtyCoverageBundle(t, `[{"file":"notes.txt","ranges":"all"}]`)
	if ref := runDirtyCoverage(t, &dirtyDiffCommander{diff: coverageDiff}); ref != nil {
		t.Fatalf("record refusal: %v", ref)
	}
	assertDirtyCoverageGate(t, false)
	writeFile(t, dirtyCoverageDir+"/source.json", strings.Replace(dirtyCoverageSource, dirtyCoverageDigest, strings.Repeat("c", 64), 1))
	assertDirtyCoverageGate(t, true)
	writeFile(t, dirtyCoverageDir+"/source.json", dirtyCoverageSource)
	writeFile(t, dirtyCoverageDir+"/full.diff", coverageDiff+"\n")
	assertDirtyCoverageGate(t, true)
	writeFile(t, dirtyCoverageDir+"/full.diff", coverageDiff)
	writeFile(t, dirtyCoverageDir+"/record.json", `{"head":"","diff_sha256":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","coverage":{"percent":100,"attested":3,"total":3,"gaps":0}}`)
	assertDirtyCoverageGate(t, true)
}

func assertDirtyCoverageGate(t *testing.T, wantFail bool) {
	t.Helper()
	rt := testRuntime(&collector{})
	rt.Commander = &dirtyDiffCommander{diff: coverageDiff}
	read := core.ReadRequest{Runtime: rt, Repo: "acme/app", Pr: coverageSlug}
	resp, refusal := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: read,
		Flags:       core.ReportFlags{Check: "informational", Coverage: "require", Live: "skip"},
	})
	if refusal != nil {
		if wantFail && refusal.Code == "coverage-stale" {
			return
		}
		t.Fatalf("gate refusal: %v", refusal)
	}
	if resp.GateFailed != wantFail {
		t.Fatalf("GateFailed = %v, want %v: %v", resp.GateFailed, wantFail, resp.CheckLines)
	}
}

type dirtyDiffCommander struct{ diff string }

func (c *dirtyDiffCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	if !slices.Equal(cmd.Argv, []string{"git", "diff", "HEAD"}) {
		return core.Result{ExitCode: 1}, nil
	}
	return core.Result{Stdout: c.diff}, nil
}

func runDirtyCoverage(t *testing.T, cmd core.Commander) *core.RefusalError {
	t.Helper()
	req := recordRequest(t, &collector{}, coverageSlug)
	req.Flags.RequireCoverage = true
	req.Commander = cmd
	_, ref := recordcore.Run(context.Background(), req)
	return ref
}

// TestDirtyCoverageEditAfterFetch refuses stale record and gate operations.
func TestDirtyCoverageEditAfterFetch(t *testing.T) {
	dirtyCoverageBundle(t, `[{"file":"notes.txt","ranges":"all"}]`)
	cmd := &dirtyDiffCommander{diff: coverageDiff}
	if ref := runDirtyCoverage(t, cmd); ref != nil {
		t.Fatal(ref)
	}
	cmd.diff = "unreviewed tracked edit"
	if ref := runDirtyCoverage(t, cmd); ref == nil || ref.Code != "coverage-stale" {
		t.Fatalf("record refusal = %v, want coverage-stale", ref)
	}
	rt := testRuntime(&collector{})
	rt.Commander = cmd
	read := core.ReadRequest{Runtime: rt, Repo: "acme/app", Pr: coverageSlug}
	_, ref := reportcore.Run(context.Background(), &core.ReportRequest{ReadRequest: read, Flags: core.ReportFlags{Check: "informational", Coverage: "require", Live: "skip"}})
	if ref == nil || ref.Code != "coverage-stale" {
		t.Fatalf("gate refusal = %v, want coverage-stale", ref)
	}
}
