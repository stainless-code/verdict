package query

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

const (
	headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	sigCrit = "c111111111111111111111111111111111111111"
	sigImp  = "b222222222222222222222222222222222222222"
	sigAddr = "d444444444444444444444444444444444444444"
	sigOpen = "e555555555555555555555555555555555555555"
)

// fixedNow pins every composed timestamp.
func fixedNow() time.Time { return time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC) }

// newLayer builds one layer over dir with the spec's default options.
func newLayer(dir string) *Layer {
	return New(Snapshot{
		Version: "1.2.3",
		Roots:   []Root{{Path: dir}},
		Runtime: &core.Runtime{Now: fixedNow},
	}, Options{Check: "critical", MaxBundles: 10})
}

// finding renders one store entry.
func finding(sig, status, severity, rigor string) string {
	return fmt.Sprintf(`{"kind":"inline","path":"notes.txt","side":"RIGHT","line":2,`+
		`"anchored":true,"body":"Usable body here with enough substance.","suggestion":null,`+
		`"inBounds":true,"rigor":%q,"severity":%q,"sig":%q,"sig_version":"1",`+
		`"firstSeen":"aaaaaaa","lastSeen":"aaaaaaa","status":%q,"rationale":"","history":[]}`,
		rigor, severity, sig, status)
}

// prDoc renders a GitHub-shaped PR.json.
func prDoc() string {
	return fmt.Sprintf(`{"state":"OPEN","headRefOid":%q,"baseRefOid":%q,`+
		`"author":{"login":"teammate"},"url":"https://github.com/acme/app/pull/1234"}`,
		headA, headB)
}

// sourceDoc renders a local bundle source.json.
func sourceDoc(head string) string {
	return fmt.Sprintf(`{"schema_version":"1","kind":"range","repo":"acme/app","slug":"work",`+
		`"head_ref":"v2","base_sha":%q,"head_sha":%q,"dirty":false}`, headB, head)
}

// postDoc renders a completed post receipt.
func postDoc(head string, id int) string {
	return fmt.Sprintf(`{"schema_version":"1","review":{"id":%d},"head":%q,"model":"m1",`+
		`"posted_at":"2026-01-01T00:00:00Z","confidence":null,"security_summary":null}`, id, head)
}

// fullDiff is the fixture's raw diff, present in the bundle but consumed by
// no composed read: the inventory still carries it.
const fullDiff = `diff --git a/notes.txt b/notes.txt
index 1111111..2222222 100644
--- a/notes.txt
+++ b/notes.txt
@@ -1,3 +1,3 @@
 top
-old line
+new line
 bottom
`

// writeFiles writes relpath->content fixtures under dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// fixture reads the two-bundle root the count test pins.
func fixture(t *testing.T) (string, map[string]string) {
	t.Helper()
	files := map[string]string{
		".verdict/state/1234/PR.json":        prDoc(),
		".verdict/state/1234/full.diff":      fullDiff,
		".verdict/state/1234/post.json":      postDoc(headA, 7),
		".verdict/state/local-1/source.json": sourceDoc(headB),
		".verdict/state/local-1/full.diff":   fullDiff,
		".verdict/state/local-1/post.json":   postDoc(headB, 8),
		".verdict/state/1234/findings.json": "[" + strings.Join([]string{
			finding(sigCrit, "open", "critical", "walked"),
			finding(sigImp, "open", "important", "pointed"),
			finding(sigAddr, "addressed", "important", "walked"),
		}, ",") + "]",
		".verdict/state/local-1/findings.json": "[" +
			finding(sigOpen, "open", "critical", "said") + "]",
	}
	dir := t.TempDir()
	writeFiles(t, dir, files)
	return dir, files
}

// wireMap decodes an ordered object into a number-faithful map.
func wireMap(t *testing.T, obj interface{ MarshalCompact() string }) map[string]any {
	t.Helper()
	var out map[string]any
	dec := json.NewDecoder(strings.NewReader(obj.MarshalCompact()))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("decode wire object: %v", err)
	}
	return out
}

