package release_test

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	contract "github.com/stainless-code/verdict/release"
)

// recordedBundle is the sigstore bundle GoReleaser published for the v0.4.0
// release (run 41803129279, commit 72e7d30), reformatted by `bun fmt` with the
// content untouched. It is a real certificate, so it pins the identity the
// release pipeline mints without trusting the contract to describe itself.
const recordedBundle = "testdata/v0.4.0-checksums.txt.sigstore.json"

// Fulcio OIDC extensions: 1.1 is the token issuer, 1.3 is the workflow SHA,
// 1.6 is the git ref.
const (
	oidcIssuerExt      = "1.3.6.1.4.1.57264.1.1"
	oidcWorkflowSHAExt = "1.3.6.1.4.1.57264.1.3"
	oidcRefExt         = "1.3.6.1.4.1.57264.1.6"
)

// cosignPolicy is the contract's signing section.
type cosignPolicy struct {
	Identity       string `json:"identity"`
	IdentityRegexp string `json:"identityRegexp"`
	Issuer         string `json:"issuer"`
}

// contractCosign reads the cosign section of the embedded contract.
func contractCosign(t *testing.T) cosignPolicy {
	t.Helper()
	var doc struct {
		Cosign cosignPolicy `json:"cosign"`
	}
	if err := json.Unmarshal(contract.ContractJSON(), &doc); err != nil {
		t.Fatalf("release contract: %v", err)
	}
	if doc.Cosign.Identity == "" || doc.Cosign.IdentityRegexp == "" || doc.Cosign.Issuer == "" {
		t.Fatalf("contract cosign section incomplete: %+v", doc.Cosign)
	}
	return doc.Cosign
}

// recordedCert decodes the leaf certificate out of the recorded bundle.
func recordedCert(t *testing.T) *x509.Certificate {
	t.Helper()
	raw, err := os.ReadFile(recordedBundle)
	if err != nil {
		t.Fatal(err)
	}
	var bundle struct {
		VerificationMaterial struct {
			Certificate struct {
				RawBytes string `json:"rawBytes"`
			} `json:"certificate"`
		} `json:"verificationMaterial"`
	}
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatalf("%s: %v", recordedBundle, err)
	}
	der, err := base64.StdEncoding.DecodeString(bundle.VerificationMaterial.Certificate.RawBytes)
	if err != nil {
		t.Fatalf("%s: certificate rawBytes: %v", recordedBundle, err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("%s: %v", recordedBundle, err)
	}
	return cert
}

// extValue returns one Fulcio extension's string value.
func extValue(t *testing.T, cert *x509.Certificate, oid string) string {
	t.Helper()
	for _, ext := range cert.Extensions {
		if ext.Id.String() == oid {
			return string(ext.Value)
		}
	}
	t.Fatalf("%s carries no %s extension", recordedBundle, oid)
	return ""
}

// TestContractIdentityMatchesRecordedCertificate proves the identity the
// installers and `verdict upgrade` verify is the SAN a release run really
// mints. While the contract carried `refs/tags/v…` the recorded certificate
// did not match it, so every signed install, self-replace, and setup-verdict
// run failed closed; this test fails for the same reason.
func TestContractIdentityMatchesRecordedCertificate(t *testing.T) {
	doc := contractCosign(t)
	cert := recordedCert(t)

	if len(cert.URIs) != 1 {
		t.Fatalf("bundle SANs = %v; want exactly one", cert.URIs)
	}
	san := cert.URIs[0].String()
	if san != doc.Identity {
		t.Fatalf("bundle SAN = %q; contract identity = %q", san, doc.Identity)
	}

	identity := regexp.MustCompile(doc.IdentityRegexp)
	if !identity.MatchString(san) {
		t.Errorf("contract regexp %q rejects the recorded SAN %q", doc.IdentityRegexp, san)
	}
	// Anchored: a regexp that also accepted another ref would let an
	// unrelated workflow run sign this file.
	other := strings.Replace(san, "@refs/heads/main", "@refs/heads/attacker", 1)
	if other == san || identity.MatchString(other) {
		t.Errorf("contract regexp %q matches %q; want it anchored on the recorded SAN", doc.IdentityRegexp, other)
	}

	if issuer := extValue(t, cert, oidcIssuerExt); issuer != doc.Issuer {
		t.Errorf("bundle issuer = %q; contract issuer = %q", issuer, doc.Issuer)
	}
	// The SAN names the ref that fired the run. Re-record the fixture and
	// revisit the contract if this ever becomes a tag ref.
	if ref := extValue(t, cert, oidcRefExt); !strings.HasSuffix(san, "@"+ref) {
		t.Errorf("SAN %q does not end in the certificate's ref %q", san, ref)
	}
}

// The installers pin cosign with --certificate-github-workflow-sha, which takes
// a full SHA: prove Fulcio mints one and that commit.txt carries it.
func TestRecordedCertificateCarriesFullCommit(t *testing.T) {
	sha := extValue(t, recordedCert(t), oidcWorkflowSHAExt)
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(sha) {
		t.Fatalf("bundle workflow sha = %q; want a full 40-character lowercase SHA", sha)
	}
	if sha != releaseCommitSHA {
		t.Errorf("fixture commit.txt = %q; bundle workflow sha = %q", releaseCommitSHA, sha)
	}
}
