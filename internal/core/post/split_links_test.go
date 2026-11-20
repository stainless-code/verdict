package postcore

import (
	"context"
	"strconv"
	"strings"
	"testing"
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
	req := postTestReq(cmdr, Flags{Approve: "false", ReviewFile: ".verdict/state/7/review.json"}, &warnings, &human)
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

func TestRuneChunksDoesNotSplitEvidenceRefToken(t *testing.T) {
	t.Parallel()
	para := "prefix [a][ev-1] suffix"
	chunks := runeChunks(para, 11)
	if len(chunks) != 3 || chunks[0] != "prefix " || chunks[1] != "[a][ev-1] s" || chunks[2] != "uffix" {
		t.Fatalf("runeChunks split reference token across boundary: %q", chunks)
	}
}

func TestSplitPreFooterManyEvidenceDefsExceedingPartBudget(t *testing.T) {
	t.Parallel()
	const count = 700
	lines := make([]string, 0, count)
	defs := make([]string, 0, count)
	for i := 1; i <= count; i++ {
		id := strconv.Itoa(i)
		lines = append(lines, id+". ⚠️ **important** [src/pkg/file_"+id+".go:"+id+"][ev-"+id+"] (`walked`) — Finding claim number "+id+" with details.")
		defs = append(defs, "[ev-"+id+"]: https://github.com/acme/app/blob/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/src/pkg/file_"+id+".go#L"+id)
	}
	body := strings.Join([]string{
		"> ⚠️ Findings require attention.",
		"**Findings**\n\n" + strings.Join(lines, "\n"),
		strings.Join(defs, "\n"),
	}, "\n\n")
	if len(body) <= hostingBodyLimit {
		t.Fatalf("fixture len = %d, want > %d", len(body), hostingBodyLimit)
	}

	var warnings []string
	p := postSplitState(&warnings)
	head := p.splitPreFooter(body)
	if len(p.extraParts) == 0 {
		t.Fatal("expected split into multiple parts")
	}
	allParts := append([]string{head}, p.extraParts...)
	verifySplitPartsKeepAllEvidenceDefs(t, allParts, count)
}

func verifySplitPartsKeepAllEvidenceDefs(t *testing.T, parts []string, wantRefs int) {
	t.Helper()
	seenRefs := map[string]bool{}
	for idx, part := range parts {
		verifyOneSplitPartEvidenceDefs(t, idx, len(parts), part, seenRefs)
	}
	if len(seenRefs) != wantRefs {
		t.Fatalf("preserved %d distinct [ev-N] references across parts, want %d", len(seenRefs), wantRefs)
	}
}

func verifyOneSplitPartEvidenceDefs(t *testing.T, idx, total int, part string, seenRefs map[string]bool) {
	t.Helper()
	if idx > 0 {
		assertSplitPart(t, splitFollowHead(idx+1, total)+part)
	} else {
		assertSplitPart(t, part)
	}
	for _, m := range evRefRe.FindAllStringSubmatch(part, -1) {
		id := m[1]
		seenRefs[id] = true
		defPrefix := "[ev-" + id + "]: https://github.com/acme/app/blob/"
		if !strings.Contains(part, defPrefix) {
			t.Fatalf("part %d references [ev-%s] but lacks its definition", idx+1, id)
		}
	}
}