// asMap asserts one decoded object.
func asMap(t *testing.T, value any) map[string]any {
	t.Helper()
	out, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("value %v is not an object", value)
	}
	return out
}

// asList asserts one decoded array.
func asList(t *testing.T, value any) []any {
	t.Helper()
	out, ok := value.([]any)
	if !ok {
		t.Fatalf("value %v is not an array", value)
	}
	return out
}

// asString asserts one decoded string.
func asString(t *testing.T, value any) string {
	t.Helper()
	out, ok := value.(string)
	if !ok {
		t.Fatalf("value %v is not a string", value)
	}
	return out
}

// rowLiteral is one hand-written inbox row expectation.
type rowLiteral struct { //nolint:govet // literal expectation row
	bundle    string
	open      string
	deferred  string
	addressed string
	terminal  string
	head      any
	model     any
}

// TestInboxCountsAreLiterals pins hand-written counts for two bundles. A
// flipped finding status in the fixture must move one of these literals.
func TestInboxCountsAreLiterals(t *testing.T) {
	t.Parallel()
	dir, _ := fixture(t)
	view, err := newLayer(dir).Bundles(context.Background(), "")
	if err != nil {
		t.Fatalf("Bundles: %v", err)
	}
	data := wireMap(t, view.Data)
	rows := asList(t, data["rows"])
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want two bundles", data["rows"])
	}
	assertRowLiteral(t, asMap(t, rows[0]), &rowLiteral{
		bundle: "1234", open: "2", deferred: "0", addressed: "1", terminal: "0",
		head: headA, model: "m1",
	})
	assertRowLiteral(t, asMap(t, rows[1]), &rowLiteral{
		bundle: "local-1", open: "1", deferred: "0", addressed: "0", terminal: "0",
		head: headB, model: "m1",
	})
	assertPortfolio(t, asMap(t, data["counts"]), map[string]string{
		"bundles": "2", "open": "3", "deferred": "0", "addressed": "1", "terminal": "0",
	})
	if data["partial"] != false {
		t.Errorf("partial = %v, want false", data["partial"])
	}
}

// assertRowLiteral compares one inbox row with its literal expectation.
func assertRowLiteral(t *testing.T, row map[string]any, want *rowLiteral) {
	t.Helper()
	got := []any{row["bundle"], row["open"], row["deferred"], row["addressed"], row["terminal"], row["head"], row["model"]}
	expect := []any{
		want.bundle,
		json.Number(want.open),
		json.Number(want.deferred),
		json.Number(want.addressed),
		json.Number(want.terminal),
		want.head,
		want.model,
	}
	if !slices.Equal(got, expect) {
		t.Errorf("row = %v, want %v", got, expect)
	}
}

// assertPortfolio compares the rollup with hand-written literals.
func assertPortfolio(t *testing.T, counts map[string]any, want map[string]string) {
	t.Helper()
	for key, value := range want {
		if counts[key] != json.Number(value) {
			t.Errorf("counts.%s = %v, want %s", key, counts[key], value)
		}
	}
}

// TestCorruptStoreIsNullNotZero proves INV-1 at the query layer: the corrupt
// row keeps null counts, the portfolio is null and partial, and the detail
// returns a typed refusal.
func TestCorruptStoreIsNullNotZero(t *testing.T) {
	t.Parallel()
	dir, _ := fixture(t)
	writeFiles(t, dir, map[string]string{".verdict/state/local-1/findings.json": "{"})
	layer := newLayer(dir)
	view, err := layer.Bundles(context.Background(), "")
	if err != nil {
		t.Fatalf("Bundles: %v", err)
	}
	data := wireMap(t, view.Data)
	if data["counts"] != nil {
		t.Errorf("counts = %v, want null when a bundle refuses", data["counts"])
	}
	if data["partial"] != true {
		t.Errorf("partial = %v, want true", data["partial"])
	}
	rows := asList(t, data["rows"])
	assertCorruptRow(t, asMap(t, rows[1]))
	assertCorruptError(t, asList(t, data["errors"]))
	root := asString(t, view.Data.Fields["root"])
	_, err = layer.Detail(context.Background(), root, "local-1")
	var ref *core.RefusalError
	if !errors.As(err, &ref) || ref.Code != "corrupt-store" {
		t.Errorf("Detail(corrupt) = %v, want corrupt-store", err)
	}
}

