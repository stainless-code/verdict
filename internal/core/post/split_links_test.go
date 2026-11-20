package postcore

import (
	"context"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

func TestSplitPreFooterPerPartEvidenceLinkDefs(t *testing.T) {
	t.Parallel()
	var warnings []string
	p := postSplitState(&warnings)
	partA := "1. 🚨 **critical** [src/a.go:10][ev-1] (`ran`) — " + strings.Repeat("a", 40000)
	partB := "2. ⚠️ **important** [src/b.go:20][ev-2] (`walked`) — " + strings.Repeat("b", 40000)
	def1 := "[ev-1]: https://github.com/acme/app/blob/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/src/a.go#L10"
	def2 := "[ev-2]: https://github.com/acme/app/blob/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/src/b.go#L20"
	body := strings.Join([]string{"> ⚠️ Findings require attention.", partA, partB, def1 + "\n" + def2}, "\n\n")

	head := p.splitPreFooter(body)
	if len(p.extraParts) != 1 {
		t.Fatalf("extraParts = %d, want 1", len(p.extraParts))
	}
	if !strings.Contains(head, def1) || strings.Contains(head, def2) {
		t.Errorf("part 1 link defs mismatch: has def1=%v has def2=%v", strings.Contains(head, def1), strings.Contains(head, def2))
	}
	part2 := p.extraParts[0]
	if !strings.Contains(part2, def2) || strings.Contains(part2, def1) {
		t.Errorf("part 2 link defs mismatch: has def2=%v has def1=%v", strings.Contains(part2, def2), strings.Contains(part2, def1))
	}
	assertSplitPart(t, head)
	assertSplitPart(t, splitFollowHead(2, 2)+part2)
}

func TestSplitRunsAfterFinalBannersStaysWithinLimit(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	bigClaim := strings.Repeat("x", 64800)
	review := `{"schema_version":"1","summary":"Boundary sized body.","findings":[` +
		`{"path":"notes.txt","line":2,"side":"RIGHT","severity":"important","rigor":"walked",` +
		`"quote":"new line","claim":"` + bigClaim + `"}]}`
	postBundle(t, review)
	live := driftLive
	cmdr := postBundleCommander(t, &live)
	var warnings, human []string
	req := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
	resp, ref := Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	if resp.Data.Body.Marker != "verdict" {
		t.Errorf("body.marker = %q, want verdict", resp.Data.Body.Marker)
	}
	payloads := postedPayloads(t, cmdr)
	if len(payloads) < 2 {
		t.Fatalf("posted %d payloads, want >= 2 when drift banner pushes past 65536", len(payloads))
	}
	for i, pl := range payloads {
		b := payloadBody(t, pl)
		if len(b) > hostingBodyLimit {
			t.Errorf("payload %d exceeds 65536 limit: %d bytes", i+1, len(b))
		}
	}
}
