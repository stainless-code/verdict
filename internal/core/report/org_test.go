package reportcore_test

import (
	"strings"
	"testing"

	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/jq"
)

// TestReportShowsGoverningOrgPack proves report names the central pack
// version that governed the run, in both the human line and the JSON data.
//
//nolint:revive // the seed, run, and assert sequence reads as one scenario
func TestReportShowsGoverningOrgPack(t *testing.T) {
	req := fixture(t, "[]")
	writeReportProvenance(t)
	writeFile(t, reportBundleDir+"/controls/config.json",
		`{"schema_version":"1","packs":{"org_pack":{"repo":"https://example.invalid/acme/packs.git","ref":"v1.2.3","path":"packs/org"}}}`)
	writeFile(t, reportBundleDir+"/controls/verdict.lock",
		`{"schema_version":"1","packs":[{"name":"acme/lenses","source":"git:https://example.invalid/acme/packs.git@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","version":"1.0.0","hash":"`+strings.Repeat("abcd", 16)+`","org_path":"packs/org","org_ref":"v1.2.3","sig":"`+strings.Repeat("efef", 16)+`"}]}`)

	resp := runReportChecked(t, req, &reportcore.Flags{})
	org := resp.Data.OrgPack
	if org == nil {
		t.Fatal("report data carries no org_pack")
	}
	if org.Repo != "https://example.invalid/acme/packs.git" || org.Ref != "v1.2.3" || org.Path != "packs/org" {
		t.Errorf("org_pack = %+v, want the pinned repo, ref, and path", org)
	}
	if org.Hash != "abcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcd" {
		t.Errorf("org_pack hash = %q, want the locked hash", org.Hash)
	}
	if org.SHA != strings.Repeat("a", 40) || org.Name != "acme/lenses" || org.Version != "1.0.0" {
		t.Errorf("org_pack locked = sha %q pack %s@%s, want the lock row", org.SHA, org.Name, org.Version)
	}
	wire := jq.PrettyOrdered(resp.Data.Wire())
	if !strings.Contains(wire, `"org_pack"`) || !strings.Contains(wire, "acme/lenses") {
		t.Fatalf("report JSON missing the org_pack block:\n%s", wire)
	}
	human := strings.Join(resp.Human, "\n")
	if !strings.Contains(human, "org pack: https://example.invalid/acme/packs.git@v1.2.3") {
		t.Fatalf("human report missing the org pack line:\n%s", human)
	}
}

// TestReportWithoutOrgPinKeepsLegacyShape proves a bundle with no pin
// carries no org_pack data and no extra human line.
func TestReportWithoutOrgPinKeepsLegacyShape(t *testing.T) {
	req := fixture(t, "[]")
	resp := runReportChecked(t, req, &reportcore.Flags{})
	if resp.Data.OrgPack != nil {
		t.Fatalf("org_pack = %+v, want absent", resp.Data.OrgPack)
	}
	if strings.Contains(strings.Join(resp.Human, "\n"), "org pack:") {
		t.Fatal("human report carries an org pack line without a pin")
	}
	if strings.Contains(jq.PrettyOrdered(resp.Data.Wire()), `"org_pack"`) {
		t.Fatal("report JSON carries org_pack without a pin")
	}
}
