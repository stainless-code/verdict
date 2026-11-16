package recordcore_test

import (
	"strings"
	"testing"
)

// generatedTypeScriptDiff changes notes.txt plus a `.gen.ts` path the review
// never names: the shared role heuristic must exempt the second file, so the
// stored receipt risk reads carries no gap for it.
const generatedTypeScriptDiff = "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n" +
	"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n" +
	"diff --git a/clients/ts/src/refusal-codes.gen.ts b/clients/ts/src/refusal-codes.gen.ts\n" +
	"index 3333333..4444444 100644\n--- a/clients/ts/src/refusal-codes.gen.ts\n" +
	"+++ b/clients/ts/src/refusal-codes.gen.ts\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"

// TestMergeCoverageExemptsGeneratedTypeScript pins the receipt risk reads:
// a `.gen.ts` file left out of coverage[] is exempt, not unattested, so the
// stored gaps stay zero and `unreviewed-files` cannot raise the risk floor.
func TestMergeCoverageExemptsGeneratedTypeScript(t *testing.T) {
	coverageBundle(t, generatedTypeScriptDiff, coverageReview("notes.txt", `"coverage":[{"file":"notes.txt","ranges":"all"}],`))
	if ref := runCoverage(t, false, false); ref != nil {
		t.Fatalf("record refused: %+v", ref)
	}
	cov, ok := decodeObject(t, ".verdict/state/"+coverageSlug+"/record.json")["coverage"].(map[string]any)
	if !ok {
		t.Fatalf("record.json carries no coverage receipt")
	}
	if cov["percent"] != float64(100) || cov["gaps"] != float64(0) || cov["exempt"] != float64(1) {
		t.Errorf("coverage receipt = %+v, want percent 100, gaps 0, exempt 1", cov)
	}
}

// A bundle whose every changed file is generated has nothing to attest, so
// --require-coverage must pass rather than refuse on a 0/0 denominator: the
// exemption is automatic and needs no sentinel. The gate is the one place the
// exemption can turn into a false refusal.
func TestMergeCoverageGatePassesWhenEveryFileIsExempt(t *testing.T) {
	coverageBundle(t, generatedDiff, coverageReview("dist/app.min.js", `"coverage":[{"file":"dist/app.min.js","ranges":"all"}],`))
	body, warns, err := mergeCoverage(t, true)
	if err != nil {
		t.Fatalf("exemption-only bundle refused: %v", err)
	}
	for _, w := range warns {
		if strings.Contains(w, "coverage:") {
			t.Errorf("exemption-only bundle warned about coverage: %q", w)
		}
	}
	if strings.Contains(body, coverageLinePrefix) {
		t.Errorf("exemption-only bundle added a coverage body line:\n%s", body)
	}
}

// An explicit attestation that names no file measures nothing, so it also
// passes: requiring coverage of a diff with no reviewable lines is a refusal
// with no possible remedy.
func TestMergeCoverageGatePassesOnEmptyAttestationOfNothing(t *testing.T) {
	coverageBundle(t, generatedDiff, coverageReview("dist/app.min.js", `"coverage":[],`))
	if _, _, err := mergeCoverage(t, true); err != nil {
		t.Fatalf("empty attestation of an exemption-only bundle refused: %v", err)
	}
}
