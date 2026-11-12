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
func prDoc(head string) string {
	return fmt.Sprintf(`{"state":"OPEN","headRefOid":%q,"baseRefOid":%q,`+
		`"author":{"login":"teammate"},"url":"https://github.com/acme/app/pull/1234"}`,
		head, headB)
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
		".verdict/state/1234/PR.json":        prDoc(headA),
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

// mustAbs makes dir absolute the way New does.
func mustAbs(t *testing.T, dir string) string {
	t.Helper()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return abs
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
