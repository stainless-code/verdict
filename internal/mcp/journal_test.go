package mcp

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/argvshape"
	journalcore "github.com/stainless-code/verdict/internal/core/journal"
)

func journalTestConfig(t *testing.T) (Config, string) {
	t.Helper()
	stateDir := t.TempDir()
	cfg := testConfig()
	cfg.Env = map[string]string{"XDG_STATE_HOME": stateDir}
	return cfg, stateDir
}

func mustMCPJournalPath(t *testing.T, stateDir, bundleName string) string {
	t.Helper()
	jpath, _, ok := journalcore.Path(".", bundleName, map[string]string{"XDG_STATE_HOME": stateDir})
	if !ok {
		t.Fatalf("journal.Path(., %q) returned ok=false", bundleName)
	}
	return jpath
}

func TestMCPJournalsWritingToolsAndSkipsReadOnly(t *testing.T) {
	fixture(t)
	bundleDir := filepath.Join(".verdict", "state", "5101")
	writeFile(t, filepath.Join(bundleDir, "full.diff"), "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n")
	writeFile(t, filepath.Join(bundleDir, "hunks.tsv"), "a.go\t1\t1\t1\t1\n")
	writeFile(t, filepath.Join(bundleDir, "review.json"),
		`{"schema_version":"1","findings":[],"spec_checks":[],"receipts":[],"summary":"clean"}`)

	cfg, stateDir := journalTestConfig(t)
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
	jpath := mustMCPJournalPath(t, stateDir, "5101")
	assertMCPStepRecorded(t, jpath, bundleDir)
	assertReadOnlyMCPUnchanged(t, &cfg, jpath)
}

func assertMCPStepRecorded(t *testing.T, jpath, bundleDir string) {
	t.Helper()
	for _, name := range []string{"journal.jsonl", "journal.jsonl.lock"} {
		if _, err := os.Stat(filepath.Join(bundleDir, name)); err == nil {
			t.Fatalf("found unexpected %s inside bundle dir", name)
		}
	}
	sum, err := journalcore.Read(jpath, "5101")
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

func assertReadOnlyMCPUnchanged(t *testing.T, cfg *Config, jpath string) {
	t.Helper()
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
		t.Fatal("read-only MCP tool `show` mutated external journal")
	}
}

