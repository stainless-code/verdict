package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// jsonEscapeDirs are the trees whose non-test code may write JSON a consumer
// reads. bench/ is the benchmark harness, not a verdict surface, and stays out.
var jsonEscapeDirs = []string{"cmd", "internal"}

// jsonEscapeHome is the package that owns the rule: its renderers and
// jq.Marshal / jq.MarshalIndent / jq.EscapeControls apply it, so it may call
// encoding/json directly.
const jsonEscapeHome = "internal/jq"

// jsonEscapeFrameWriters are the JSON-RPC frame writers. Each marshals with
// encoding/json (HTML escaping off, so the text block and structuredContent
// keep the same bytes) and must pass the bytes through jq.EscapeControls
// before writing them.
var jsonEscapeFrameWriters = map[string]int{
	"internal/mcp/server.go": 2, // writeFrame's encoder; refusalResult's structured payload rides the frame
	"internal/acp/server.go": 1,
	"internal/lsp/rpc.go":    1,
}

// jsonEscapeInternalUses counts the direct encoding/json encoder calls per
// file whose output no consumer reads: a hash or comparison key, a round trip
// back into a struct, verdict's own cache, a model prompt, a help line. Every
// other write goes through jq so C1 and bidi controls never travel raw.
// A new entry needs a reason a reviewer can check.
var jsonEscapeInternalUses = map[string]int{
	"internal/core/stores.go":                 1, // round trip into Verification
	"internal/core/panel/execution.go":        1, // round trip into CodeQuote
	"internal/core/panel/executioncompare.go": 1, // comparison key
	"internal/core/corpus/metrics.go":         1, // round trip into an ordered doc
	"internal/core/recipe/plan.go":            1, // hashed, never written
	"internal/core/reviewdoc/review.go":       1, // round trip into a value
	"internal/core/index/store.go":            4, // verdict's own index cache and its size accounting
	"internal/core/index/cochange.go":         1, // verdict's own co-change cache
	"internal/core/repair/repair.go":          2, // repair feedback fed to the model, size-capped
	"internal/config/runstate.go":             1, // hashed, never written
	"internal/readcommon/phasecost.go":        1, // round trip into the spend decoder
	"internal/spend/spend.go":                 1, // round trip into the spend decoder
	"internal/schemacheck/keywords.go":        1, // message text in a schema failure
	"internal/packtest/test.go":               1, // fixture fed back into verdict
	"internal/golden/harness.go":              1, // test-harness fingerprint, not product output
	"internal/cli/cli.go":                     1, // one help line for a human
}

// TestJSONWritersApplyTheControlEscape fails when non-test code outside
// internal/jq calls an encoding/json encoder (Marshal, MarshalIndent,
// NewEncoder) that is neither a frame writer applying jq.EscapeControls nor
// an allowlisted internal use, so a new output writer cannot put C1 or bidi
// controls on the wire raw. It is a syntactic guard: an encoder reached
// through another function value is out of its reach, and review covers that.
// So is hand-built JSON (%q, [strconv.Quote], concatenation).
func TestJSONWritersApplyTheControlEscape(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	if bad := unescapedJSONWriters(t, root); len(bad) > 0 {
		t.Errorf("encoding/json encoders outside %s (use jq.Marshal, jq.MarshalIndent, or a jq renderer; "+
			"frame writers wrap jq.EscapeControls; internal-only uses join jsonEscapeInternalUses with a reason):\n%s",
			jsonEscapeHome, strings.Join(bad, "\n"))
	}
}