// assertCorruptRow proves every numeric count is null, never zero.
func assertCorruptRow(t *testing.T, row map[string]any) {
	t.Helper()
	for _, key := range []string{"open", "deferred", "addressed", "terminal"} {
		if row[key] != nil {
			t.Errorf("corrupt row %s = %v, want null", key, row[key])
		}
	}
}

// assertCorruptError proves the one tolerated failure row.
func assertCorruptError(t *testing.T, errs []any) {
	t.Helper()
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want one", errs)
	}
	row := asMap(t, errs[0])
	if row["code"] != "corrupt-store" || row["bundle"] != "local-1" {
		t.Errorf("error row = %v, want corrupt-store naming local-1", row)
	}
}

// TestSourcesCoverEveryRead pins the source set to the fixture files the test
// wrote, with literal paths and independently computed hashes.
func TestSourcesCoverEveryRead(t *testing.T) {
	t.Parallel()
	dir, files := fixture(t)
	layer := newLayer(dir)
	view, err := layer.Bundles(context.Background(), "")
	if err != nil {
		t.Fatalf("Bundles: %v", err)
	}
	want := map[string]string{
		".verdict/state/1234/PR.json":          files[".verdict/state/1234/PR.json"],
		".verdict/state/1234/findings.json":    files[".verdict/state/1234/findings.json"],
		".verdict/state/1234/post.json":        files[".verdict/state/1234/post.json"],
		".verdict/state/1234/full.diff":        files[".verdict/state/1234/full.diff"],
		".verdict/state/local-1/source.json":   files[".verdict/state/local-1/source.json"],
		".verdict/state/local-1/post.json":     files[".verdict/state/local-1/post.json"],
		".verdict/state/local-1/full.diff":     files[".verdict/state/local-1/full.diff"],
		".verdict/state/local-1/findings.json": files[".verdict/state/local-1/findings.json"],
	}
	got := map[string]string{}
	for _, source := range view.Sources {
		got[source.Path] = source.SHA256
		if source.Bytes != int64(len(want[source.Path])) {
			t.Errorf("source %s bytes = %d, want %d", source.Path, source.Bytes, len(want[source.Path]))
		}
	}
	if !slices.Equal(slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want))) {
		t.Fatalf("sources = %v, want exactly %v", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
	}
	assertSourceHashes(t, got, want)
	root := asString(t, view.Data.Fields["root"])
	detail, err := layer.Detail(context.Background(), root, "1234")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(detail.Sources) != 4 {
		t.Errorf("detail sources = %d, want four files", len(detail.Sources))
	}
}

// TestCacheInvalidatesOnOverrideEdit proves a policy edit under the served
// root re-composes instead of serving the entry built before it.
func TestCacheInvalidatesOnOverrideEdit(t *testing.T) {
	t.Parallel()
	dir, _ := fixture(t)
	layer := newLayer(dir)
	root := RootID(mustAbs(t, dir))
	if _, err := layer.Detail(context.Background(), root, "1234"); err != nil {
		t.Fatalf("first Detail: %v", err)
	}
	writeFiles(t, dir, map[string]string{
		".verdict/overrides/preamble.md": "### not allowed\n",
	})
	_, err := layer.Detail(context.Background(), root, "1234")
	var ref *core.RefusalError
	if !errors.As(err, &ref) || ref.Code != "bad-override" {
		t.Fatalf("Detail after the override edit = %v, want bad-override", err)
	}
}

