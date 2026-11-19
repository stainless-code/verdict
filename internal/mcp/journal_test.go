package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	journalcore "github.com/stainless-code/verdict/internal/core/journal"
)

func TestMCPJournalsWritingToolsAndSkipsReadOnly(t *testing.T) {
	fixture(t)
	bundleDir := filepath.Join(".verdict", "state", "5101")
	writeFile(t, filepath.Join(bundleDir, "full.diff"), "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n")
	writeFile(t, filepath.Join(bundleDir, "hunks.tsv"), "a.go\t1\t1\t1\t1\n")
	writeFile(t, filepath.Join(bundleDir, "review.json"),
		`{"schema_version":"1","findings":[],"spec_checks":[],"receipts":[],"summary":"clean"}`)

	cfg := testConfig()
	frames := serve(t, &cfg,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"report","arguments":{"repo":"acme/app","pr":"5101"}}}`,
	)
	var res callToolResult
	if err := json.Unmarshal(frames["1"].Result, &res); err != nil {
		t.Fatalf("decode report result: %v", err)
	}
	if res.IsError {
		t.Fatalf("report returned error: %+v", res)
	}
	assertMCPStepRecorded(t, bundleDir)
	assertReadOnlyMCPUnchanged(t, &cfg, bundleDir)
}

func assertMCPStepRecorded(t *testing.T, bundleDir string) {
	t.Helper()
	sum, err := journalcore.Read(bundleDir, "5101")
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	if len(sum.Steps) != 1 {
		t.Fatalf("len(Steps) = %d, want 1", len(sum.Steps))
	}
	st := sum.Steps[0]
	if st.Verb != "report" || st.Channel != journalcore.ChannelMCP || st.Version != "1.2.3" || st.Exit != 0 {
		t.Fatalf("MCP step = %+v, want verb=report channel=mcp version=1.2.3 exit=0", st)
	}
}

func assertReadOnlyMCPUnchanged(t *testing.T, cfg *Config, bundleDir string) {
	t.Helper()
	jpath := filepath.Join(bundleDir, journalcore.JournalFile)
	before, err := os.ReadFile(jpath)
	if err != nil {
		t.Fatal(err)
	}
	_ = serve(t, cfg,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"show","arguments":{"repo":"acme/app","pr":"5101","sig":"deadbeef"}}}`,
	)
	after, err := os.ReadFile(jpath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("read-only MCP tool `show` mutated journal.jsonl")
	}
}

func TestMCPJournalAppendFailureWarnsAndSucceeds(t *testing.T) {
	fixture(t)
	bundleDir := filepath.Join(".verdict", "state", "5101")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(bundleDir, journalcore.JournalFile)); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cfg := testConfig()
	cfg.Stderr = &stderr
	frames := serve(t, &cfg,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"report","arguments":{"repo":"acme/app","pr":"5101"}}}`,
	)
	var res callToolResult
	if err := json.Unmarshal(frames["1"].Result, &res); err != nil {
		t.Fatalf("decode report result: %v", err)
	}
	if res.IsError {
		t.Fatalf("report failed despite journal error being non-fatal: %+v", res)
	}
	if !strings.Contains(stderr.String(), "warn: journal: could not append to ") {
		t.Fatalf("stderr = %q, want frozen journal warning", stderr.String())
	}
}

func TestReqArgvFormatsFlagsDeterministically(t *testing.T) {
	t.Parallel()
	got := reqArgv("record", json.RawMessage(`{"repo":"acme/app","pr":"local-7","dry_run":true,"requireCoverage":false,"token":"shh"}`))
	want := []string{"record", "acme/app", "local-7", "--dry-run", "--token", "shh"}
	if !slices.Equal(got, want) {
		t.Fatalf("reqArgv = %q, want %q", got, want)
	}
}
