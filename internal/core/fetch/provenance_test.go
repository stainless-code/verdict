package fetchcore

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

const (
	provTestHead = "dddddddddddddddddddddddddddddddddddddddd"
	fiveRuleKB   = `{"id":"r-1","rule":"SECRET_RULE_ONE Always wrap returned errors with context","scope":"src/*","hits":4,"judged":4,"status":"active"}
{"id":"r-2","rule":"SECRET_RULE_TWO Never ignore closing errors on write handles","scope":"src/*","hits":3,"judged":4,"status":"active"}
{"id":"r-3","rule":"SECRET_RULE_THREE Validate caller input bounds before indexing","scope":"src/*","hits":2,"judged":4,"status":"active"}
{"id":"r-4","rule":"SECRET_RULE_FOUR Check mutex release on early returns","scope":"src/*","hits":1,"judged":4,"status":"active"}
{"id":"r-5","rule":"SECRET_RULE_FIVE Prefer explicit timeouts over background sleep","scope":"src/*","hits":0,"judged":4,"status":"active"}
`
)

func writeLocalFixtureFiles(t *testing.T, configJSON string) {
	t.Helper()
	if err := os.MkdirAll(".verdict", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".verdict/kb.jsonl", []byte(fiveRuleKB), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".verdict/learnings.md", []byte("- LEARNING_TEXT_SENTINEL check nil pointers\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if configJSON != "" {
		if err := os.WriteFile(".verdict/config.json", []byte(configJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func runLocalProvenanceFixture(t *testing.T, configJSON string, env map[string]string) (*core.FetchResponse, []string, string) {
	t.Helper()
	cmdr := &scriptCommander{
		t: t,
		out: map[string]scriptResult{
			"git\x00rev-parse\x00--is-inside-work-tree":          {stdout: "true\n"},
			"git\x00branch\x00--show-current":                    {stdout: "main\n"},
			"git\x00rev-parse\x00HEAD":                           {stdout: provTestHead + "\n"},
			"git\x00diff\x00HEAD":                                {stdout: recallRunDiff},
			"git\x00diff\x00--no-renames\x00--name-only\x00HEAD": {stdout: "src/a.go\n"},
			"git\x00diff\x00--numstat\x00HEAD":                   {stdout: "1\t1\tsrc/a.go\n"},
			"git\x00ls-files\x00--others\x00--exclude-standard":  {stdout: ""},
		},
	}
	if configJSON != "" {
		scriptBasePolicy(cmdr, provTestHead, configJSON)
	} else {
		scriptEmptyBasePolicy(cmdr, provTestHead)
	}
	var human, diag bytes.Buffer
	var warnings []string
	req := localScriptReq(cmdr, &human, &diag, &warnings, env, core.FetchLocalFlags{})
	resp, err := RunLocal(context.Background(), req)
	if err != nil {
		t.Fatalf("RunLocal error: %v", err)
	}
	return resp, warnings, filepath.Join(".verdict", "state", "local-mine")
}

func readReceiptFile(t *testing.T, bundleDir string) (core.ProvenanceReceipt, string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(bundleDir, "provenance.json"))
	if err != nil {
		t.Fatalf("read provenance.json: %v", err)
	}
	var r core.ProvenanceReceipt
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("unmarshal provenance.json: %v", err)
	}
	return r, string(raw)
}

type kbReceiptFlags struct {
	sawKB       bool
	sawLearn    bool
	omittedTopN int
}

func assertProvenanceKBCapAndSecrets(t *testing.T, r *core.ProvenanceReceipt, raw string) {
	t.Helper()
	assertNoSecretLeaks(t, raw)
	var flags kbReceiptFlags
	for i := range r.Inputs {
		inspectKBReceiptInput(t, &r.Inputs[i], &flags)
	}
	if !flags.sawKB || !flags.sawLearn || flags.omittedTopN != 2 {
		t.Fatalf("receipt checks failed: %+v", flags)
	}
}

func inspectKBReceiptInput(t *testing.T, in *core.ProvenanceInput, flags *kbReceiptFlags) {
	t.Helper()
	switch in.Kind {
	case "kb":
		flags.sawKB = true
		assertKBEntryCapped(t, in)
	case "kb_candidate":
		if isOmittedTopN(in) {
			flags.omittedTopN++
		}
	case "learnings":
		flags.sawLearn = checkLearningsIncluded(t, in)
	}
}

func isOmittedTopN(in *core.ProvenanceInput) bool {
	return in.Status == "omitted" && in.Reason == "top_n"
}

func assertNoSecretLeaks(t *testing.T, raw string) {
	t.Helper()
	for _, secret := range []string{"SECRET_RULE_ONE", "SECRET_RULE_FOUR", "LEARNING_TEXT_SENTINEL"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("provenance.json copied forbidden rule/learning text %q", secret)
		}
	}
}

func checkLearningsIncluded(t *testing.T, in *core.ProvenanceInput) bool {
	t.Helper()
	if in.Status != "included" {
		t.Fatalf("learnings status = %q, want included", in.Status)
	}
	return true
}

func assertKBEntryCapped(t *testing.T, in *core.ProvenanceInput) {
	t.Helper()
	if in.Status != "truncated" || in.Reason != "cap" {
		t.Fatalf("kb entry status=%q reason=%q, want truncated/cap", in.Status, in.Reason)
	}
	if in.Included == nil || *in.Included != 3 || in.Omitted == nil || *in.Omitted != 2 {
		t.Fatalf("kb counts = (%v, %v), want (3, 2)", in.Included, in.Omitted)
	}
	assertKBCharCap(t, in)
}

func assertKBCharCap(t *testing.T, in *core.ProvenanceInput) {
	t.Helper()
	if in.Cap == nil || *in.Cap != 140 || in.CharsIncluded == nil || in.CharsTotal == nil {
		t.Fatalf("kb char cap missing fields: %+v", in)
	}
	if *in.CharsIncluded > 140 || *in.CharsTotal <= *in.CharsIncluded {
		t.Fatalf("kb char cap range invalid: included=%d total=%d", *in.CharsIncluded, *in.CharsTotal)
	}
}

func assertKBCapEquals(t *testing.T, r *core.ProvenanceReceipt, want int) {
	t.Helper()
	for i := range r.Inputs {
		if r.Inputs[i].Kind == "kb" && (r.Inputs[i].Cap == nil || *r.Inputs[i].Cap != want) {
			t.Fatalf("kb cap = %v, want %d", r.Inputs[i].Cap, want)
		}
	}
}

func TestFetchLocalProvenanceOverCapAndCharCap(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg1 := `{"schema_version":"1","defaults":{"kb_policy":{"max_chars":140}}}`
	writeLocalFixtureFiles(t, cfg1)

	_, _, bundleDir := runLocalProvenanceFixture(t, cfg1, nil)
	r1, raw1 := readReceiptFile(t, bundleDir)
	if r1.RunID == "" {
		t.Fatal("expected non-empty run_id")
	}
	_, _, _ = runLocalProvenanceFixture(t, cfg1, nil)
	if r2, _ := readReceiptFile(t, bundleDir); r2.RunID != r1.RunID {
		t.Fatalf("run_id drifted across identical runs: %q vs %q", r1.RunID, r2.RunID)
	}
	assertProvenanceKBCapAndSecrets(t, &r1, raw1)

	cfg2 := `{"schema_version":"1","defaults":{"effort":"deep"}}`
	writeLocalFixtureFiles(t, cfg2)
	_, _, _ = runLocalProvenanceFixture(t, cfg2, map[string]string{"REVIEW_KB_MAX_CHARS": "90"})
	r3, _ := readReceiptFile(t, bundleDir)
	if r3.RunID == r1.RunID {
		t.Fatalf("changing control config did not change run_id (%q)", r1.RunID)
	}
	assertKBCapEquals(t, &r3, 90)
}

func TestFetchLocalReceiptWriteFailureWarnsWithoutFailing(t *testing.T) {
	t.Chdir(t.TempDir())
	writeLocalFixtureFiles(t, "")
	bundleDir := filepath.Join(".verdict", "state", "local-mine")
	if err := os.MkdirAll(filepath.Join(bundleDir, "provenance.json", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, warnings, _ := runLocalProvenanceFixture(t, "", nil)
	if data == nil {
		t.Fatal("expected non-nil fetch data despite receipt write failure")
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "provenance.json") {
		t.Fatalf("expected provenance.json warning, got: %v", warnings)
	}
}