func TestMCPJournalAppendFailureWarnsAndSucceeds(t *testing.T) {
	fixture(t)
	cfg, stateDir := journalTestConfig(t)
	jpath := mustMCPJournalPath(t, stateDir, "5101")
	if err := os.MkdirAll(filepath.Dir(jpath), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, jpath); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
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

// TestReqArgvFlattensNestedFlagsForRedaction proves a value placed in the nested
// `flags` object reaches the shared redactor as its own operand: an environment
// credential carried in flags.summary is hidden, and a plain summary survives.
func TestReqArgvFlattensNestedFlagsForRedaction(t *testing.T) {
	t.Parallel()
	const secret = "env-secret-12345"
	argv := reqArgv("report", json.RawMessage(`{"repo":"acme/app","pr":"local-7","flags":{"summary":"`+secret+`"}}`))
	wantArgv := []string{"report", "acme/app", "local-7", "--summary", secret}
	if !slices.Equal(argv, wantArgv) {
		t.Fatalf("reqArgv = %q, want %q", argv, wantArgv)
	}
	shape, secrets := argvshape.Record(argv, "", []string{"ANTHROPIC_API_KEY=" + secret})
	wantShape := []string{"report", "acme/app", "local-7", "--summary", "***REDACTED***"}
	if !slices.Equal(shape, wantShape) {
		t.Fatalf("Record = %q, want %q", shape, wantShape)
	}
	if !slices.Equal(secrets, []string{secret}) {
		t.Fatalf("secrets = %q, want the flags.summary value", secrets)
	}
	plain := reqArgv("report", json.RawMessage(`{"repo":"acme/app","pr":"local-7","flags":{"summary":"plain summary"}}`))
	if !slices.Equal(plain, []string{"report", "acme/app", "local-7", "--summary", "plain summary"}) {
		t.Fatalf("reqArgv = %q, want the readable summary kept", plain)
	}
}

// TestCappedJournalResultKeepsTruncationAndTruncatedFlag proves the response cap
// leaves the journal summary's own truncation object alone: an oversized journal
// result carries both `truncation` and the cap's `truncated: true`.
func TestCappedJournalResultKeepsTruncationAndTruncatedFlag(t *testing.T) {
	t.Parallel()
	summary := &journalcore.Summary{
		Bundle: "5101",
		Steps: []journalcore.Step{{
			Step: "2026-11-19T10:00:00Z-1-1", Verb: "record", Channel: journalcore.ChannelMCP,
			Argv: []string{"record", strings.Repeat("x", maxResultTextBytes)},
		}},
	}
	res := successResult("journal", summary.Wire())
	if len(res.Content[0].Text) >= maxResultTextBytes {
		t.Fatalf("text block = %d bytes, want under the %d budget", len(res.Content[0].Text), maxResultTextBytes)
	}
	var content map[string]json.RawMessage
	if err := json.Unmarshal(res.StructuredContent, &content); err != nil {
		t.Fatalf("structuredContent: %v", err)
	}
	if got := string(content["truncated"]); got != "true" {
		t.Fatalf("truncated = %s, want the response-cap boolean", got)
	}
	var truncation map[string]json.RawMessage
	if err := json.Unmarshal(content["truncation"], &truncation); err != nil {
		t.Fatalf("truncation = %s, want the journal summary object: %v", content["truncation"], err)
	}
	for _, key := range []string{"dropped", "at", "kept_from"} {
		if _, ok := truncation[key]; !ok {
			t.Errorf("truncation object missing %q: %s", key, content["truncation"])
		}
	}
}

// TestMCPJournalToolIsReadOnly proves the `journal` MCP tool is exposed under
// --read-only with ReadOnlyHint=true, reads the external journal cleanly, and
// writes no journal lines of its own.
func TestMCPJournalToolIsReadOnly(t *testing.T) {
	fixture(t)
	list := readOnlyList(t)
	desc := toolNamed(t, list, "journal")
	if !desc.Annotations.ReadOnlyHint || !desc.Annotations.IdempotentHint {
		t.Fatalf("journal annotations = %+v, want readOnly+idempotent", desc.Annotations)
	}
	cfg, stateDir := journalTestConfig(t)
	cfg.ReadOnly = true
	frames := serve(t, &cfg,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"journal","arguments":{"repo":"acme/app","pr":"5101"}}}`,
	)
	var res callToolResult
	if err := json.Unmarshal(frames["1"].Result, &res); err != nil {
		t.Fatalf("decode journal result: %v", err)
	}
	if res.IsError {
		t.Fatalf("journal returned error: %+v", res)
	}
	assertNoFilesUnder(t, stateDir)
}

// TestMCPReadOnlyRefusedCallWritesNoJournal pins Amendment J4: a --read-only
// MCP server refuses a non-dry record (and hidden tools) before journaling,
// writing no journal anywhere in stateDir or the repository.
func TestMCPReadOnlyRefusedCallWritesNoJournal(t *testing.T) {
	fixture(t)
	cfg, stateDir := journalTestConfig(t)
	cfg.ReadOnly = true
	frames := serve(t, &cfg,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"record","arguments":{"repo":"acme/app","pr":"5101"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"triage","arguments":{"repo":"acme/app","pr":"5101","sig":"deadbeef","status":"wont-fix","rationale":"nope"}}}`,
	)
	var res callToolResult
	if err := json.Unmarshal(frames["1"].Result, &res); err != nil {
		t.Fatalf("decode record result: %v", err)
	}
	if !res.IsError {
		t.Fatalf("read-only non-dry record succeeded, want refusal: %+v", res)
	}
	assertNoFilesUnder(t, stateDir)
}

func assertNoFilesUnder(t *testing.T, root string) {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			found = append(found, filepath.ToSlash(p))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(found) != 0 {
		t.Fatalf("unexpected files written under stateDir: %v", found)
	}
}
