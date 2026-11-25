package lsp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setFindingsMtime(t *testing.T, root, bundle string, at time.Time) {
	t.Helper()
	p := filepath.Join(root, ".verdict", "state", bundle, "findings.json")
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
}

func writePRBundle(t *testing.T, root, pr string) {
	t.Helper()
	dir := ".verdict/state/" + pr + "/"
	writeFile(t, root, dir+"PR.json", `{"state":"OPEN","headRefOid":"`+headB+`","baseRefOid":"`+headB+`",`+
		`"author":{"login":"teammate"},"url":"https://github.com/acme/app/pull/`+pr+`"}`)
	writeFile(t, root, dir+"full.diff", fixtureDiff)
	writeFile(t, root, dir+"findings.json", "["+findingJSON(sigCrit, "open", "critical", "notes.txt", 2)+"]")
}

// TestSelectBundleRanking pins auto-selection: local- bundles beat newer PR
// bundles, the newest findings store wins among locals, a tie breaks by
// name, corpus bundles and bundles without a findings store never win, and
// the operator's --bundle and the client's initializationOptions.bundle
// override the ranking in that order.
func TestSelectBundleRanking(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := old.Add(time.Hour)
	writeBundle(t, root, "local-a", findingJSON(sigCrit, "open", "critical", "notes.txt", 2))
	writeBundle(t, root, "local-b", findingJSON(sigCrit, "open", "critical", "notes.txt", 2))
	writeBundle(t, root, "local-c", findingJSON(sigCrit, "open", "critical", "notes.txt", 2))
	writeBundle(t, root, "corpus-1", findingJSON(sigCrit, "open", "critical", "notes.txt", 2))
	writePRBundle(t, root, "77")
	writeFile(t, root, ".verdict/state/local-z/full.diff", fixtureDiff)
	setFindingsMtime(t, root, "local-a", old)
	setFindingsMtime(t, root, "local-b", newer)
	setFindingsMtime(t, root, "local-c", newer)
	setFindingsMtime(t, root, "corpus-1", newer.Add(time.Hour))
	setFindingsMtime(t, root, "77", newer.Add(2*time.Hour))

	for _, tc := range []struct {
		name     string
		flag     string
		initOpts string
		want     string
	}{
		{"auto", "", "", "local-b"},
		{"client option", "", "77", "77"},
		{"flag beats client", "local-a", "77", "local-a"},
	} {
		cfg := newConfig(root)
		cfg.Bundle = tc.flag
		s := New(cfg)
		s.initOpts.Bundle = tc.initOpts
		got, err := s.newJob(&s.roots[0]).selectBundle(context.Background(), &s.roots[0])
		if err != nil || got != tc.want {
			t.Errorf("%s: selectBundle = %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
}

// TestSelectBundlePrefersPRWithoutLocal pins the fallback: with no local
// bundle, the newest PR bundle wins.
func TestSelectBundlePrefersPRWithoutLocal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writePRBundle(t, root, "12")
	writePRBundle(t, root, "34")
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	setFindingsMtime(t, root, "12", at.Add(time.Hour))
	setFindingsMtime(t, root, "34", at)
	s := New(newConfig(root))
	if got, err := s.newJob(&s.roots[0]).selectBundle(context.Background(), &s.roots[0]); err != nil || got != "12" {
		t.Fatalf("selectBundle = %q, %v; want 12", got, err)
	}
}

// TestPreflightRefusesAnUnknownBundle pins the startup refusal for a
// --bundle no served root carries, and accepts one any root carries.
func TestPreflightRefusesAnUnknownBundle(t *testing.T) {
	t.Parallel()
	empty := t.TempDir()
	root := fixtureRoot(t)
	cfg := newConfig(empty, root)
	cfg.Bundle = "local-1"
	if ref := New(cfg).Preflight(); ref != nil {
		t.Fatalf("Preflight(local-1 under the second root) = %v", ref)
	}
	cfg.Bundle = "local-9"
	ref := New(cfg).Preflight()
	if ref == nil || ref.Code != "missing-bundle" {
		t.Fatalf("Preflight(local-9) = %v, want missing-bundle", ref)
	}
	cfg.Bundle = "../escape"
	if ref := New(cfg).Preflight(); ref == nil {
		t.Fatal("Preflight(../escape) accepted a traversal name")
	}
}

// TestMultiRootPublishesPerRoot pins multi-root serving: each root
// publishes its own bundle under its own URIs.
func TestMultiRootPublishesPerRoot(t *testing.T) {
	t.Parallel()
	first := fixtureRoot(t)
	second := t.TempDir()
	writeBundle(t, second, "local-9", findingJSON(sigInfo, "open", "informational", "notes.txt", 2))
	s := startSession(t, newConfig(first, second))
	s.initialize(1, nil)
	s.notify(methodInitialized, map[string]any{})
	got := publishes(t, s.barrier(2, 1))
	if len(got) != 3 {
		t.Fatalf("publishes = %d, want 3", len(got))
	}
	if got[2].URI != fileURI(second, "notes.txt") || sigsOf(&got[2]) != sigInfo || got[2].Diagnostics[0].Data.Bundle != "local-9" {
		t.Fatalf("second root publish = %+v", got[2])
	}
	if err := s.finish(3); err != nil {
		t.Fatal(err)
	}
}
