package reportcore_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
)

// categoryFinding renders one whole-store finding carrying raw category JSON.
func categoryFinding(sig, severity, cat string) string {
	return `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
		`"anchored":true,"body":"Usable body here with enough substance.","suggestion":null,` +
		`"inBounds":true,"rigor":"walked","severity":` + severity + `,"category":` + cat + `,` +
		`"sig":"` + sig + `","sig_version":"1",` +
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open",` +
		`"rationale":"","history":[]}`
}

const (
	catSigSecurity = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	catSigStyle    = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	catSigUntagged = "cccccccccccccccccccccccccccccccccccccccc"
)

// wantCategoryCounts is the whole-store tally for the three-finding fixture:
// one security, one style, one untagged, every other key zero.
func wantCategoryCounts() map[string]int {
	return map[string]int{
		"logic": 0, "edge-case": 0, "security": 1, "performance": 0,
		"accidental-commit": 0, "style": 1, "docs": 0, "other": 0, "untagged": 1,
	}
}

// checkCategoryCounts asserts the whole-store by_category tally.
func checkCategoryCounts(t *testing.T, resp *core.ReportResponse) {
	t.Helper()
	if !reflect.DeepEqual(resp.Data.Counts.ByCategory, wantCategoryCounts()) {
		t.Errorf("by_category = %v, want %v", resp.Data.Counts.ByCategory, wantCategoryCounts())
	}
}

func categoryStore() string {
	return "[" + strings.Join([]string{
		categoryFinding(catSigSecurity, `"critical"`, `"security"`),
		categoryFinding(catSigStyle, `"informational"`, `"style"`),
		categoryFinding(catSigUntagged, `"important"`, `null`),
	}, ",") + "]"
}

// TestReportCategoryFilter proves --category narrows the ranked display to
// the gated class while the whole-store counts keep every bucket.
func TestReportCategoryFilter(t *testing.T) {
	req := fixture(t, categoryStore())
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Category: "security"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	checkFilteredItems(t, resp)

	if resp.Data.Filters.Category == nil || *resp.Data.Filters.Category != "security" {
		t.Errorf("filters.category = %+v, want security", resp.Data.Filters)
	}
	checkCategoryCounts(t, resp)
}

// TestReportByCategoryKeys proves the unfiltered human and JSON surfaces
// carry every published key plus untagged with zero defaults.
func TestReportByCategoryKeys(t *testing.T) {
	req := fixture(t, categoryStore())
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{ReadRequest: req})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.Data.Filters.Category != nil {
		t.Errorf("filters.category = %v, want null without a filter", *resp.Data.Filters.Category)
	}
	wantLine := "by category: security 1, style 1, untagged 1"
	found := false
	for _, line := range resp.Human {
		if line == wantLine {
			found = true
		}
	}
	if !found {
		t.Errorf("human = %v, want the group line %q", resp.Human, wantLine)
	}
	checkCategoryCounts(t, resp)
}

// TestReportCheckCategoryGate proves the AND gate: only findings in the
// category at-or-above the tier fail it, and the check lines name the class.
func TestReportCheckCategoryGate(t *testing.T) {
	for _, tc := range []struct {
		name     string
		category string
		failed   bool
	}{
		{"security-critical-fails", "security", true},
		{"style-below-tier-passes", "style", false},
		{"untagged-below-tier-passes", "logic", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runCategoryGate(t, tc.category, tc.failed)
		})
	}
}

// runCategoryGate runs one --check critical gate inside a category and proves
// the verdict plus the named check line.
func runCategoryGate(t *testing.T, category string, failed bool) {
	t.Helper()
	req := fixture(t, categoryStore())
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Check: "critical", Category: category, Live: "skip"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.GateFailed != failed {
		t.Errorf("GateFailed = %v, want %v", resp.GateFailed, failed)
	}
	if !checkLineNames(resp.CheckLines, category) {
		t.Errorf("check lines = %v, want the category named", resp.CheckLines)
	}
}

// checkLineNames reports whether any check line names the gated category.
func checkLineNames(lines []string, category string) bool {
	for _, line := range lines {
		if strings.Contains(line, " in "+category+" ") || strings.Contains(line, " in "+category+" —") {
			return true
		}
	}
	return false
}

// TestReportBadCategoryRefusal proves the service refuses an unknown
// category filter in core, so direct Go API, MCP, and service callers get
// the typed bad-category refusal instead of a silently passing gate.
func TestReportBadCategoryRefusal(t *testing.T) {
	req := fixture(t, categoryStore())
	_, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Category: "securty", Check: "important", Live: "skip"},
	})
	if ref == nil {
		t.Fatal("Run with Category securty refused nothing, want bad-category")
	}
	if ref.Code != "bad-category" {
		t.Errorf("refusal code = %q, want bad-category", ref.Code)
	}
}

