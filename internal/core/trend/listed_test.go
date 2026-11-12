package trendcore

import (
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// trendOpenFinding is one readable store entry: an open critical finding.
const trendOpenFinding = `{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,` +
	`"anchored":true,"body":"Usable body.","suggestion":null,` +
	`"inBounds":true,"rigor":"walked","severity":"critical",` +
	`"sig":"c111111111111111111111111111111111111111","sig_version":"1",` +
	`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":"open",` +
	`"rationale":"","history":[]}`

// TestTrendCountsListedBundlesOnce proves a listed bare name reads its own
// directory: `42` has no PR.json, so the fallback an operator's positional gets
// would resolve it to `local-42`, counting that bundle twice and dropping the
// findings of `42`.
func TestTrendCountsListedBundlesOnce(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	state := readcommon.StateDir(root)
	writeListedStore(t, filepath.Join(state, "42"), "["+trendOpenFinding+"]")
	writeListedStore(t, filepath.Join(state, "local-42"), "["+trendOpenFinding+","+trendOpenFinding+"]")
	read := core.ReadRequest{Runtime: &core.Runtime{Progress: &progressLog{}}, RepoRoot: root}
	resp, ref := Run(t.Context(), &Request{ReadRequest: read})
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	got := map[string]int{}
	for _, r := range resp.Data.Rows {
		got[r.bundle] += len(r.findings)
	}
	want := map[string]int{"42": 1, "local-42": 2}
	if !maps.Equal(got, want) {
		t.Fatalf("rows = %v; want %v", got, want)
	}
	if resp.Data.Rollup.bundles != 2 || resp.Data.Rollup.findings != 3 {
		t.Errorf("rollup = %d bundles, %d findings; want 2 bundles, 3 findings",
			resp.Data.Rollup.bundles, resp.Data.Rollup.findings)
	}
}

// writeListedStore lands one findings store under a state-directory name.
func writeListedStore(t *testing.T, dir, findings string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "findings.json"), []byte(findings), 0o600); err != nil {
		t.Fatal(err)
	}
}
