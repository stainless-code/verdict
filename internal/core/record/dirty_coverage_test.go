package recordcore_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

const dirtyCoverageDigest = "318d35b3738f5e9aadfdaef4e9c0f2b1772e60605db9a0b29f7008fd67c6e268"
const dirtyCoverageDir = ".verdict/state/local-cov"
const dirtyCoverageSource = `{"kind":"worktree","dirty":true,"head_sha":null,"base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","diff_sha256":"318d35b3738f5e9aadfdaef4e9c0f2b1772e60605db9a0b29f7008fd67c6e268"}`

func dirtyCoverageBundle(t *testing.T, attestation string) {
	t.Helper()
	review := `{"schema_version":"1","body":"Reviewed the entire tracked change.","approved":true,"comments":[],"coverage":` + attestation + `}`
	coverageBundle(t, coverageDiff, review)
	writeFile(t, dirtyCoverageDir+"/source.json", dirtyCoverageSource)
}

func TestRequiredCoverageDirtyLocalEmptyReview(t *testing.T) {
	dirtyCoverageBundle(t, `[]`)
	ref := runCoverage(t, true, false)
	if ref == nil || ref.Code != "coverage-incomplete" {
		t.Fatalf("record refusal = %v, want coverage-incomplete", ref)
	}
}

func TestRequiredCoverageDirtyLocalBundle(t *testing.T) {
	dirtyCoverageBundle(t, `[{"file":"notes.txt","ranges":"all"}]`)
	if ref := runCoverage(t, true, false); ref != nil {
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
	read := core.ReadRequest{Runtime: testRuntime(&collector{}), Repo: "acme/app", Pr: coverageSlug}
	resp, refusal := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: read,
		Flags:       core.ReportFlags{Check: "informational", Coverage: "require", Live: "skip"},
	})
	if refusal != nil {
		t.Fatalf("gate refusal: %v", refusal)
	}
	if resp.GateFailed != wantFail {
		t.Fatalf("GateFailed = %v, want %v: %v", resp.GateFailed, wantFail, resp.CheckLines)
	}
}
