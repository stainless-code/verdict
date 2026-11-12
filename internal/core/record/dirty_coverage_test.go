package recordcore_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

func TestRequiredCoverageDirtyLocalBundle(t *testing.T) {
	const digest = "318d35b3738f5e9aadfdaef4e9c0f2b1772e60605db9a0b29f7008fd67c6e268"
	const dir = ".verdict/state/local-cov"
	tests := []struct {
		name        string
		coverage    string
		wantRefusal bool
	}{
		{name: "complete", coverage: `[{"file":"notes.txt","ranges":"all"}]`},
		{name: "empty", coverage: `[]`, wantRefusal: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			review := `{"schema_version":"1","body":"Reviewed the entire tracked change.","approved":true,"comments":[],"coverage":` + tc.coverage + `}`
			coverageBundle(t, coverageDiff, review)
			source := `{"kind":"worktree","dirty":true,"head_sha":null,"base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","diff_sha256":"` + digest + `"}`
			writeFile(t, dir+"/source.json", source)
			ref := runCoverage(t, true, false)
			if tc.wantRefusal {
				if ref == nil || ref.Code != "coverage-incomplete" {
					t.Fatalf("record refusal = %v, want coverage-incomplete", ref)
				}
				return
			}
			if ref != nil {
				t.Fatalf("record refusal: %v", ref)
			}

			assertDirtyCoverageGate(t, false)
			writeFile(t, dir+"/source.json", strings.Replace(source, digest, strings.Repeat("c", 64), 1))
			assertDirtyCoverageGate(t, true)
			writeFile(t, dir+"/source.json", source)
			writeFile(t, dir+"/full.diff", coverageDiff+"\n")
			assertDirtyCoverageGate(t, true)
			writeFile(t, dir+"/full.diff", coverageDiff)
			writeFile(t, dir+"/record.json", `{"head":"","diff_sha256":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","coverage":{"percent":100,"attested":3,"total":3,"gaps":0}}`)
			assertDirtyCoverageGate(t, true)
		})
	}
}

func assertDirtyCoverageGate(t *testing.T, wantFail bool) {
	t.Helper()
	resp, refusal := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: core.ReadRequest{Runtime: testRuntime(&collector{}), Repo: "acme/app", Pr: coverageSlug},
		Flags:       core.ReportFlags{Check: "informational", Coverage: "require", Live: "skip"},
	})
	if refusal != nil {
		t.Fatalf("gate refusal: %v", refusal)
	}
	if resp.GateFailed != wantFail {
		t.Fatalf("GateFailed = %v, want %v: %v", resp.GateFailed, wantFail, resp.CheckLines)
	}
}
