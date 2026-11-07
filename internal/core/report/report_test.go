package reportcore_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

var fixedNow = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

func fixedRuntime() *core.Runtime {
	return &core.Runtime{Now: func() time.Time { return fixedNow }}
}

func fixture(t *testing.T, findings string) core.ReadRequest {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, ".verdict/state/5101/PR.json",
		`{"state":"OPEN","headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
			`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	writeFile(t, ".verdict/state/5101/findings.json", findings)
	return core.ReadRequest{
		Runtime: fixedRuntime(),
		Repo:    "acme/app",
		Pr:      "5101",
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const openFinding = `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
	`"anchored":true,"body":"Usable body.","suggestion":null,` +
	`"inBounds":true,"rigor":"walked","severity":"critical",` +
	`"sig":"c111111111111111111111111111111111111111","sig_version":"1",` +
	`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open",` +
	`"rationale":"","history":[]}`

// TestRunReportData proves the typed report response carries the wire data
// in shell key order, with items keeping their store key order and locator
// and rank appended last.
//
//nolint:revive // one assertion block per response dimension
func TestRunReportData(t *testing.T) {
	req := fixture(t, "["+openFinding+"]")
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{ReadRequest: req})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.At != "2026-11-02T12:00:00Z" {
		t.Errorf("At = %q", resp.At)
	}
	if len(resp.Data.Items) != 1 {
		t.Fatalf("items = %d", len(resp.Data.Items))
	}
	item := resp.Data.Items[0].Wire()
	wantTail := []string{"locator", "rank"}
	if got := item.Keys[len(item.Keys)-2:]; strings.Join(got, ",") != strings.Join(wantTail, ",") {
		t.Errorf("item key tail = %v, want %v", got, wantTail)
	}
	if item.Keys[0] != "kind" {
		t.Errorf("item first key = %q, want kind (store order preserved)", item.Keys[0])
	}
	data := resp.Data.Wire()
	wantKeys := []string{
		"generated_at", "dir", "head", "verdict", "counts", "panel",
		"patch", "output", "filters", "items",
	}
	if strings.Join(data.Keys, ",") != strings.Join(wantKeys, ",") {
		t.Errorf("data keys = %v, want %v", data.Keys, wantKeys)
	}
	if len(resp.Human) == 0 || !strings.HasPrefix(resp.Human[0], "report: .verdict/state/5101 (1 shown)") {
		t.Errorf("human = %v", resp.Human)
	}
	if resp.Next != "address open threads" {
		t.Errorf("next = %q", resp.Next)
	}
	if resp.Output != nil || resp.Sarif != nil {
		t.Error("unrequested artifacts returned")
	}
}

func sarifFindingJSON(kind, severity, sig string) string {
	return `{"kind":"` + kind + `","path":"notes.txt","side":"RIGHT","line":2,` +
		`"anchored":true,"body":"` + kind + ` body.","suggestion":null,` +
		`"inBounds":true,"rigor":"walked","severity":"` + severity + `",` +
		`"sig":"` + sig + `","sig_version":"1","firstSeen":"aaaaaaa",` +
		`"lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}`
}

// TestRunReportArtifacts proves --output/--sarif return bytes instead of
// writing them, and that the SARIF export carries the 2.1.0 document shape,
// per-kind rules, result level mapping, locations, and sig fingerprints.
//
//nolint:cyclop,gocognit,revive // one assertion block per SARIF dimension
func TestRunReportArtifacts(t *testing.T) {
	const (
		sigBug   = "1111111111111111111111111111111111111111"
		sigSec   = "2222222222222222222222222222222222222222"
		sigStyle = "3333333333333333333333333333333333333333"
	)
	req := fixture(t, "["+
		sarifFindingJSON("bug", "critical", sigBug)+","+
		sarifFindingJSON("security", "important", sigSec)+","+
		sarifFindingJSON("style", "informational", sigStyle)+"]")
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Output: "out.md", Sarif: "out.sarif"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.Output == nil || resp.Output.Path != "out.md" {
		t.Fatalf("output artifact = %+v", resp.Output)
	}
	if !strings.Contains(string(resp.Output.Content), "# Review report — acme/app#5101") {
		t.Error("output artifact missing report heading")
	}
	if resp.Sarif == nil || resp.Sarif.Path != "out.sarif" {
		t.Fatalf("sarif artifact = %+v", resp.Sarif)
	}
	var doc struct {
		Version string `json:"version"`
		Runs    []struct {
			Tool struct {
				Driver struct {
					Rules []struct {
						ID string `json:"id"`
					} `json:"rules"`
				} `json:"driver"`
			} `json:"tool"`
			Results []struct { //nolint:govet // decode-only shape mirrors the SARIF document
				RuleID  string `json:"ruleId"`
				Level   string `json:"level"`
				Message struct {
					Text string `json:"text"`
				} `json:"message"`
				Locations []struct {
					PhysicalLocation struct {
						ArtifactLocation struct {
							URI string `json:"uri"`
						} `json:"artifactLocation"`
						Region struct {
							StartLine int `json:"startLine"`
						} `json:"region"`
					} `json:"physicalLocation"`
				} `json:"locations"`
				PartialFingerprints map[string]string `json:"partialFingerprints"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(resp.Sarif.Content, &doc); err != nil {
		t.Fatalf("sarif is not JSON: %v\n%s", err, resp.Sarif.Content)
	}
	if doc.Version != "2.1.0" {
		t.Errorf("sarif version = %q, want 2.1.0", doc.Version)
	}
	if len(doc.Runs) != 1 {
		t.Fatalf("sarif runs = %d, want 1", len(doc.Runs))
	}
	var ruleIDs []string
	for _, rule := range doc.Runs[0].Tool.Driver.Rules {
		ruleIDs = append(ruleIDs, rule.ID)
	}
	if got, want := strings.Join(ruleIDs, ","), "verdict/bug,verdict/security,verdict/style"; got != want {
		t.Errorf("sarif rules = %s, want %s", got, want)
	}
	if len(doc.Runs[0].Results) != 3 {
		t.Fatalf("sarif results = %d, want 3", len(doc.Runs[0].Results))
	}
	wantLevels := map[string]string{
		"verdict/bug":      "error",
		"verdict/security": "warning",
		"verdict/style":    "note",
	}
	wantSigs := map[string]string{
		"verdict/bug":      sigBug,
		"verdict/security": sigSec,
		"verdict/style":    sigStyle,
	}
	for _, result := range doc.Runs[0].Results {
		if got := result.Level; got != wantLevels[result.RuleID] {
			t.Errorf("%s level = %q, want %q", result.RuleID, got, wantLevels[result.RuleID])
		}
		if !strings.Contains(result.Message.Text, strings.TrimPrefix(result.RuleID, "verdict/")+" body.") {
			t.Errorf("%s message = %q", result.RuleID, result.Message.Text)
		}
		if len(result.Locations) != 1 {
			t.Fatalf("%s locations = %d, want 1", result.RuleID, len(result.Locations))
		}
		loc := result.Locations[0].PhysicalLocation
		if loc.ArtifactLocation.URI != "notes.txt" || loc.Region.StartLine != 2 {
			t.Errorf("%s location = %+v, want notes.txt:2", result.RuleID, loc)
		}
		if got := result.PartialFingerprints["verdict/sig"]; got != wantSigs[result.RuleID] {
			t.Errorf("%s fingerprint = %q, want %q", result.RuleID, got, wantSigs[result.RuleID])
		}
	}
	if _, err := os.Stat("out.md"); !os.IsNotExist(err) {
		t.Error("core wrote the output artifact; adapters own writes")
	}
	if _, err := os.Stat("out.sarif"); !os.IsNotExist(err) {
		t.Error("core wrote the sarif artifact; adapters own writes")
	}
}

// TestRunReportCorruptStore proves a corrupt store refuses typed and keeps
// the warnings accumulated before the refusal.
func TestRunReportCorruptStore(t *testing.T) {
	req := fixture(t, "[42]")
	_, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Warnings:    []string{"prefs warning"},
	})
	if ref == nil {
		t.Fatal("expected refusal")
	}
	if ref.Code != "corrupt-store" {
		t.Errorf("code = %q", ref.Code)
	}
	if len(ref.Warnings) != 1 || ref.Warnings[0] != "prefs warning" {
		t.Errorf("warnings = %v", ref.Warnings)
	}
}

// TestRunNextEmptyStore proves the empty-store next path is typed, not a
// report replay artifact.
func TestRunNextEmptyStore(t *testing.T) {
	req := fixture(t, "[]")
	resp, ref := reportcore.RunNext(context.Background(), &core.NextRequest{ReadRequest: req})
	if ref != nil {
		t.Fatalf("RunNext refusal: %v", ref)
	}
	if resp.StoreUnreadable {
		t.Fatal("empty store classified unreadable")
	}
	if resp.Item != nil {
		t.Errorf("item = %+v, want nil", resp.Item)
	}
	if resp.Human != "next: none open — nothing to act on" {
		t.Errorf("human = %q", resp.Human)
	}
}

// TestRunNextCorruptStore proves the corrupt store is classified and
// carries the underlying report refusal with the adapter warnings once.
func TestRunNextCorruptStore(t *testing.T) {
	req := fixture(t, "[42]")
	resp, ref := reportcore.RunNext(context.Background(), &core.NextRequest{
		ReadRequest: req,
		Warnings:    []string{"prefs warning"},
	})
	if ref != nil {
		t.Fatalf("RunNext refusal: %v", ref)
	}
	if !resp.StoreUnreadable || resp.ReportRefusal == nil {
		t.Fatalf("store classification = %+v", resp)
	}
	if resp.ReportRefusal.Code != "corrupt-store" {
		t.Errorf("code = %q", resp.ReportRefusal.Code)
	}
	if len(resp.ReportRefusal.Warnings) != 1 {
		t.Errorf("report warnings = %v", resp.ReportRefusal.Warnings)
	}
}

// TestRunNextBadStatus proves an invalid status never reaches the store and
// has no report refusal to replay.
func TestRunNextBadStatus(t *testing.T) {
	req := fixture(t, "["+openFinding+"]")
	resp, ref := reportcore.RunNext(context.Background(), &core.NextRequest{
		ReadRequest: req,
		Status:      "bogus",
	})
	if ref != nil {
		t.Fatalf("RunNext refusal: %v", ref)
	}
	if !resp.StoreUnreadable || resp.ReportRefusal != nil {
		t.Fatalf("classification = %+v", resp)
	}
}

// TestRunShow proves the show service resolves a short sig and returns the
// full record with store key order.
func TestRunShow(t *testing.T) {
	req := fixture(t, "["+openFinding+"]")
	resp, ref := reportcore.RunShow(context.Background(), &core.ShowRequest{
		ReadRequest: req,
		Sig:         "c1111111",
	})
	if ref != nil {
		t.Fatalf("RunShow refusal: %v", ref)
	}
	if resp.Item == nil || resp.Item.Raw == nil {
		t.Fatal("no item returned")
	}
	if got := resp.Item.Raw.MarshalCompact(); got != openFinding {
		t.Errorf("record = %s", got)
	}
}

// TestRunShowRefusals proves the missing and unknown sig refusals stay typed.
func TestRunShowRefusals(t *testing.T) {
	req := fixture(t, "[]")
	for name, want := range map[string]string{
		"":         "missing-sig",
		"deadbeef": "unknown-sig",
	} {
		_, ref := reportcore.RunShow(context.Background(), &core.ShowRequest{
			ReadRequest: req,
			Sig:         name,
		})
		if ref == nil || ref.Code != want {
			t.Errorf("sig %q: refusal = %v, want %s", name, ref, want)
		}
	}
}
