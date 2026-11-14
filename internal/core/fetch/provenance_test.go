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

const fiveRuleKB = `{"id":"r-1","rule":"SECRET_RULE_ONE Always wrap returned errors with context","scope":"src/*","hits":4,"judged":4,"status":"active"}
{"id":"r-2","rule":"SECRET_RULE_TWO Never ignore closing errors on write handles","scope":"src/*","hits":3,"judged":4,"status":"active"}
{"id":"r-3","rule":"SECRET_RULE_THREE Validate caller input bounds before indexing","scope":"src/*","hits":2,"judged":4,"status":"active"}
{"id":"r-4","rule":"SECRET_RULE_FOUR Check mutex release on early returns","scope":"src/*","hits":1,"judged":4,"status":"active"}
{"id":"r-5","rule":"SECRET_RULE_FIVE Prefer explicit timeouts over background sleep","scope":"src/*","hits":0,"judged":4,"status":"active"}
`

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
			"git\x00rev-parse\x00HEAD":                           {stdout: localTestHead + "\n"},
			"git\x00diff\x00HEAD":                                {stdout: recallRunDiff},
			"git\x00diff\x00--no-renames\x00--name-only\x00HEAD": {stdout: "src/a.go\n"},
			"git\x00diff\x00--numstat\x00HEAD":                   {stdout: "1\t1\tsrc/a.go\n"},
			"git\x00ls-files\x00--others\x00--exclude-standard":  {stdout: ""},
		},
	}
	if configJSON != "" {
		scriptBasePolicy(cmdr, localTestHead, configJSON)
	} else {
		scriptEmptyBasePolicy(cmdr, localTestHead)
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

func TestFetchLocalProvenanceOverCapAndCharCap(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg1 := `{"schema_version":"1","defaults":{"kb_policy":{"max_chars":140}}}`
	writeLocalFixtureFiles(t, cfg1)

	_, _, bundleDir := runLocalProvenanceFixture(t, cfg1, nil)
	r1, raw1 := readReceiptFile(t, bundleDir)
	if r1.RunID == "" {
		t.Fatal("expected non-empty run_id")
	}

	// Identical re-run keeps run_id deterministic.
	_, _, _ = runLocalProvenanceFixture(t, cfg1, nil)
	r2, _ := readReceiptFile(t, bundleDir)
	if r2.RunID != r1.RunID {
		t.Fatalf("run_id drifted across identical runs: %q vs %q", r1.RunID, r2.RunID)
	}

	// Secret / rule text must never enter provenance.json.
	for _, secret := range []string{"SECRET_RULE_ONE", "SECRET_RULE_FOUR", "LEARNING_TEXT_SENTINEL"} {
		if strings.Contains(raw1, secret) {
			t.Fatalf("provenance.json copied forbidden rule/learning text %q", secret)
		}
	}

	// Verify KB over-cap + char cap + non-truncated learnings.
	var sawKB, sawLearn bool
	omittedTopN := 0
	for _, in := range r1.Inputs {
		switch in.Kind {
		case "kb":
			sawKB = true
			if in.Status != "truncated" || in.Reason != "cap" {
				t.Fatalf("kb entry status=%q reason=%q, want truncated/cap", in.Status, in.Reason)
			}
			if in.Included == nil || *in.Included != 3 || in.Omitted == nil || *in.Omitted != 2 {
				t.Fatalf("kb counts = (%v, %v), want (3, 2)", in.Included, in.Omitted)
			}
			if in.Cap == nil || *in.Cap != 140 || in.CharsIncluded == nil || in.CharsTotal == nil || *in.CharsIncluded > 140 || *in.CharsTotal <= *in.CharsIncluded {
				t.Fatalf("kb char cap details invalid: %+v", in)
			}
		case "kb_candidate":
			if in.Status == "omitted" && in.Reason == "top_n" {
				omittedTopN++
			}
		case "learnings":
			sawLearn = true
			if in.Status != "included" {
				t.Fatalf("learnings status = %q, want included", in.Status)
			}
		}
	}
	if !sawKB || !sawLearn || omittedTopN != 2 {
		t.Fatalf("receipt checks failed: sawKB=%v sawLearn=%v omittedTopN=%d", sawKB, sawLearn, omittedTopN)
	}

	// Control change moves run_id, and REVIEW_KB_MAX_CHARS applies when repo omits kb_policy.max_chars.
	cfg2 := `{"schema_version":"1","defaults":{"effort":"deep"}}`
	writeLocalFixtureFiles(t, cfg2)
	_, _, _ = runLocalProvenanceFixture(t, cfg2, map[string]string{"REVIEW_KB_MAX_CHARS": "90"})
	r3, _ := readReceiptFile(t, bundleDir)
	if r3.RunID == r1.RunID {
		t.Fatalf("changing control config did not change run_id (%q)", r1.RunID)
	}
	for _, in := range r3.Inputs {
		if in.Kind == "kb" && (in.Cap == nil || *in.Cap != 90) {
			t.Fatalf("REVIEW_KB_MAX_CHARS cap = %v, want 90", in.Cap)
		}
	}
}

func TestFetchLocalReceiptWriteFailureWarnsWithoutFailing(t *testing.T) {
	t.Chdir(t.TempDir())
	writeLocalFixtureFiles(t, "")
	bundleDir := filepath.Join(".verdict", "state", "local-mine")
	// Plant a directory where provenance.json is written so atomic write/rename fails.
	if err := os.MkdirAll(filepath.Join(bundleDir, "provenance.json", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	data, warnings, _ := runLocalProvenanceFixture(t, "", nil)
	if data == nil {
		t.Fatal("expected non-nil fetch data despite receipt write failure")
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "provenance.json") {
		t.Fatalf("expected provenance.json warning, got: %v", warnings)
	}
}
