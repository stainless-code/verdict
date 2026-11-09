package recordcore_test

import (
	"strings"
	"testing"
)

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
