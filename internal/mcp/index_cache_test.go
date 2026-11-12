package mcp

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/testenv"
)

// gitWaitDelay bounds the git commands these fixtures capture pipes from
// (GO-CONC-06): a descendant that keeps the pipe open costs a wait, not a hung
// test.
const gitWaitDelay = time.Minute

// TestIndexMCPNeverWritesCoChangeCache proves a co-changed call that misses the
// cache computes its rows and leaves the cache byte-identical: no new file and
// no prune.
func TestIndexMCPNeverWritesCoChangeCache(t *testing.T) {
	dir := coChangeRepo(t)
	cacheDir := filepath.Join(dir, ".verdict", "index", "cochange")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"old-a.json", "old-b.json", "old-c.json"} {
		if err := os.WriteFile(filepath.Join(cacheDir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	before := snapshotCache(t, cacheDir)

	t.Chdir(dir)
	cfg := testConfig()
	frames := serve(t, &cfg, coChangeCall(1))

	assertPartner(t, callIndex(t, frames["1"]))
	if after := snapshotCache(t, cacheDir); !maps.Equal(before, after) {
		t.Fatalf("cache after a read-only query:\nbefore: %q\nafter:  %q", before, after)
	}
}

// TestIndexMCPReadsCoChangeCache proves a valid cached window is still read
// without writing or pruning it.
func TestIndexMCPReadsCoChangeCache(t *testing.T) {
	dir := coChangeRepo(t)
	cacheDir := filepath.Join(dir, ".verdict", "index", "cochange")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	head := gitOut(t, dir, "rev-parse", "HEAD")
	valid := `{"schema_version":"2","head":"` + head + `","since":"","commits":2000,"changes":[` +
		`{"commit":"` + strings.Repeat("a", 40) + `","paths":["a/target.go","z/cached.go"]},` +
		`{"commit":"` + strings.Repeat("b", 40) + `","paths":["a/target.go","z/cached.go"]}]}`
	if err := os.WriteFile(filepath.Join(cacheDir, head+"-2000.json"), []byte(valid), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshotCache(t, cacheDir)

	t.Chdir(dir)
	cfg := testConfig()
	frames := serve(t, &cfg, coChangeCall(1))

	data := callIndex(t, frames["1"])
	rows, ok := data["rows"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("rows = %v; want the one cached partner", data["rows"])
	}
	row, ok := rows[0].(map[string]any)
	if !ok || row["path"] != "z/cached.go" || row["commits"] != float64(2) {
		t.Errorf("row = %v; want z/cached.go with 2 commits from the cache", rows[0])
	}
	if after := snapshotCache(t, cacheDir); !maps.Equal(before, after) {
		t.Fatalf("cache after a cached read:\nbefore: %q\nafter:  %q", before, after)
	}
}

// coChangeCall builds one tools/call frame for index co-changed.
func coChangeCall(id int) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call",`+
		`"params":{"name":"index","arguments":{"query":"co-changed","path":"a/target.go"}}}`, id)
}

// callIndex decodes one index tool result and requires success.
func callIndex(t *testing.T, f frame) map[string]any {
	t.Helper()
	var result callToolResult
	if err := json.Unmarshal(f.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("index refused: %s", result.Content[0].Text)
	}
	var data map[string]any
	if err := json.Unmarshal(result.StructuredContent, &data); err != nil {
		t.Fatal(err)
	}
	return data
}

// assertPartner requires the git-derived partner row for a/target.go.
func assertPartner(t *testing.T, data map[string]any) {
	t.Helper()
	rows, ok := data["rows"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("rows = %v; want one partner", data["rows"])
	}
	row, ok := rows[0].(map[string]any)
	if !ok || row["path"] != "b/partner.go" || row["commits"] != float64(2) || row["jaccard"] != float64(1) {
		t.Errorf("row = %v; want b/partner.go with 2 commits and jaccard 1", rows[0])
	}
}

// coChangeRepo creates one repository whose target and partner move together
// twice, so the second commit reaches the default --min-count.
func coChangeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		cmd.Env = testenv.Base(nil)
		cmd.WaitDelay = gitWaitDelay
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	write("a/target.go", "package a\n\nfunc Target() {}\n")
	write("b/partner.go", "package b\n\nfunc B() {}\n")
	git("add", "-A")
	git("commit", "-qm", "one")
	write("a/target.go", "package a\n\nfunc Target() {}\n\nfunc Second() {}\n")
	write("b/partner.go", "package b\n\nfunc B() {}\n\nfunc B2() {}\n")
	git("add", "-A")
	git("commit", "-qm", "two")
	return dir
}

// gitOut runs one git command in dir and returns its trimmed stdout.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = testenv.Base(nil)
	cmd.WaitDelay = gitWaitDelay
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// snapshotCache reads every cache file keyed by name.
func snapshotCache(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(raw)
	}
	return out
}
