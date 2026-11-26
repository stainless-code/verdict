package query

// The git-index trust gate: a symlink the repository commits at or under
// .verdict refuses the root's verdict data on every read path, while a link
// the operator made locally keeps serving. Canary targets carry a finding
// signature no fixture uses, so a view that read through a link shows it.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/testenv"
)

// canarySig marks the outside store a link would serve.
const canarySig = "c0ffee0000000000000000000000000000000000"

// git runs one git command in dir with the test environment.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = testenv.Base(nil)
	cmd.WaitDelay = time.Minute
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitFixture is the standard fixture inside a fresh checkout.
func gitFixture(t *testing.T) string {
	t.Helper()
	dir, _ := fixture(t)
	git(t, dir, "init", "-q")
	return dir
}

// canaryTarget writes an outside copy of what rel would hold, carrying the
// canary finding, and returns the path the link points at.
func canaryTarget(t *testing.T, rel string) string {
	t.Helper()
	outside := t.TempDir()
	bundle := map[string]string{"PR.json": prDoc(), "findings.json": "[" + finding(canarySig, "open", "critical", "walked") + "]"}
	switch rel {
	case ".verdict":
		writeFiles(t, filepath.Join(outside, "state", "1234"), bundle)
		return outside
	case ".verdict/state":
		writeFiles(t, filepath.Join(outside, "1234"), bundle)
		return outside
	case ".verdict/state/1234":
		writeFiles(t, outside, bundle)
		return outside
	case ".verdict/state/1234/chunks":
		writeFiles(t, outside, map[string]string{"chunk-a.cost.json": `[{"models":[{"input_tokens":7}]}]`})
		return outside
	}
	writeFiles(t, outside, map[string]string{filepath.Base(rel): bundle["findings.json"]})
	return filepath.Join(outside, filepath.Base(rel))
}

// trackLink replaces rel with a link to target and stages it in the index.
func trackLink(t *testing.T, dir, rel, target string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, target, path)
	git(t, dir, "add", "-f", "--", filepath.FromSlash(rel))
}

// TestGitGateRefusesTrackedLinks proves a tracked symlink at each position
// under .verdict refuses the root on the detail, listing, and aggregate paths,
// keeps the watcher silent, and never serves the canary behind it.
func TestGitGateRefusesTrackedLinks(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlinks are unavailable in this Windows test environment")
	}
	for _, rel := range []string{
		".verdict", ".verdict/state", ".verdict/state/1234", ".verdict/state/1234/findings.json",
		".verdict/state/1234/threads.json", ".verdict/state/1234/chunks", ".verdict/overrides/x.md",
	} {
		t.Run(rel, func(t *testing.T) {
			t.Parallel()
			dir := gitFixture(t)
			trackLink(t, dir, rel, canaryTarget(t, rel))
			assertRootRefused(t, newLayer(dir), dir, rel)
		})
	}
}

// assertRootRefused proves every read path refuses the root, naming rel.
func assertRootRefused(t *testing.T, layer *Layer, dir, rel string) {
	t.Helper()
	view, err := layer.Detail(context.Background(), RootID(mustAbs(t, dir)), "1234")
	var ref *core.RefusalError
	if view != nil || !errors.As(err, &ref) || ref.Code != "bad-config" || !strings.Contains(ref.Message, rel) {
		t.Fatalf("Detail = %v, %v; want bad-config naming %s", view, err, rel)
	}
	assertAggregatesRefused(t, layer)
	state := newWatchState(t.Context(), layer)
	if changes := append(state.Tick(), state.Sweep()...); len(changes) != 0 {
		t.Errorf("watcher on a refused root = %#v, want silence", changes)
	}
}

// assertAggregatesRefused proves the listing and aggregate views carry the
// refusal as an error row and never the canary.
func assertAggregatesRefused(t *testing.T, layer *Layer) {
	t.Helper()
	for name, read := range map[string]func() (*View, error){
		"bundles":   func() (*View, error) { return layer.Bundles(context.Background(), "") },
		"cost":      func() (*View, error) { return layer.Cost(context.Background(), "model") },
		"precision": func() (*View, error) { return layer.Precision(context.Background(), "category") },
	} {
		got, err := read()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if raw := got.Data.MarshalCompact(); strings.Contains(raw, canarySig) || !strings.Contains(raw, "bad-config") {
			t.Errorf("%s view = %s, want a bad-config error row and no canary", name, raw)
		}
	}
}

// TestGitGateRefusesATrackedInRootLink proves the gate fails closed on any
// tracked link, even one whose target stays inside the checkout.
func TestGitGateRefusesATrackedInRootLink(t *testing.T) {
	t.Parallel()
	dir := gitFixture(t)
	writeFiles(t, dir, map[string]string{"docs/findings.json": "[" + finding(canarySig, "open", "critical", "walked") + "]"})
	trackLink(t, dir, ".verdict/state/1234/findings.json", filepath.Join(dir, "docs", "findings.json"))
	assertRootRefused(t, newLayer(dir), dir, ".verdict/state/1234/findings.json")
}

