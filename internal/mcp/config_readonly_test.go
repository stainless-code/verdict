package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
)

// configCapture writes the run bundle the config tool explains: a genuine
// resolution of a repository default and a contradicting vendored scope over
// two reviewed paths, persisted with the controls the explain path requires.
func configCapture(t *testing.T) {
	t.Helper()
	repo, ref := config.Parse([]byte(`{"schema_version":"1",
		"defaults":{"effort":"standard","display_min_severity":"important"},
		"scopes":[{"name":"vendored","include":["vendor/**"],
		"defaults":{"effort":"deep","display_min_severity":"critical"}}]}`))
	if ref != nil {
		t.Fatalf("Parse: %v", ref)
	}
	in := &config.Inputs{
		Repo:  repo,
		Files: []config.FileInput{{Path: "vendor/lib.go"}, {Path: "app/main.go"}},
	}
	resolved, rerr := config.Resolve(in)
	if rerr != nil {
		t.Fatalf("Resolve: %v", rerr)
	}
	bundle := filepath.Join(".verdict", "state", "5101")
	rs := &config.RunState{
		Name:       "5101",
		BaseSHA:    "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Trusted:    true,
		CapturedAt: "2026-11-06T00:00:00Z",
		Evidence:   config.Capture(in, resolved, nil, "2026-11-06T00:00:00Z"),
		Resolved:   resolved,
	}
	if err := config.WriteRunState(bundle, rs); err != nil {
		t.Fatalf("WriteRunState: %v", err)
	}
	writeFile(t, ".verdict/config.json", `{"schema_version":"1","defaults":{"effort":"standard"}}`)
	if _, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: bundle, Root: ".", BaseSHA: rs.BaseSHA, AllowHead: true,
	}); ref != nil {
		t.Fatalf("controls.Resolve: %v", ref)
	}
}

