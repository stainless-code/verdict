package mcp

import (
	"strings"
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

// TestReportToolRefusesRemovedGateFlags proves the report tool is the advisory
// read: gate is the only gating verb, so a client still sending a gating flag is
// refused as an unknown argument rather than handed an ungated report it would
// read as a pass.
func TestReportToolRefusesRemovedGateFlags(t *testing.T) {
	fixture(t)
	writeFile(t, ".verdict/state/5101/findings.json", tierStore)
	for _, flag := range []string{"check", "maxRisk", "completeness", "live"} {
		t.Run(flag, func(t *testing.T) {
			cfg := testConfig()
			frames := serve(t, &cfg, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"report",`+
				`"arguments":{"repo":"acme/app","pr":"5101","flags":{"`+flag+`":"critical"}}}}`)
			f := frames["1"]
			if f.Error == nil || !strings.Contains(f.Error.Message, `unknown argument "flags.`+flag+`"`) {
				t.Fatalf("report flags.%s was not refused as unknown: %+v / %s", flag, f.Error, f.Result)
			}
		})
	}
}
