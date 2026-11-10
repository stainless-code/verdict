package archtest

// Guard for the release signature gate. release.yml signs checksums.txt with
// cosign keyless and shipped without reading the signature back, so a bundle
// that was missing, mis-signed, or minted by another workflow surfaced only on
// an operator's machine. The job now verifies the published assets, after
// signing, against the exact certificate release/contract.json publishes — the
// same identity, issuer, and trigger both installers pin — so the job accepts
// what an installer accepts and nothing else.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// releaseVerifyStep is the step that reads the published signature back.
const releaseVerifyStep = "      - name: Verify the published checksums signature"

// releaseSigningStep is the step that signs, so verification can only follow.
const releaseSigningStep = "      - name: Create Release Pull Request or Publish to npm"

// contractCosign mirrors the certificate block the contract publishes.
type contractCosign struct {
	Identity       string `json:"identity"`
	Issuer         string `json:"issuer"`
	IdentityRegexp string `json:"identityRegexp"`
	Trigger        string `json:"trigger"`
}

// TestReleaseVerifiesPublishedSignature fails when the release job stops
// grading the signature it published, grades it before signing, or pins a
// certificate other than the contract's.
func TestReleaseVerifiesPublishedSignature(t *testing.T) {
	block := releaseJob(t)
	step := stepBlock(block, releaseVerifyStep)
	if step == nil {
		t.Fatal("release.yml has no step verifying the published signature: a mis-signed bundle ships ungraded")
	}
	checkVerifyStep(t, step)
	checkVerifyFollowsSigning(t, block)
}

// checkVerifyStep proves the step runs cosign on a bundle, only after a
// publish, with every certificate claim the contract publishes.
func checkVerifyStep(t *testing.T, step []string) {
	t.Helper()
	joined := strings.Join(step, "\n")
	for _, want := range []string{"cosign verify-blob", "--bundle"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the verify step no longer runs %q", want)
		}
	}
	if !strings.Contains(joined, "if: steps.changesets.outputs.published == 'true'") {
		t.Error("the verify step lost its published gate: a Version-PR run that signs nothing would fail it")
	}
	for _, pin := range certificatePins(t) {
		if !strings.Contains(joined, pin.want) {
			t.Errorf("the verify step does not pin the certificate %s (%q): an installer would reject what this job accepted",
				pin.name, pin.want)
		}
	}
}

// checkVerifyFollowsSigning proves the step grades a bundle the same job made.
func checkVerifyFollowsSigning(t *testing.T, block []string) {
	t.Helper()
	signAt := slices.Index(block, releaseSigningStep)
	if signAt < 0 {
		t.Fatalf("release.yml no longer has %q: the verify step would grade nothing",
			strings.TrimSpace(releaseSigningStep))
	}
	if slices.Index(block, releaseVerifyStep) < signAt {
		t.Error("the verify step runs before the signing step: it would grade a bundle that does not exist yet")
	}
}

// certificatePins lists the claims the verify step must pass to verify-blob.
func certificatePins(t *testing.T) []struct{ name, want string } {
	t.Helper()
	cosign := readContractCosign(t)
	return []struct{ name, want string }{
		{"identity", `--certificate-identity "` + cosign.Identity + `"`},
		{"issuer", `--certificate-oidc-issuer "` + cosign.Issuer + `"`},
		{"trigger", "--certificate-github-workflow-trigger " + cosign.Trigger},
		{"workflow SHA", "--certificate-github-workflow-sha"},
	}
}

// TestContractCosignIdentityAgrees checks the contract's two spellings of the
// identity, so the job and the installers cannot pin different certificates.
func TestContractCosignIdentityAgrees(t *testing.T) {
	cosign := readContractCosign(t)
	if cosign.Identity == "" || cosign.Issuer == "" || cosign.Trigger == "" {
		t.Fatalf("release/contract.json has an empty cosign pin: %+v", cosign)
	}
	re, err := regexp.Compile(cosign.IdentityRegexp)
	if err != nil {
		t.Fatalf("contract cosign.identityRegexp %q: %v", cosign.IdentityRegexp, err)
	}
	if !re.MatchString(cosign.Identity) {
		t.Errorf("contract cosign.identity %q is not matched by its own identityRegexp %q",
			cosign.Identity, cosign.IdentityRegexp)
	}
}

// releaseJob returns the release job's lines.
func releaseJob(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(moduleRoot(t), ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release.yml: %v", err)
	}
	block := workflowJobs(t, "release.yml", string(raw))["release"]
	if len(block) == 0 {
		t.Fatal("release.yml has no release job: the signature gate would pass on nothing")
	}
	return block
}

// stepBlock returns one named step's lines, header included, or nil.
func stepBlock(block []string, header string) []string {
	start := slices.Index(block, header)
	if start < 0 {
		return nil
	}
	end := start + 1
	for end < len(block) && !strings.HasPrefix(block[end], "      - name: ") {
		end++
	}
	return block[start:end]
}

// readContractCosign returns the certificate pins the contract publishes.
func readContractCosign(t *testing.T) contractCosign {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(moduleRoot(t), "release", "contract.json"))
	if err != nil {
		t.Fatalf("read release/contract.json: %v", err)
	}
	var contract struct {
		Cosign contractCosign `json:"cosign"`
	}
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatalf("parse release/contract.json: %v", err)
	}
	return contract.Cosign
}