// TestGitGateServesAnUntrackedLocalLink proves a link the operator made and
// never committed is the operator's own: the root keeps serving through it.
func TestGitGateServesAnUntrackedLocalLink(t *testing.T) {
	t.Parallel()
	dir := gitFixture(t)
	writeFiles(t, dir, map[string]string{"docs/findings.json": "[" + finding(canarySig, "open", "critical", "walked") + "]"})
	link := filepath.Join(dir, ".verdict", "state", "1234", "findings.json")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, filepath.Join(dir, "docs", "findings.json"), link)
	view, err := newLayer(dir).Detail(context.Background(), RootID(mustAbs(t, dir)), "1234")
	if err != nil {
		t.Fatalf("Detail through an untracked local link = %v, want it served", err)
	}
	if !strings.Contains(view.Data.MarshalCompact(), canarySig) {
		t.Errorf("Detail did not serve the linked store")
	}
}

// TestBundleLookupNeverWalksARelayToTheRoot proves a bundle holding a link
// back to the checkout root costs no walk of the checkout: the lookup reads
// no directory outside the store, and a link elsewhere in the checkout,
// unrelated to the bundle, does not refuse it.
func TestBundleLookupNeverWalksARelayToTheRoot(t *testing.T) {
	t.Parallel()
	dir, _ := fixture(t)
	symlinkOrSkip(t, dir, filepath.Join(dir, ".verdict", "state", "1234", "relay"))
	writeFiles(t, dir, map[string]string{"node_modules/.keep": ""})
	symlinkOrSkip(t, t.TempDir(), filepath.Join(dir, "node_modules", "elsewhere"))
	var reads []string
	layer := New(Snapshot{Roots: []Root{{Path: dir}}, Runtime: &core.Runtime{Now: fixedNow}}, Options{
		Check: "critical", MaxBundles: 10, Stat: os.Stat,
		ReadDir: func(path string) ([]os.DirEntry, error) {
			reads = append(reads, path)
			return os.ReadDir(path)
		},
	})
	if _, err := layer.Detail(context.Background(), RootID(mustAbs(t, dir)), "1234"); err != nil {
		t.Fatalf("Detail with a relay link = %v, want it served", err)
	}
	store := filepath.Join(dir, ".verdict", "state")
	if i := slices.IndexFunc(reads, func(p string) bool { return !physicallyWithin(store, p) }); i >= 0 {
		t.Errorf("lookup read %s, outside the store", reads[i])
	}
}

// physicallyWithin reports whether path resolves to base or below it. Both
// sides are resolved first, so a link (the relay back to the root) is judged
// by where it lands, and Windows' 8.3 short names and slash spellings compare
// equal to their long forms. A path that does not resolve is not within.
func physicallyWithin(base, path string) bool {
	realBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return false
	}
	realPath, err := filepath.EvalSymlinks(filepath.FromSlash(path))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(realBase, realPath)
	return err == nil && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// TestGitGateServesARootBehindALinkedAncestor proves a checkout reached
// through a linked ancestor directory (macOS /tmp is /private/tmp) serves.
func TestGitGateServesARootBehindALinkedAncestor(t *testing.T) {
	t.Parallel()
	checkout := gitFixture(t)
	alias := filepath.Join(t.TempDir(), "alias")
	symlinkOrSkip(t, checkout, alias)
	if _, err := newLayer(alias).Detail(context.Background(), RootID(mustAbs(t, alias)), "1234"); err != nil {
		t.Fatalf("Detail through a linked ancestor = %v, want it served", err)
	}
}

// TestGitGateFailsClosedOnABrokenCheckout proves a root inside a checkout
// git cannot read refuses, naming the git failure, instead of serving
// unverified data.
func TestGitGateFailsClosedOnABrokenCheckout(t *testing.T) {
	t.Parallel()
	dir, _ := fixture(t)
	writeFiles(t, dir, map[string]string{".git": "gitdir: missing\n"})
	_, err := newLayer(dir).Detail(context.Background(), RootID(mustAbs(t, dir)), "1234")
	var ref *core.RefusalError
	if !errors.As(err, &ref) || ref.Code != "bad-config" || !strings.Contains(ref.Message, "git ") {
		t.Fatalf("Detail in a broken checkout = %v, want bad-config naming the git failure", err)
	}
}

// TestGitGateRefusesACaseVariantTrackedLink proves the gate matches .verdict
// case-insensitively: on a case-insensitive filesystem a tracked .Verdict is
// the very directory the readers open as .verdict, so a link committed there
// refuses the root like one under .verdict.
func TestGitGateRefusesACaseVariantTrackedLink(t *testing.T) {
	t.Parallel()
	dir := gitFixture(t)
	if !caseInsensitive(t, dir) {
		t.Skip("this filesystem is case-sensitive: .Verdict and .verdict are different directories")
	}
	rel := ".Verdict/state/1234/findings.json"
	trackLink(t, dir, rel, canaryTarget(t, rel))
	assertRootRefused(t, newLayer(dir), dir, rel)
}

// caseInsensitive reports whether dir's filesystem folds case.
func caseInsensitive(t *testing.T, dir string) bool {
	t.Helper()
	probe := filepath.Join(dir, "case-probe")
	if err := os.WriteFile(probe, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := os.Stat(filepath.Join(dir, "CASE-PROBE"))
	if rerr := os.Remove(probe); rerr != nil {
		t.Fatal(rerr)
	}
	return err == nil
}