// TestCacheStampCoversBundleInputs proves the stamp moves for an in-place edit
// of each artifact class the composition reads.
func TestCacheStampCoversBundleInputs(t *testing.T) {
	t.Parallel()
	dir, _ := fixture(t)
	root := Root{ID: RootID(mustAbs(t, dir)), Path: mustAbs(t, dir)}
	bundleDir := filepath.Join(dir, ".verdict", "state", "1234")
	writeFiles(t, dir, map[string]string{".verdict/state/1234/chunks/cost.json": "aa"})
	before := bundleStamp(root, bundleDir)
	for rel, content := range map[string]string{
		".verdict/state/1234/patch.json":       "x",
		".verdict/state/1234/panel":            "x",
		".verdict/state/1234/chunks/cost.json": "bbb",
		".verdict/overrides/preamble.md":       "x",
	} {
		writeFiles(t, dir, map[string]string{rel: content})
		after := bundleStamp(root, bundleDir)
		if after == before {
			t.Errorf("%s did not move the cache stamp", rel)
		}
		before = after
	}
}

// TestCacheDoesNotPinCancellation proves a request-scoped cancellation is not
// stored: the next request with a live context composes normally.
func TestCacheDoesNotPinCancellation(t *testing.T) {
	t.Parallel()
	dir, _ := fixture(t)
	layer := newLayer(dir)
	root := RootID(mustAbs(t, dir))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := layer.Detail(canceled, root, "1234")
	var ref *core.RefusalError
	if !errors.As(err, &ref) || ref.Code != "canceled" {
		t.Fatalf("canceled Detail = %v, want canceled", err)
	}
	if _, err := layer.Detail(context.Background(), root, "1234"); err != nil {
		t.Fatalf("Detail after a canceled request = %v, want success", err)
	}
}

// TestCacheKeepsCompositionsPerBundle proves the cap is counted in bundles,
// not compositions: an inbox and a detail for one bundle coexist.
func TestCacheKeepsCompositionsPerBundle(t *testing.T) {
	t.Parallel()
	dir, _ := fixture(t)
	layer := New(Snapshot{
		Version: "1.2.3",
		Roots:   []Root{{Path: dir}},
		Runtime: &core.Runtime{Now: fixedNow},
	}, Options{Check: "critical", MaxBundles: 1})
	ctx := context.Background()
	if _, err := layer.Bundles(ctx, ""); err != nil {
		t.Fatalf("Bundles: %v", err)
	}
	root := RootID(mustAbs(t, dir))
	if _, err := layer.Detail(ctx, root, "1234"); err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if got := layer.cacheEntries(); got != 2 {
		t.Fatalf("cache entries = %d, want 2 (inbox plus detail for one bundle)", got)
	}
	if _, err := layer.Bundles(ctx, ""); err != nil {
		t.Fatalf("Bundles again: %v", err)
	}
	if got := layer.cacheEntries(); got != 2 {
		t.Fatalf("cache entries after the second inbox = %d, want 2", got)
	}
}

// TestCacheEvictsByBundleAtTheCap proves a new bundle evicts one whole bundle
// instead of clearing every composition.
func TestCacheEvictsByBundleAtTheCap(t *testing.T) {
	t.Parallel()
	dirA, _ := fixture(t)
	dirB, _ := fixture(t)
	rootA := Root{ID: RootID(mustAbs(t, dirA)), Path: mustAbs(t, dirA)}
	rootB := Root{ID: RootID(mustAbs(t, dirB)), Path: mustAbs(t, dirB)}
	layer := New(Snapshot{
		Version: "1.2.3",
		Roots:   []Root{rootA, rootB},
		Runtime: &core.Runtime{Now: fixedNow},
	}, Options{Check: "critical", MaxBundles: 1})
	flags := inboxFlags()
	if _, ref, _, _ := layer.cached(context.Background(), rootA, "1234",
		filepath.Join(dirA, ".verdict", "state", "1234"), &flags); ref != nil {
		t.Fatalf("cache root A: %v", ref)
	}
	if _, ref, _, _ := layer.cached(context.Background(), rootB, "1234",
		filepath.Join(dirB, ".verdict", "state", "1234"), &flags); ref != nil {
		t.Fatalf("cache root B: %v", ref)
	}
	if got := layer.cacheEntries(); got != 1 {
		t.Fatalf("cache entries = %d, want 1 at a one-bundle cap", got)
	}
	for key := range layer.cache {
		if key.root != rootB.ID {
			t.Errorf("cache kept root %s, want the newest root %s", key.root, rootB.ID)
		}
	}
}

