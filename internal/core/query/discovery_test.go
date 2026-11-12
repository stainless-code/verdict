package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
)

func discoveryNow() time.Time {
	return time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
}

func makeRepo(t *testing.T, root, rel string) string {
	t.Helper()
	repo := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Join(repo, ".verdict"), 0o755); err != nil {
		t.Fatal(err)
	}
	return repo
}

// TestDiscoveryFixture pins traversal semantics to a literal tree.
func TestDiscoveryFixture(t *testing.T) {
	root, symlinkCount := copyDiscoveryFixture(t)
	got, err := Discover(root, DiscoveryOptions{Depth: 3, Now: discoveryNow()})
	if err != nil {
		t.Fatal(err)
	}
	assertDiscoveryFixture(t, &got, root, symlinkCount)
}

// TestDiscoveryCountsSymlinksWithoutFollowing counts links without inspecting targets.
func TestDiscoveryCountsSymlinksWithoutFollowing(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	makeRepo(t, outside, "foreign")
	if err := os.WriteFile(filepath.Join(root, "plain-file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	links := []struct{ target, name string }{
		{target: ".", name: "loop"},
		{target: outside, name: "outside"},
		{target: "plain-file", name: "file-link"},
	}
	for _, link := range links {
		if err := os.Symlink(link.target, filepath.Join(root, link.name)); err != nil {
			t.Skipf("symlinks are unavailable: %v", err)
		}
	}
	got, err := Discover(root, DiscoveryOptions{Depth: 3, Now: discoveryNow()})
	if err != nil {
		t.Fatal(err)
	}
	if got.Symlinks != 3 || len(got.Roots) != 0 || got.Visits != 1 {
		t.Fatalf("symlink discovery = %+v; want three counted links, no roots, and one visit", got)
	}
}

func copyDiscoveryFixture(t *testing.T) (string, int) {
	t.Helper()
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS("testdata/discovery")); err != nil {
		t.Fatal(err)
	}
	makeRepo(t, root, ".git/hidden")
	var symlinkCount int
	if err := os.Symlink("ignored", filepath.Join(root, "linked")); err == nil {
		symlinkCount = 1
	} else if runtime.GOOS != "windows" {
		t.Fatal(err)
	}
	return root, symlinkCount
}

func assertDiscoveryFixture(t *testing.T, got *Discovery, root string, symlinkCount int) {
	t.Helper()
	paths := make([]string, 0, len(got.Roots))
	for _, repo := range got.Roots {
		paths = append(paths, repo.Path)
	}
	want := []string{root, filepath.Join(root, "visible", "child"), filepath.Join(root, "x")}
	if !slices.Equal(paths, want) {
		t.Fatalf("roots = %q, want %q", paths, want)
	}
	if got.Symlinks != symlinkCount {
		t.Errorf("symlinks = %d, want %d", got.Symlinks, symlinkCount)
	}
	if got.SkippedPatterns != 21 || len(got.Warnings) != 21 || !strings.Contains(got.Warnings[0], ".gitignore:2: skipped gitignore pattern \"?\"") || got.Warnings[20] != "… and 1 more skipped patterns" {
		t.Errorf("ignored pattern outcome = (%d, %q), want 20 line-numbered warnings and the summary", got.SkippedPatterns, got.Warnings)
	}
}

// TestRegistryExpiryAndRebuild pins metadata, TTL, and file mode.
func TestRegistryExpiryAndRebuild(t *testing.T) {
	root, registryPath, opts, reads := registryExpiryFixture(t)
	first, err := DiscoverCached(root, opts, registryPath, false, false)
	if err != nil {
		t.Fatal(err)
	}
	assertRegistryScan(t, "first", &first, *reads, 1, 3, false)
	if first.Warnings[2] != "serve: registry unavailable or corrupt; rebuilding" {
		t.Errorf("first registry warning = %q", first.Warnings[2])
	}
	warm := opts
	warm.Now = opts.Now.Add(registryTTL - time.Nanosecond)
	nearExpiry, err := DiscoverCached(root, warm, registryPath, false, false)
	if err != nil {
		t.Fatal(err)
	}
	assertRegistryScan(t, "near-expiry", &nearExpiry, *reads, 1, 2, true)
	expired := opts
	expired.Now = opts.Now.Add(registryTTL)
	rebuilt, err := DiscoverCached(root, expired, registryPath, false, false)
	if err != nil {
		t.Fatal(err)
	}
	assertRegistryScan(t, "expired", &rebuilt, *reads, 2, 2, false)
	assertRegistryMode(t, registryPath)
}