// configCall drives one real tools/call frame through the server and returns
// the call result's structuredContent.
func configCall(t *testing.T, args string) map[string]any {
	t.Helper()
	cfg := testConfig()
	frames := serve(t, &cfg,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"config","arguments":`+args+`}}`,
	)
	f := frames["1"]
	if f.Error != nil {
		t.Fatalf("protocol error: %+v", *f.Error)
	}
	var result callToolResult
	if err := json.Unmarshal(f.Result, &result); err != nil {
		t.Fatalf("decode result: %v (%s)", err, f.Result)
	}
	if result.IsError {
		t.Fatalf("config call failed: %s", result.Content[0].Text)
	}
	var data map[string]any
	if err := json.Unmarshal(result.StructuredContent, &data); err != nil {
		t.Fatalf("decode structuredContent: %v", err)
	}
	return data
}

// treeDigest fingerprints every file under root, so a read-only assertion can
// prove the call wrote nothing at all.
func treeDigest(t *testing.T) string {
	t.Helper()
	var paths []string
	if err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		paths = append(paths, path)
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	sort.Strings(paths)
	sum := sha256.New()
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		inner := sha256.Sum256(raw)
		sum.Write([]byte(path))
		sum.Write(inner[:])
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// TestConfigExplainReportsCaptureAndWritesNothing drives the actual binder: the
// explained files and shadowed values come from the saved capture, survive a
// changed environment and working configuration, and the call leaves the tree
// byte-identical.
func TestConfigExplainReportsCaptureAndWritesNothing(t *testing.T) {
	fixture(t)
	configCapture(t)
	before := treeDigest(t)
	data := configCall(t, `{"repo":"acme/app","pr":"5101","flags":{"explain":true}}`)
	assertCapturedFiles(t, data)
	if after := treeDigest(t); after != before {
		t.Fatalf("explain wrote to the tree: %s -> %s", before, after)
	}

	writeFile(t, ".verdict/config.json", `{"schema_version":"1","defaults":{"effort":"cheap"}}`)
	t.Setenv("REVIEW_EFFORT", "cheap")
	drifted := treeDigest(t)
	data = configCall(t, `{"repo":"acme/app","pr":"5101","flags":{"explain":true}}`)
	assertCapturedFiles(t, data)
	if after := treeDigest(t); after != drifted {
		t.Fatalf("explain wrote to the tree after drift: %s -> %s", drifted, after)
	}
}

// assertCapturedFiles checks the reported values, layers, and shadows against
// independent literals: the vendored file's scope beats the repository default.
func assertCapturedFiles(t *testing.T, data map[string]any) {
	t.Helper()
	vendored, ordinary := configFiles(t, data)
	assertFileEffort(t, vendored, "deep", "scope")
	assertFileEffort(t, ordinary, "standard", "repo")
	assertVendoredShadow(t, vendored)
	assertRootShadow(t, data)
}

// configFiles returns the two reported files in captured review order.
func configFiles(t *testing.T, data map[string]any) (map[string]any, map[string]any) {
	t.Helper()
	files, ok := data["files"].([]any)
	if !ok || len(files) != 2 {
		t.Fatalf("files = %v; want the two reviewed paths", data["files"])
	}
	vendored, ok := files[0].(map[string]any)
	if !ok || vendored["path"] != "vendor/lib.go" {
		t.Fatalf("first file = %v; want vendor/lib.go", files[0])
	}
	ordinary, ok := files[1].(map[string]any)
	if !ok || ordinary["path"] != "app/main.go" {
		t.Fatalf("second file = %v; want app/main.go", files[1])
	}
	return vendored, ordinary
}

// assertVendoredShadow checks the scoped file reports the repository value its
// scope overrode.
func assertVendoredShadow(t *testing.T, vendored map[string]any) {
	t.Helper()
	shadowed, ok := vendored["shadowed"].([]any)
	if !ok || len(shadowed) == 0 {
		t.Fatalf("vendored shadowed = %v; want the losing repository value", vendored["shadowed"])
	}
	shadow, ok := shadowed[0].(map[string]any)
	if !ok || shadow["key"] != "effort" || shadow["value"] != "standard" || shadow["layer"] != "repo" {
		t.Fatalf("vendored shadow = %v; want repo effort standard", shadowed[0])
	}
}

// assertRootShadow checks the root defaults report their captured losers.
func assertRootShadow(t *testing.T, data map[string]any) {
	t.Helper()
	root, ok := data["shadowed"].([]any)
	if !ok || len(root) == 0 {
		t.Fatalf("root shadowed = %v; want the captured losing values", data["shadowed"])
	}
}

// assertFileEffort checks one file reports the wanted key value and layer.
func assertFileEffort(t *testing.T, file map[string]any, want, layer string) {
	t.Helper()
	value, ok := fileValue(t, file, "effort")
	if !ok {
		t.Fatalf("%v reports no effort value", file["path"])
	}
	if value["value"] != want || value["layer"] != layer {
		t.Fatalf("%v effort = %v; want %s from the %s layer", file["path"], value, want, layer)
	}
}

// fileValue finds one reported key in a file's values.
func fileValue(t *testing.T, file map[string]any, key string) (map[string]any, bool) {
	t.Helper()
	values, ok := file["values"].([]any)
	if !ok {
		t.Fatalf("%v reports no values", file["path"])
	}
	for _, v := range values {
		if value, ok := v.(map[string]any); ok && value["key"] == key {
			return value, true
		}
	}
	return nil, false
}

// TestConfigPreviewReportsProposedFileAndWritesNothing drives the binder's
// preview path: the proposed values appear as a prospective untrusted read and
// nothing on disk changes.
func TestConfigPreviewReportsProposedFileAndWritesNothing(t *testing.T) {
	fixture(t)
	configCapture(t)
	proposed := filepath.Join(t.TempDir(), "proposed.json")
	body := `{"schema_version":"1","defaults":{"effort":"cheap","budget_usd":1.0}}`
	if err := os.WriteFile(proposed, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	before := treeDigest(t)
	data := configCall(t, `{"repo":"acme/app","pr":"5101","flags":{"preview":true,"file":`+
		quoteJSON(t, proposed)+`}}`)
	if data["mode"] != "preview" || data["trusted"] != false {
		t.Fatalf("preview = %v/%v; want an untrusted preview", data["mode"], data["trusted"])
	}
	assertPreviewEffort(t, data)
	if after := treeDigest(t); after != before {
		t.Fatalf("preview wrote to the tree: %s -> %s", before, after)
	}
}

// assertPreviewEffort checks the prospective values carry the proposed effort.
func assertPreviewEffort(t *testing.T, data map[string]any) {
	t.Helper()
	values, ok := data["values"].([]any)
	if !ok {
		t.Fatalf("preview reports no values")
	}
	value, ok := previewValue(values, "effort")
	if !ok {
		t.Fatalf("preview values = %v; want the proposed cheap effort", values)
	}
	if value["value"] != "cheap" {
		t.Fatalf("preview effort = %v; want the proposed cheap", value)
	}
}

// previewValue finds one reported key in the preview's flat values.
func previewValue(values []any, key string) (map[string]any, bool) {
	for _, v := range values {
		if value, ok := v.(map[string]any); ok && value["key"] == key {
			return value, true
		}
	}
	return nil, false
}

// quoteJSON renders one string as a JSON literal for an arguments payload.
func quoteJSON(t *testing.T, s string) string {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal %q: %v", s, err)
	}
	return string(raw)
}