// assertSourceHashes compares every path's hash with the fixture content.
func assertSourceHashes(t *testing.T, got, want map[string]string) {
	t.Helper()
	for rel, content := range want {
		sum := sha256.Sum256([]byte(content))
		if got[rel] != hex.EncodeToString(sum[:]) {
			t.Errorf("source %s sha256 = %s, want %s", rel, got[rel], hex.EncodeToString(sum[:]))
		}
	}
}

// TestInventoryCapsAndReportsTruncation proves a bundle past the entry budget
// stops hashing, reports partial with a warning, and still ledgers the store.
func TestInventoryCapsAndReportsTruncation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	files := map[string]string{
		".verdict/state/1234/PR.json":     prDoc(),
		".verdict/state/1234/unknown.bin": "not an artifact",
		".verdict/state/1234/findings.json": "[" +
			finding(sigCrit, "open", "critical", "walked") + "]",
	}
	for i := range 70 {
		files[fmt.Sprintf(".verdict/state/1234/chunks/%02d.cost.json", i)] = "{}"
	}
	writeFiles(t, dir, files)
	layer := newLayer(dir)
	root := RootID(mustAbs(t, dir))
	view, err := layer.Detail(context.Background(), root, "1234")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(view.Sources) != 64 {
		t.Fatalf("sources = %d, want the 64-entry cap", len(view.Sources))
	}
	assertTruncatedInventory(t, view)
}

// assertTruncatedInventory pins the partial flag, warning, store row, and
// supported-artifact filter of a capped inventory.
func assertTruncatedInventory(t *testing.T, view *View) {
	t.Helper()
	data := wireMap(t, view.Data)
	if data["partial"] != true {
		t.Errorf("partial = %v, want true for a truncated inventory", data["partial"])
	}
	want := "serve: source inventory capped at 64 entries or 16 MiB under bundle 1234; sources[] is incomplete"
	if !slices.Contains(view.Warnings, want) {
		t.Errorf("warnings = %v, want %q", view.Warnings, want)
	}
	first := asMap(t, asList(t, data["artifacts"])[0])
	if first["path"] != "findings.json" || first["present"] != true {
		t.Errorf("first artifact = %v, want the findings store present", first)
	}
	for _, source := range view.Sources {
		if strings.HasSuffix(source.Path, "/unknown.bin") {
			t.Errorf("source %s is not a supported artifact", source.Path)
		}
	}
}

// TestSourceCollectorBudgets pins both cuts directly.
func TestSourceCollectorBudgets(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact")
	if err := os.WriteFile(path, []byte("0123456789A"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		maxEntries  int
		maxBytes    int64
		wantSources int
	}{
		{name: "entry cap", maxEntries: 1, maxBytes: 0, wantSources: 1},
		{name: "byte cap", maxEntries: 10, maxBytes: 10, wantSources: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertSourceBudget(t, dir, path, tc.maxEntries, tc.maxBytes, tc.wantSources)
		})
	}
}

// assertSourceBudget pins one collector cut.
func assertSourceBudget(t *testing.T, dir, path string, maxEntries int, maxBytes int64, wantSources int) {
	t.Helper()
	c := &sourceCollector{
		root: Root{Path: dir}, rootPath: dir,
		maxEntries: maxEntries, maxBytes: maxBytes,
	}
	c.add("1234", path)
	c.add("1234", path)
	if len(c.sources) != wantSources {
		t.Errorf("sources = %d, want %d", len(c.sources), wantSources)
	}
	if !c.truncated {
		t.Error("truncated = false, want true past the budget")
	}
}