// TestJSONEscapeGuardFires proves the guard flags a direct encoder call and a
// frame writer without the escape, and lets an allowlisted count, a frame
// writer with the escape, and a jq call through.
func TestJSONEscapeGuardFires(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write := func(rel, src string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("internal/fixture/raw.go", `package fixture
import "encoding/json"
func f(v any) { _, _ = json.Marshal(v); _, _ = json.MarshalIndent(v, "", " "); _ = json.NewEncoder(nil) }
`)
	write("internal/fixture/clean.go", `package fixture
import "github.com/stainless-code/verdict/internal/jq"
func f(v any) { _, _ = jq.Marshal(v); _ = jq.EscapeControls(nil) }
`)
	write("internal/mcp/server.go", `package mcp
import "encoding/json"
func f(v any) { _ = json.NewEncoder(nil); _, _ = json.Marshal(v) }
`)
	write("internal/acp/server.go", `package acp
import (
	"encoding/json"
	"github.com/stainless-code/verdict/internal/jq"
)
func f() { _ = jq.EscapeControls(nil); _ = json.NewEncoder(nil) }
`)
	write("internal/core/stores.go", `package core
import "encoding/json"
func f(v any) { _, _ = json.Marshal(v) }
`)
	got := strings.Join(unescapedJSONWriters(t, root), "\n")
	want := "internal/fixture/raw.go: 3 encoding/json encoder call(s), 0 allowed\n" +
		"internal/mcp/server.go: frame writer never calls jq.EscapeControls"
	if got != want {
		t.Fatalf("guard reported:\n%s\nwant:\n%s", got, want)
	}
}

// unescapedJSONWriters lists the files breaking the rule, one line each.
func unescapedJSONWriters(t *testing.T, root string) []string {
	t.Helper()
	var bad []string
	for _, path := range jsonEscapeSources(t, root) {
		name := filepath.ToSlash(rel(root, path))
		bad = append(bad, jsonWriterFindings(t, path, name)...)
	}
	sort.Strings(bad)
	return bad
}

// jsonEscapeSources lists the non-test Go files under jsonEscapeDirs outside
// the package that owns the rule.
func jsonEscapeSources(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	for _, dir := range jsonEscapeDirs {
		if _, err := os.Stat(filepath.Join(root, dir)); err != nil {
			continue
		}
		if err := filepath.WalkDir(filepath.Join(root, dir), jsonEscapeWalker(root, &paths)); err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	return paths
}

// jsonEscapeWalker appends each source file the walk visits to paths.
func jsonEscapeWalker(root string, paths *[]string) fs.WalkDirFunc {
	return func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && d.Name() == "testdata":
			return filepath.SkipDir
		case jsonEscapeSource(root, path, d):
			*paths = append(*paths, path)
		}
		return nil
	}
}

// jsonEscapeSource reports whether the entry is a non-test Go file outside
// the package that owns the rule.
func jsonEscapeSource(root, path string, d fs.DirEntry) bool {
	if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
		return false
	}
	return !strings.HasPrefix(filepath.ToSlash(rel(root, path)), jsonEscapeHome+"/")
}

// jsonWriterFindings reports one file against the allowlists.
func jsonWriterFindings(t *testing.T, path, name string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	local := jsonImportName(file)
	if local == "" {
		return nil
	}
	calls, escapes := countJSONCalls(file, local)
	allowed, frame := jsonEscapeFrameWriters[name]
	if !frame {
		allowed = jsonEscapeInternalUses[name]
	}
	var out []string
	if frame && escapes == 0 {
		out = append(out, name+": frame writer never calls jq.EscapeControls")
	}
	if calls > allowed {
		out = append(out, fmt.Sprintf("%s: %d encoding/json encoder call(s), %d allowed", name, calls, allowed))
	}
	return out
}

// jsonImportName is the local name file gives encoding/json, or "".
func jsonImportName(file *ast.File) string {
	for _, imp := range file.Imports {
		if p, err := strconv.Unquote(imp.Path.Value); err != nil || p != "encoding/json" {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return "json"
	}
	return ""
}

// countJSONCalls counts the encoder calls through the json import and the
// jq.EscapeControls calls in file.
func countJSONCalls(file *ast.File, local string) (calls, escapes int) {
	encoders := map[string]bool{"Marshal": true, "MarshalIndent": true, "NewEncoder": true}
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		switch {
		case !ok:
		case pkg.Name == local && encoders[sel.Sel.Name]:
			calls++
		case pkg.Name == "jq" && sel.Sel.Name == "EscapeControls":
			escapes++
		}
		return true
	})
	return calls, escapes
}
