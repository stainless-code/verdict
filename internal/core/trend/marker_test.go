package trendcore

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// progressLog records the warnings trend streams to the runtime observer.
type progressLog struct {
	warns []string
}

// Human drops the human lines.
func (*progressLog) Human(string) {}

// Warn records one warning.
func (p *progressLog) Warn(msg string) { p.warns = append(p.warns, msg) }

// Diag drops the diagnostic lines.
func (*progressLog) Diag(string) {}

// TestTrendSkipsBacktestBundles proves a bundle carrying the replay marker is
// not part of the operator's history, and that the skip is named rather than
// silent.
func TestTrendSkipsBacktestBundles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	state := readcommon.StateDir(root)
	writeTrendBundle(t, filepath.Join(state, "local-bt-ra-1"), true)
	writeTrendBundle(t, filepath.Join(state, "local-real"), false)
	prog := &progressLog{}
	read := core.ReadRequest{Runtime: &core.Runtime{Progress: prog}, RepoRoot: root}
	resp, ref := Run(t.Context(), &Request{ReadRequest: read})
	if ref != nil {
		t.Fatalf("refusal %s: %s", ref.Code, ref.Message)
	}
	if len(resp.Data.Rows) != 1 || resp.Data.Rows[0].bundle != "local-real" {
		t.Fatalf("rows = %+v; want only the operator's own bundle", resp.Data.Rows)
	}
	if resp.Data.Rollup.bundles != 1 {
		t.Errorf("rollup bundles = %d, want 1", resp.Data.Rollup.bundles)
	}
	want := "trend skips 1 backtest bundle(s) (replay input, not your review history)"
	if !slices.Contains(resp.Warnings, want) {
		t.Errorf("warnings = %q, want %q", resp.Warnings, want)
	}
}

// writeTrendBundle lands one readable findings store, marked as replay input
// when marked asks for it.
func writeTrendBundle(t *testing.T, dir string, marked bool) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "findings.json"), []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !marked {
		return
	}
	marker := `{"schema_version":"1","mode":"replay"}`
	if err := os.WriteFile(filepath.Join(dir, readcommon.BacktestMarkerFile), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestTrendSkipsHostileEntryNames proves a state entry whose name is not one
// path element is skipped with a named warning rather than resolved: a listing
// carries names, and no name becomes a path.
func TestTrendSkipsHostileEntryNames(t *testing.T) {
	t.Parallel()
	read := core.ReadRequest{RepoRoot: t.TempDir()}
	st := &state{req: &Request{ReadRequest: read}}
	for _, name := range []string{"../escaped", "a/b", `a\b`, ".."} {
		dir, ok := st.listedBundleDir(name)
		if ok || dir != "" {
			t.Fatalf("listedBundleDir(%q) = %q, true; want a skip", name, dir)
		}
	}
	if len(st.warnings) != 4 {
		t.Fatalf("warnings = %q; want one per hostile name", st.warnings)
	}
	want := "trend skips ../escaped (bundle name must be one path element (a PR number or fetch-local name), not a path)"
	if st.warnings[0] != want {
		t.Errorf("warning = %q; want %q", st.warnings[0], want)
	}
}