// TestShowDoesNotInventoryTheBundle proves the byte-identical show route does
// not hash an inventory it cannot render.
func TestShowDoesNotInventoryTheBundle(t *testing.T) {
	t.Parallel()
	dir, _ := fixture(t)
	view, err := newLayer(dir).Show(context.Background(), RootID(mustAbs(t, dir)), "1234", sigCrit[:4])
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if len(view.Sources) != 0 {
		t.Errorf("show sources = %v, want none", view.Sources)
	}
}

// TestDetailReportDirIsRootRelative proves the served report data keeps the
// CLI's portable bundle path.
func TestDetailReportDirIsRootRelative(t *testing.T) {
	t.Parallel()
	dir, _ := fixture(t)
	layer := newLayer(dir)
	view, err := layer.Bundles(context.Background(), "")
	if err != nil {
		t.Fatalf("Bundles: %v", err)
	}
	root := asString(t, view.Data.Fields["root"])
	detail, err := layer.Detail(context.Background(), root, "1234")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	report, ok := detail.Data.Fields["report"].(*jq.Obj)
	if !ok {
		t.Fatal("data.report is not an object")
	}
	if got := report.Fields["dir"]; got != ".verdict/state/1234" {
		t.Errorf("data.report.dir = %v, want .verdict/state/1234", got)
	}
}

// TestBundleAmbiguous proves a bare name that resolves to a local sibling is
// refused instead of reading the sibling.
func TestBundleAmbiguous(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		".verdict/state/1234/findings.json": "[" +
			finding(sigCrit, "open", "critical", "walked") + "]",
		".verdict/state/local-1234/findings.json": "[" +
			finding(sigOpen, "open", "critical", "said") + "]",
	})
	layer := newLayer(dir)
	root := RootID(mustAbs(t, dir))
	_, err := layer.Detail(context.Background(), root, "1234")
	var ref *core.RefusalError
	if !errors.As(err, &ref) || ref.Code != "bundle-ambiguous" {
		t.Fatalf("Detail(1234) = %v, want bundle-ambiguous", err)
	}
	if _, err := layer.Detail(context.Background(), root, "local-1234"); err != nil {
		t.Fatalf("Detail(local-1234) = %v, want success", err)
	}
}

// guardRefusedNames are bundle directories the shared guard refuses: the
// names serve's old route regex accepted and the CLI refused.
var guardRefusedNames = []string{
	"NUL", "nul", "NuL", "nul.txt", "CON", "con.json", "PRN", "AUX",
	"COM1", "com9", "LPT1", "lpt9", "123.", "1234.", "a.", "1234..",
	"com1.txt", "1.",
}

// TestDetailRefusesNamesTheSharedGuardRefuses proves the route and the CLI
// answer one rule: every divergent name reaches the shared refusal, never a
// bundle read and never a spurious not-found.
func TestDetailRefusesNamesTheSharedGuardRefuses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot create a DOS-device or trailing-dot directory; the shared guard is tested on every OS by internal/config")
	}
	t.Parallel()
	dir := t.TempDir()
	files := map[string]string{}
	for _, name := range guardRefusedNames {
		base := ".verdict/state/" + name + "/"
		files[base+"PR.json"] = prDoc()
		files[base+"findings.json"] = "[" + finding(sigCrit, "open", "critical", "walked") + "]"
	}
	writeFiles(t, dir, files)
	layer := newLayer(dir)
	root := RootID(mustAbs(t, dir))
	for _, name := range guardRefusedNames {
		t.Run(name, func(t *testing.T) { assertGuardRefusal(t, layer, root, name) })
	}
}