// eligibilityFinding renders one gate-eligibility probe entry: an open
// important security finding with raw anchored/rigor JSON.
func eligibilityFinding(sig, anchored, rigor string) string {
	return `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
		`"anchored":` + anchored + `,"body":"Usable body here with enough substance.","suggestion":null,` +
		`"inBounds":true,"rigor":` + rigor + `,"severity":"important","category":"security",` +
		`"sig":"` + sig + `","sig_version":"1",` +
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open",` +
		`"rationale":"","history":[]}`
}

// runEligibilityGate runs one --check important --category security gate over
// a single-finding store and returns whether the gate failed.
func runEligibilityGate(t *testing.T, store string) bool {
	t.Helper()
	req := fixture(t, store)
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Check: "important", Category: "security", Live: "skip"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	return resp.GateFailed
}

// TestReportCategoryGateEligibility proves the category gate still requires
// an anchor and a recorded evidence rung: unanchored or missing/invalid
// rigor never counts, while every published rung (said included) gates an
// anchored finding.
func TestReportCategoryGateEligibility(t *testing.T) {
	for _, tc := range []struct {
		name     string
		anchored string
		rigor    string
		failed   bool
	}{
		{"anchored-said-fails", "true", `"said"`, true},
		{"anchored-walked-fails", "true", `"walked"`, true},
		{"anchored-ran-fails", "true", `"ran"`, true},
		{"unanchored-said-passes", "false", `"said"`, false},
		{"anchored-null-rigor-passes", "true", `null`, false},
		{"anchored-unknown-rigor-passes", "true", `"maybe"`, false},
		{"anchored-number-rigor-passes", "true", `3`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := "[" + eligibilityFinding("dddddddddddddddddddddddddddddddddddddddd", tc.anchored, tc.rigor) + "]"
			if got := runEligibilityGate(t, store); got != tc.failed {
				t.Errorf("GateFailed = %v, want %v (anchored=%s rigor=%s)",
					got, tc.failed, tc.anchored, tc.rigor)
			}
		})
	}
}

// TestReportBaselineGateIgnoresEligibility proves the gate without a
// category keeps its severity-only semantics: an unanchored said finding
// still fails it, so the anchor/rigor intersection only narrows the
// category-gated path.
func TestReportBaselineGateIgnoresEligibility(t *testing.T) {
	store := "[" + eligibilityFinding("dddddddddddddddddddddddddddddddddddddddd", "false", `"said"`) + "]"
	req := fixture(t, store)
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Check: "important", Live: "skip"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if !resp.GateFailed {
		t.Error("baseline GateFailed = false, want true: the unanchored said finding still counts without a category")
	}
}

// TestReportSarifCategory proves the SARIF export carries the category in
// properties.category without changing the rule id or level.
func TestReportSarifCategory(t *testing.T) {
	req := fixture(t, categoryStore())
	resp, ref := reportcore.Run(context.Background(), &core.ReportRequest{
		ReadRequest: req,
		Flags:       core.ReportFlags{Sarif: "report.sarif"},
	})
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.Sarif == nil {
		t.Fatal("no SARIF artifact returned")
	}
	doc := string(resp.Sarif.Content)
	if !strings.Contains(doc, `"category": "security"`) {
		t.Errorf("SARIF has no security category property:\n%s", doc)
	}
	if !strings.Contains(doc, `"category": null`) {
		t.Errorf("SARIF has no null category property for the untagged finding:\n%s", doc)
	}
	if !strings.Contains(doc, `"ruleId": "verdict/inline"`) {
		t.Errorf("SARIF changed the rule id:\n%s", doc)
	}
}

// checkFilteredItems proves the ranked display holds only the security
// finding and drops the group line under a filter.
func checkFilteredItems(t *testing.T, resp *core.ReportResponse) {
	t.Helper()
	if len(resp.Data.Items) != 1 {
		t.Fatalf("items = %d, want only the security finding", len(resp.Data.Items))
	}
	if got := resp.Data.Items[0].Sig; got != catSigSecurity {
		t.Errorf("item sig = %s, want the security finding", got)
	}
	for _, line := range resp.Human {
		if strings.HasPrefix(line, "by category:") {
			t.Errorf("filtered human prints the group line: %q", line)
		}
	}
}
