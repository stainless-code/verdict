package mcp

import (
	"encoding/json"
	"testing"
)

// tierStore is one open informational inline finding: below every published
// threshold, so a gate that reads an unknown tier as the untagged rank counts
// it and fails.
const tierStore = `[{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
	`"anchored":true,"body":"Usable body here with enough substance.","suggestion":null,` +
	`"inBounds":true,"rigor":"walked","severity":"informational",` +
	`"sig":"1111111111111111111111111111111111111111","sig_version":"1",` +
	`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open","rationale":"","history":[]}]`

// reportCall drives one report tool call and returns the decoded result.
func reportCall(t *testing.T, flags string) callToolResult {
	t.Helper()
	cfg := testConfig()
	frames := serve(t, &cfg, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"report",`+
		`"arguments":{"repo":"acme/app","pr":"5101","flags":`+flags+`}}}`)
	f := frames["1"]
	if f.Error != nil {
		t.Fatalf("protocol error: %+v", *f.Error)
	}
	var result callToolResult
	if err := json.Unmarshal(f.Result, &result); err != nil {
		t.Fatalf("decode result: %v (%s)", err, f.Result)
	}
	return result
}

// TestReportToolRefusesUnpublishedTier proves the MCP report tool shares the
// core tier rule on both its advisory and its gated shape: the schema enum is
// metadata only, so a client that ignores it must be refused rather than
// ranked against every open finding.
func TestReportToolRefusesUnpublishedTier(t *testing.T) {
	fixture(t)
	writeFile(t, ".verdict/state/5101/findings.json", tierStore)
	for _, flags := range []string{`{"check":"bogus"}`, `{"check":"bogus","completeness":true}`} {
		t.Run(flags, func(t *testing.T) {
			result := reportCall(t, flags)
			if !result.IsError {
				t.Fatalf("report %s is not an error result: %s", flags, result.Content[0].Text)
			}
			var refusal refusalContent
			if err := json.Unmarshal(result.StructuredContent, &refusal); err != nil {
				t.Fatalf("decode structuredContent: %v", err)
			}
			if refusal.Code != "bad-flags" {
				t.Errorf("refusal code = %q, want bad-flags", refusal.Code)
			}
			want := "report: unknown gate tier 'bogus' (want critical|important|informational)"
			if refusal.Message != want {
				t.Errorf("refusal message = %q, want %q", refusal.Message, want)
			}
		})
	}
}

// TestReportToolAcceptsPublishedTiers proves the shared rule keeps every
// published tier working through the MCP surface, live probe skipped so the
// leg stays offline.
func TestReportToolAcceptsPublishedTiers(t *testing.T) {
	fixture(t)
	writeFile(t, ".verdict/state/5101/findings.json", tierStore)
	for _, tier := range []string{"critical", "important", "informational"} {
		t.Run(tier, func(t *testing.T) {
			result := reportCall(t, `{"check":"`+tier+`","live":"skip"}`)
			if result.IsError {
				t.Fatalf("report --check %s refused: %s", tier, result.Content[0].Text)
			}
		})
	}
}