// assertGuardRefusal pins the shared guard's code and line on the layer.
func assertGuardRefusal(t *testing.T, layer *Layer, root, name string) {
	t.Helper()
	_, err := layer.Detail(context.Background(), root, name)
	var ref *core.RefusalError
	if !errors.As(err, &ref) || ref.Code != "bad-flags" {
		t.Fatalf("Detail(%q) = %v, want bad-flags", name, err)
	}
	const want = "bundle name must be one path element (a PR number or fetch-local name), not a path"
	if ref.Message != want {
		t.Errorf("Detail(%q) message = %q, want %q", name, ref.Message, want)
	}
}

// TestBundleListingUsesTheSharedGuard proves a directory no verb can address
// never becomes an inbox row, while the names the old regex refused remain
// addressable.
func TestBundleListingUsesTheSharedGuard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot create a DOS-device or trailing-dot directory; the shared guard is tested on every OS by internal/config")
	}
	t.Parallel()
	dir, _ := fixture(t)
	longName := strings.Repeat("a", 140)
	names := []string{".hidden", "-x", longName, "_x"}
	files := map[string]string{}
	for _, name := range append(append([]string{}, guardRefusedNames...), names...) {
		base := ".verdict/state/" + name + "/"
		files[base+"PR.json"] = prDoc()
		files[base+"findings.json"] = "[" + finding(sigCrit, "open", "critical", "walked") + "]"
	}
	writeFiles(t, dir, files)
	view, err := newLayer(dir).Bundles(context.Background(), "")
	if err != nil {
		t.Fatalf("Bundles: %v", err)
	}
	data := wireMap(t, view.Data)
	rows := asList(t, data["rows"])
	got := make([]string, 0, len(rows))
	for _, row := range rows {
		got = append(got, asString(t, asMap(t, row)["bundle"]))
	}
	want := []string{"-x", ".hidden", "1234", "_x", longName, "local-1"}
	if !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want exactly %v", got, want)
	}
	if data["partial"] != false {
		t.Errorf("partial = %v, want false: a refused directory is not a bundle", data["partial"])
	}
	if errs := asList(t, data["errors"]); len(errs) != 0 {
		t.Errorf("errors = %v, want none", errs)
	}
}

// TestSharedGuardNamesAreAddressable proves the other direction: names the old
// regex refused resolve through the CLI's own guard.
func TestSharedGuardNamesAreAddressable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	longName := strings.Repeat("a", 140)
	names := []string{".hidden", "-x", longName, "_x"}
	files := map[string]string{}
	for _, name := range names {
		base := ".verdict/state/" + name + "/"
		files[base+"PR.json"] = prDoc()
		files[base+"findings.json"] = "[" + finding(sigCrit, "open", "critical", "walked") + "]"
	}
	writeFiles(t, dir, files)
	layer := newLayer(dir)
	root := RootID(mustAbs(t, dir))
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			if _, err := layer.Detail(context.Background(), root, name); err != nil {
				t.Fatalf("Detail(%q) = %v, want success", name, err)
			}
		})
	}
}

