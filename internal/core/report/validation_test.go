package reportcore_test

import (
	"strings"
	"testing"

	reportcore "github.com/stainless-code/verdict/internal/core/report"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	validationHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	validationSig  = "c111111111111111111111111111111111111111"
)

const validationArtifact = `{"schema_version":"1","head_sha":"` + validationHead + `","results":[{"sig":"` +
	validationSig + `","verdict":"CONFIRMED","reason":"The reset is reachable from the cited hunk."}]}`

const validationReceipt = `{"head":"aaaaaaa","posted_at":"2026-01-01T00:00:00Z",` +
	`"validation_hash":"9f2b","validation_counts":{"confirmed":1,"rejected":0,"downgraded":0,"unvalidated":0},` +
	`"validation_mode":"gate","rigor_floors":{"critical":"walked","important":"pointed","informational":"said"}}`

// TestReportValidationProjectsTheStoredReceipt pins the report half of the
// pass: the receipt a post run wrote is what report repeats, byte for byte,
// and the flag gates the key.
func TestReportValidationProjectsTheStoredReceipt(t *testing.T) {
	req := fixture(t, "["+openFinding+"]")
	writeFile(t, reportBundleDir+"/post.json", validationReceipt)

	plain := runReportChecked(t, req, &reportcore.Flags{})
	if strings.Contains(jq.PrettyOrdered(plain.Data.Wire()), `"validation"`) {
		t.Fatal("report without --validation exposed the pass")
	}

	wire := jq.PrettyOrdered(runReportChecked(t, req, &reportcore.Flags{Validation: true}).Data.Wire())
	for _, want := range []string{`"hash": "9f2b"`, `"mode": "gate"`, `"confirmed": 1`, `"critical": "walked"`} {
		if !strings.Contains(wire, want) {
			t.Fatalf("report --validation wire is missing %s:\n%s", want, wire)
		}
	}
}

// TestReportValidationTalliesTheArtifact pins the fallback: with no receipt
// yet, report tallies the artifact against the open findings, hashing the
// exact bytes the pass wrote.
func TestReportValidationTalliesTheArtifact(t *testing.T) {
	req := fixture(t, "["+openFinding+"]")
	writeFile(t, reportBundleDir+"/validation.json", validationArtifact)

	resp := runReportChecked(t, req, &reportcore.Flags{Validation: true})
	wire := jq.PrettyOrdered(resp.Data.Wire())
	for _, want := range []string{
		`"mode": "warn"`, `"confirmed": 1`, `"unvalidated": 0`,
		`"hash": "572d49006d938b2b667e04d9bf224dd5a27906a2605803312d72192ee05567dc"`,
	} {
		if !strings.Contains(wire, want) {
			t.Fatalf("report --validation wire is missing %s:\n%s", want, wire)
		}
	}
}

// TestReportValidationAbsentDegrades pins the clean degrade: no artifact and
// no receipt warns and adds no key.
func TestReportValidationAbsentDegrades(t *testing.T) {
	req := fixture(t, "["+openFinding+"]")
	resp := runReportChecked(t, req, &reportcore.Flags{Validation: true})
	if resp.Data.Validation != nil {
		t.Fatalf("report invented a pass: %+v", resp.Data.Validation)
	}
	if !strings.Contains(strings.Join(resp.Warnings, "\n"), "validation.json") {
		t.Fatalf("warnings = %v, want the absent artifact named", resp.Warnings)
	}
}

func TestReportOmitsRowsFromDifferentValidationPass(t *testing.T) {
	req := fixture(t, "["+openFinding+"]")
	writeFile(t, reportBundleDir+"/post.json", validationReceipt)
	writeFile(t, reportBundleDir+"/validation.json", validationArtifact)
	resp := runReportChecked(t, req, &reportcore.Flags{Validation: true})
	if strings.Contains(strings.Join(resp.Human, "\n"), "The reset is reachable") {
		t.Fatal("report paired current rows with recorded counts")
	}
	if !strings.Contains(strings.Join(resp.Warnings, "\n"), "differs from the recorded") {
		t.Fatalf("warnings = %v", resp.Warnings)
	}
}