func registryExpiryFixture(t *testing.T) (string, string, DiscoveryOptions, *int) {
	t.Helper()
	root := t.TempDir()
	makeRepo(t, root, ".")
	makeRepo(t, root, "child")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("?\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registryPath := filepath.Join(t.TempDir(), "projects.json")
	reads := 0
	opts := DiscoveryOptions{Depth: 1, MaxVisits: 1, MaxRepos: 10, Now: discoveryNow(), ReadDir: countingReadDir(&reads)}
	return root, registryPath, opts, &reads
}

func countingReadDir(reads *int) func(*os.Root, string) ([]os.DirEntry, error) {
	return func(root *os.Root, path string) ([]os.DirEntry, error) {
		*reads++
		f, err := root.Open(path)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		return f.ReadDir(-1)
	}
}

func assertRegistryScan(t *testing.T, stage string, got *Discovery, reads, wantReads, wantWarnings int, cached bool) {
	t.Helper()
	if got.Cached != cached || !got.Truncated || got.SkippedPatterns != 1 || len(got.Warnings) != wantWarnings || reads != wantReads {
		t.Fatalf("%s discovery = %+v, directory reads=%d; want cached=%v, truncated scan, %d warnings, %d reads", stage, got, reads, cached, wantWarnings, wantReads)
	}
}

func assertRegistryMode(t *testing.T, registryPath string) {
	t.Helper()
	info, err := os.Stat(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("registry mode = %04o, want 0600", info.Mode().Perm())
	}
}

// TestRegistryCorruptIsRebuilt rejects an unsupported schema and rebuilds.
func TestRegistryCorruptIsRebuilt(t *testing.T) {
	root := t.TempDir()
	makeRepo(t, root, ".")
	registryPath := filepath.Join(t.TempDir(), "projects.json")
	if err := os.WriteFile(registryPath, []byte(`{"schema_version":"9"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := DiscoverCached(root, DiscoveryOptions{Depth: 1, Now: discoveryNow()}, registryPath, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cached || len(got.Roots) != 1 || len(got.Warnings) != 1 || got.Warnings[0] != "serve: registry unavailable or corrupt; rebuilding" {
		t.Fatalf("rebuilt discovery = %+v", got)
	}
	data, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	var decoded registry
	if err := json.Unmarshal(data, &decoded); err != nil || decoded.Version != "1" {
		t.Fatalf("registry after rebuild = %s, decode error %v", data, err)
	}
}

// TestUnsafeRegistryFilesAreRebuilt rejects permissive files and symlinks.
func TestUnsafeRegistryFilesAreRebuilt(t *testing.T) {
	t.Run("world-writable", testWorldWritableRegistryIsRebuilt)
	t.Run("symlink", testSymlinkedRegistryIsRebuilt)
}

func testWorldWritableRegistryIsRebuilt(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not model POSIX permission bits")
	}
	root, path, opts := seedEmptyRegistry(t)
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	got, err := DiscoverCached(root, opts, path, false, false)
	if err != nil {
		t.Fatal(err)
	}
	assertUnsafeRegistryRebuilt(t, got)
	assertRegistryMode(t, path)
}

func testSymlinkedRegistryIsRebuilt(t *testing.T) {
	t.Helper()
	root, path, opts := seedEmptyRegistry(t)
	target := filepath.Join(t.TempDir(), "target.json")
	if err := os.Rename(path, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	targetBefore, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DiscoverCached(root, opts, path, false, false)
	if err != nil {
		t.Fatal(err)
	}
	assertUnsafeRegistryRebuilt(t, got)
	linkInfo, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		t.Fatal("rebuilt registry is still a symlink")
	}
	targetAfter, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(targetBefore, targetAfter) {
		t.Fatal("registry rebuild changed the symlink target")
	}
}

func seedEmptyRegistry(t *testing.T) (string, string, DiscoveryOptions) {
	t.Helper()
	root := t.TempDir()
	makeRepo(t, root, ".")
	path := filepath.Join(t.TempDir(), "projects.json")
	opts := DiscoveryOptions{Depth: 1, Now: discoveryNow()}
	if _, err := DiscoverCached(root, opts, path, false, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved registry
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	saved.Roots[0].Repos = []string{}
	data, err = json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return root, path, opts
}

func assertUnsafeRegistryRebuilt(t *testing.T, got Discovery) {
	t.Helper()
	if got.Cached || len(got.Roots) != 1 || len(got.Warnings) == 0 ||
		got.Warnings[len(got.Warnings)-1] != "serve: registry unavailable or corrupt; rebuilding" {
		t.Fatalf("unsafe registry discovery = %+v; want rebuilt root and warning", got)
	}
}

// TestWalkCapsTruncateVisibly requires both caps to report truncation.
func TestWalkCapsTruncateVisibly(t *testing.T) {
	root := t.TempDir()
	makeRepo(t, root, ".")
	makeRepo(t, root, "a")
	makeRepo(t, root, "b")
	visits, err := Discover(root, DiscoveryOptions{Depth: 5, MaxVisits: 1, MaxRepos: 10, Now: discoveryNow()})
	if err != nil {
		t.Fatal(err)
	}
	if !visits.Truncated || visits.Visits != 1 || len(visits.Warnings) != 1 || !strings.Contains(visits.Warnings[0], "max visits 1 reached") {
		t.Errorf("visit cap outcome = %+v", visits)
	}
	repos, err := Discover(root, DiscoveryOptions{Depth: 5, MaxVisits: 20, MaxRepos: 1, Now: discoveryNow()})
	if err != nil {
		t.Fatal(err)
	}
	if !repos.Truncated || len(repos.Roots) != 1 || len(repos.Warnings) != 1 || !strings.Contains(repos.Warnings[0], "max repos 1 reached") {
		t.Errorf("repository cap outcome = %+v", repos)
	}
}

// TestRepositoryCapsAreExact checks both the explicit and default repo caps.
func TestRepositoryCapsAreExact(t *testing.T) {
	t.Run("explicit", testExplicitRepositoryCapIsExact)
	t.Run("default", testDefaultRepositoryCapIsExact)
}

func testExplicitRepositoryCapIsExact(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	want := makeRepo(t, root, "a")
	makeRepo(t, root, "b")
	got, err := Discover(root, DiscoveryOptions{Depth: 2, MaxRepos: 1, Now: discoveryNow()})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Roots) != 1 || got.Roots[0].Path != want || !got.Truncated || len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "max repos 1 reached") {
		t.Fatalf("explicit repo cap = %+v; want only %q and a truncation warning", got, want)
	}
}

func testDefaultRepositoryCapIsExact(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	for i := range DefaultMaxRepos + 1 {
		makeRepo(t, root, fmt.Sprintf("repo-%03d", i))
	}
	got, err := Discover(root, DiscoveryOptions{Depth: 1, Now: discoveryNow()})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Roots) != DefaultMaxRepos || !got.Truncated || len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "max repos 500 reached") {
		t.Fatalf("default repo cap = roots %d, truncated %v, warnings %q; want exactly 500 and a truncation warning", len(got.Roots), got.Truncated, got.Warnings)
	}
}

// TestTruncatedDiscoveryMakesInboxPartial keeps incomplete discovery out of totals.
func TestTruncatedDiscoveryMakesInboxPartial(t *testing.T) {
	workspace := t.TempDir()
	makeRepo(t, workspace, "a")
	makeRepo(t, workspace, "b")
	discovery, err := Discover(workspace, DiscoveryOptions{Depth: 1, MaxRepos: 1, Now: discoveryNow()})
	if err != nil {
		t.Fatal(err)
	}
	if !discovery.Truncated || len(discovery.Roots) != 1 {
		t.Fatalf("discovery = %+v; want one root and truncation", discovery)
	}
	layer := New(Snapshot{
		Version: "1.2.3", Roots: discovery.Roots, Runtime: &core.Runtime{Now: fixedNow}, Discovery: discovery,
	}, Options{Check: "critical", MaxBundles: 10})
	view, err := layer.Bundles(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	data := wireMap(t, view.Data)
	if data["counts"] != nil || data["partial"] != true || len(asList(t, data["rows"])) != 0 {
		t.Errorf("truncated inbox = counts %v, partial %v, rows %d; want null counts, partial true, and no bundles", data["counts"], data["partial"], len(asList(t, data["rows"])))
	}
	if len(view.Warnings) == 0 || !strings.Contains(view.Warnings[0], "max repos 1 reached") {
		t.Errorf("inbox warnings = %q; want the discovery truncation warning", view.Warnings)
	}
}

// TestRepeatedDoubleStarPatternIsSkipped bounds the recursive glob matcher.
func TestRepeatedDoubleStarPatternIsSkipped(t *testing.T) {
	root := t.TempDir()
	want := makeRepo(t, root, filepath.Join("a", "b", "c", "d", "e", "f"))
	pattern := strings.Repeat("**/", 2000) + "z"
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(pattern+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Discover(root, DiscoveryOptions{Depth: 6, MaxVisits: 20, Now: discoveryNow()})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Roots) != 1 || got.Roots[0].Path != want || got.SkippedPatterns != 1 || len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], ".gitignore:1: skipped gitignore pattern") {
		t.Fatalf("complex gitignore discovery = %+v; want the nested repo and one skipped-pattern warning", got)
	}
}

// TestWarmRegistryRestartDoesNotReadDirectories pins zero warm walk reads.
func TestWarmRegistryRestartDoesNotReadDirectories(t *testing.T) {
	root, registryPath, opts, reads := warmRegistryFixture(t)
	first, err := DiscoverCached(root, opts, registryPath, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Truncated {
		t.Fatal("initial discovery unexpectedly truncated")
	}
	*reads = 0
	warm, err := DiscoverCached(root, opts, registryPath, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !warm.Cached || *reads != 0 || len(warm.Roots) != 1 || warm.Roots[0].Path != root {
		t.Fatalf("warm registry = %+v, directory reads=%d; want cached root and zero reads", warm, *reads)
	}
}

func warmRegistryFixture(t *testing.T) (string, string, DiscoveryOptions, *int) {
	t.Helper()
	root := t.TempDir()
	makeRepo(t, root, ".")
	registryPath := filepath.Join(t.TempDir(), "projects.json")
	reads := 0
	opts := DiscoveryOptions{Depth: 1, Now: discoveryNow(), ReadDir: countingReadDir(&reads)}
	return root, registryPath, opts, &reads
}

// TestRediscoverKeepsIndependentRegistryRoots retains other cached roots.
func TestRediscoverKeepsIndependentRegistryRoots(t *testing.T) {
	workspace := t.TempDir()
	rootA := makeRepo(t, workspace, "a")
	rootB := makeRepo(t, workspace, "b")
	paths := rediscoverRootPaths(t, rootA, rootB)
	if !slices.Equal(paths, []string{rootB, rootA}) {
		t.Errorf("registry roots = %q, want B retained and A replaced", paths)
	}
}

func rediscoverRootPaths(t *testing.T, rootA, rootB string) []string {
	t.Helper()
	registryPath := filepath.Join(t.TempDir(), "projects.json")
	opts := DiscoveryOptions{Depth: 1, Now: discoveryNow()}
	for _, root := range []string{rootA, rootB} {
		if _, err := DiscoverCached(root, opts, registryPath, false, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := DiscoverCached(rootA, opts, registryPath, true, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	var saved registry
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(saved.Roots))
	for i := range saved.Roots {
		paths = append(paths, saved.Roots[i].Path)
	}
	return paths
}

// TestMultiRepoInboxCountsAreLiteralsAndOverrideIsLocal pins local counts.
func TestMultiRepoInboxCountsAreLiteralsAndOverrideIsLocal(t *testing.T) {
	data, rootA, rootB := multiRepoInbox(t)
	assertMultiRepoCounts(t, data)
	assertOverrideLocal(t, data)
	assertMultiRepoRoots(t, data, rootA, rootB)
}

func multiRepoInbox(t *testing.T) (map[string]any, string, string) {
	t.Helper()
	workspace := t.TempDir()
	rootA := makeRepo(t, workspace, "a")
	rootB := makeRepo(t, workspace, "b")
	for _, root := range []string{rootA, rootB} {
		writeFiles(t, root, map[string]string{
			".verdict/state/1234/PR.json":       prDoc(),
			".verdict/state/1234/post.json":     postDoc(headA, 7),
			".verdict/state/1234/findings.json": "[" + finding(sigCrit, "open", "critical", "walked") + "]",
		})
	}
	writeFiles(t, rootA, map[string]string{".verdict/overrides/preamble.md": "### invalid\n"})
	discovery, err := Discover(workspace, DiscoveryOptions{Depth: 2, Now: fixedNow()})
	if err != nil {
		t.Fatal(err)
	}
	layer := New(Snapshot{Version: "1.2.3", Roots: discovery.Roots, Runtime: &core.Runtime{Now: fixedNow}, Discovery: discovery}, Options{Check: "critical", MaxBundles: 10})
	view, err := layer.Bundles(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	return wireMap(t, view.Data), rootA, rootB
}

func assertMultiRepoCounts(t *testing.T, data map[string]any) {
	t.Helper()
	rows := asList(t, data["rows"])
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want two repositories", rows)
	}
	rowA, rowB := asMap(t, rows[0]), asMap(t, rows[1])
	if rowA["open"] != nil || rowB["open"] != json.Number("1") {
		t.Errorf("open counts = (%v, %v), want (null, 1)", rowA["open"], rowB["open"])
	}
	if data["counts"] != nil || data["partial"] != true {
		t.Errorf("portfolio = (%v, %v), want (null, true)", data["counts"], data["partial"])
	}
}

func assertOverrideLocal(t *testing.T, data map[string]any) {
	t.Helper()
	errorRows := asList(t, data["errors"])
	if len(errorRows) != 1 || asMap(t, errorRows[0])["code"] != "bad-override" {
		t.Errorf("errors = %v, want only root A's bad-override", errorRows)
	}
}

func assertMultiRepoRoots(t *testing.T, data map[string]any, rootA, rootB string) {
	t.Helper()
	rows := asList(t, data["rows"])
	rowA, rowB := asMap(t, rows[0]), asMap(t, rows[1])
	if rowA["root"] != RootID(rootA) || rowB["root"] != RootID(rootB) {
		t.Errorf("row roots = (%v, %v), want ids for root A and B", rowA["root"], rowB["root"])
	}
}

// TestDiscoverCachedNoRegistryLeavesNoFile checks disabled caching.
func TestDiscoverCachedNoRegistryLeavesNoFile(t *testing.T) {
	root := t.TempDir()
	makeRepo(t, root, ".")
	registryPath := filepath.Join(t.TempDir(), "projects.json")
	if _, err := DiscoverCached(root, DiscoveryOptions{Depth: 1, Now: discoveryNow()}, registryPath, false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(registryPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("registry stat error = %v, want not exist", err)
	}
}

// TestDiscoveryCacheStatusIsInRootsAndInbox shares scan metadata across views.
func TestDiscoveryCacheStatusIsInRootsAndInbox(t *testing.T) {
	dir, _ := fixture(t)
	discovery := Discovery{Cached: true, ScannedAt: discoveryNow()}
	layer := New(Snapshot{Version: "1.2.3", Roots: []Root{{Path: dir}}, Runtime: &core.Runtime{Now: fixedNow}, Discovery: discovery}, Options{Check: "critical", MaxBundles: 10})
	views := []struct {
		view *View
		name string
	}{
		{name: "roots", view: layer.Roots(context.Background())},
		{name: "inbox", view: mustBundles(t, layer)},
		{name: "health", view: layer.Health(context.Background())},
	}
	for _, item := range views {
		data := wireMap(t, item.view.Data)
		metadata := asMap(t, data["discovery"])
		if metadata["cached"] != true || metadata["scanned_at"] != "2026-11-12T09:00:00Z" {
			t.Errorf("%s discovery = %v, want cached at the literal scan time", item.name, metadata)
		}
	}
}

// TestWrappedMissingDirectoryIsIgnored recognizes wrapped not-exist errors.
func TestWrappedMissingDirectoryIsIgnored(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "gone"), 0o755); err != nil {
		t.Fatal(err)
	}
	read := func(dir *os.Root, name string) ([]os.DirEntry, error) {
		if name == "gone" {
			return nil, fmt.Errorf("read directory: %w", fs.ErrNotExist)
		}
		return rootReadDir(dir, name)
	}
	got, err := Discover(root, DiscoveryOptions{Depth: 1, Now: discoveryNow(), ReadDir: read})
	if err != nil {
		t.Fatal(err)
	}
	if got.Visits != 2 || len(got.Warnings) != 0 {
		t.Errorf("scan = %+v, want two visits and no warning for a missing child", got)
	}
}

func mustBundles(t *testing.T, layer *Layer) *View {
	t.Helper()
	view, err := layer.Bundles(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	return view
}