// mustAbs makes dir absolute the way New does.
func mustAbs(t *testing.T, dir string) string {
	t.Helper()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// TestBundleRepoKeepsGitLabSubgroups proves the envelope names the full
// project path on every host, not the first two URL segments.
func TestBundleRepoKeepsGitLabSubgroups(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, url, want string }{
		{
			name: "gitlab subgroup",
			url:  "https://gitlab.example.com/team/subgroup/project/-/merge_requests/9",
			want: "team/subgroup/project",
		},
		{
			name: "gitlab plain group",
			url:  "https://gitlab.example.com/team/project/-/merge_requests/9",
			want: "team/project",
		},
		{name: "github pull", url: "https://github.com/acme/app/pull/1234", want: "acme/app"},
		{name: "forgejo pulls", url: "https://codeberg.org/acme/app/pulls/9", want: "acme/app"},
		{name: "github issue", url: "https://github.com/acme/app/issues/9", want: "acme/app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFiles(t, dir, map[string]string{
				".verdict/state/1234/PR.json": `{"state":"OPEN","headRefOid":"` + headA +
					`","baseRefOid":"` + headB + `","author":{"login":"teammate"},"url":"` + tc.url + `"}`,
			})
			if got := bundleRepo(filepath.Join(dir, ".verdict", "state", "1234")); got != tc.want {
				t.Errorf("bundleRepo = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDetailRefusesSymlinkedThreadFeed proves a feed symlink reads as absence,
// the same rule the inventory applies.
func TestDetailRefusesSymlinkedThreadFeed(t *testing.T) {
	t.Parallel()
	dir, _ := fixture(t)
	target := filepath.Join(t.TempDir(), "threads.json")
	feed := `{"schema_version":"1","provider":"github","pr":1234,"head_sha":"` + headA + `",` +
		`"fetched_at":"2026-01-01T00:00:00Z","truncated":false,"threads":[]}`
	if err := os.WriteFile(target, []byte(feed), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, ".verdict", "state", "1234", "threads.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	view, err := newLayer(dir).Detail(context.Background(), RootID(mustAbs(t, dir)), "1234")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	data := wireMap(t, view.Data)
	if data["threads"] != nil {
		t.Errorf("threads = %v, want null for a symlinked feed", data["threads"])
	}
	if data["partial"] != false {
		t.Errorf("partial = %v, want false (absence, not an error)", data["partial"])
	}
}

// TestOverridesDirSeam proves the composed rRun judges the served root's
// override fragments, not the launch directory's.
func TestOverridesDirSeam(t *testing.T) {
	dir, _ := fixture(t)
	writeFiles(t, dir, map[string]string{
		".verdict/overrides/preamble.md": "### not allowed\n",
	})
	layer := newLayer(dir)
	root := RootID(mustAbs(t, dir))
	_, err := layer.Detail(context.Background(), root, "1234")
	var ref *core.RefusalError
	if !errors.As(err, &ref) || ref.Code != "bad-override" {
		t.Fatalf("Detail with a bad root override = %v, want bad-override", err)
	}
}

// TestRootIDNormalizesWindowsSpellings pins one identity across the spellings
// Windows hands a path and two distinct identities for two roots.
func TestRootIDNormalizesWindowsSpellings(t *testing.T) {
	t.Parallel()
	const want = "b23cb163a14d"
	if got := RootID(`C:\work`); got != want {
		t.Errorf("RootID(C:\\work) = %q, want %q", got, want)
	}
	if got := RootID("c:/work/"); got != want {
		t.Errorf("RootID(c:/work/) = %q, want %q", got, want)
	}
	if RootID("/work/a") == RootID("/work/b") {
		t.Error("two different roots share an identity")
	}
}

// TestSourceScanRefusesRawReads proves internal/core/query never opens an
// artifact outside the capped reader.
func TestSourceScanRefusesRawReads(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	for _, entry := range entries {
		if !scannableFile(entry) {
			continue
		}
		scanned++
		scanForRawReads(t, entry.Name())
	}
	if scanned == 0 {
		t.Fatal("scanned no production files; the guard would pass vacuously")
	}
}

// scannableFile reports whether one entry is a production Go file.
func scannableFile(entry os.DirEntry) bool {
	name := entry.Name()
	return !entry.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
}

// scanForRawReads fails on one production file's raw artifact read.
func scanForRawReads(t *testing.T, name string) {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"os.ReadFile(", "os.Open("} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("%s calls %s; every artifact read goes through the capped trace reader", name, forbidden)
		}
	}
}

// TestBundleFilterIsPortable proves the listing filter shares the config
// glob gate.
func TestBundleFilterIsPortable(t *testing.T) {
	t.Parallel()
	if ValidBundleGlob("../..") || ValidBundleGlob("/abs") || ValidBundleGlob("a//b") {
		t.Error("an escaping glob reads as portable")
	}
	if !ValidBundleGlob("local-*") || !BundleMatches("local-*", "local-1") {
		t.Error("a portable glob does not match its bundle")
	}
	if BundleMatches("local-*", "1234") {
		t.Error("a local glob matched a PR bundle")
	}
}
