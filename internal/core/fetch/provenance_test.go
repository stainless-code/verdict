package fetchcore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/provenance"
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
	cappedTrunc int
	cappedOmit  int
	cappedIncl  int
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
	if flags.cappedTrunc != 1 || flags.cappedOmit != 2 || flags.cappedIncl != 0 {
		t.Fatalf("capped candidates = %d truncated, %d omitted, %d included; want 1, 2, 0",
			flags.cappedTrunc, flags.cappedOmit, flags.cappedIncl)
	}
}

func inspectKBReceiptInput(t *testing.T, in *core.ProvenanceInput, flags *kbReceiptFlags) {
	t.Helper()
	switch in.Kind {
	case "kb":
		flags.sawKB = true
		assertKBEntryCapped(t, in)
	case "kb_candidate":
		inspectKBCandidateStatus(in, flags)
	case "learnings":
		flags.sawLearn = checkLearningsIncluded(t, in)
	}
}

func inspectKBCandidateStatus(in *core.ProvenanceInput, flags *kbReceiptFlags) {
	switch {
	case isOmittedTopN(in):
		flags.omittedTopN++
	case in.Reason != "cap":
		return
	case in.Status == "truncated":
		flags.cappedTrunc++
	case in.Status == "omitted":
		flags.cappedOmit++
	case in.Status == "included":
		flags.cappedIncl++
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
		t.Fatalf("kb counts = (%s, %s), want (3, 2)", intPtrText(in.Included), intPtrText(in.Omitted))
	}
	assertKBCharCap(t, in)
}

func intPtrText(v *int) string {
	if v == nil {
		return "nil"
	}
	return strconv.Itoa(*v)
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
	found := false
	for i := range r.Inputs {
		if r.Inputs[i].Kind != "kb" {
			continue
		}
		found = true
		if r.Inputs[i].Cap == nil || *r.Inputs[i].Cap != want {
			t.Fatalf("kb cap = %s, want %d", intPtrText(r.Inputs[i].Cap), want)
		}
	}
	if !found {
		t.Fatalf("receipt carries no kb row, so the cap %d is unverified", want)
	}
}

func TestFetchLocalFooterCountsPresentPathsOnly(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := `{"schema_version":"1","defaults":{"kb_policy":{"max_chars":140}}}`
	writeLocalFixtureFiles(t, cfg)
	_, _, bundleDir := runLocalProvenanceFixture(t, cfg, nil)
	line := provenance.RenderInputsFooterLine(bundleDir)
	for _, want := range []string{"controls 2 ·", "learnings included ·", "overrides 0 ·"} {
		if !strings.Contains(line, want) {
			t.Fatalf("footer missing %q in: %q", want, line)
		}
	}
}

func TestFetchLocalLearningsAbsentIsNotIncluded(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := `{"schema_version":"1","defaults":{"kb_policy":{"max_chars":140}}}`
	writeLocalFixtureFiles(t, cfg)
	if err := os.Remove(".verdict/learnings.md"); err != nil {
		t.Fatal(err)
	}
	_, _, bundleDir := runLocalProvenanceFixture(t, cfg, nil)
	r, raw := readReceiptFile(t, bundleDir)
	if !strings.Contains(raw, `"status": "absent"`) {
		t.Fatalf("learnings row never reads absent:\n%s", raw)
	}
	line := provenance.RenderInputsFooterLine(bundleDir)
	if !strings.Contains(line, "learnings absent ·") {
		t.Fatalf("footer does not say learnings absent: %q", line)
	}
	assertLearningsRowAbsent(t, &r)
}

func assertLearningsRowAbsent(t *testing.T, r *core.ProvenanceReceipt) {
	t.Helper()
	for i := range r.Inputs {
		in := &r.Inputs[i]
		if in.Kind != "learnings" {
			continue
		}
		if in.Status != "absent" || in.Bytes != nil {
			t.Fatalf("learnings row = %q bytes=%v, want absent with no bytes", in.Status, in.Bytes)
		}
		return
	}
	t.Fatal("receipt carries no learnings row")
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
	assertBundleDigestsMatchDisk(t, bundleDir, &r1)

	cfg2 := `{"schema_version":"1","defaults":{"effort":"deep"}}`
	writeLocalFixtureFiles(t, cfg2)
	_, _, _ = runLocalProvenanceFixture(t, cfg2, map[string]string{"REVIEW_KB_MAX_CHARS": "90"})
	r3, _ := readReceiptFile(t, bundleDir)
	if r3.RunID == r1.RunID {
		t.Fatalf("changing control config did not change run_id (%q)", r1.RunID)
	}
	assertKBCapEquals(t, &r3, 90)
}

// assertBundleDigestsMatchDisk proves the receipt's bundle rows hash the bytes
// they name. Goldens scrub these digests so they stay clock-independent, so
// this relationship is the only place the value itself is checked.
func assertBundleDigestsMatchDisk(t *testing.T, bundleDir string, r *core.ProvenanceReceipt) {
	t.Helper()
	rows := 0
	for i := range r.Inputs {
		in := &r.Inputs[i]
		if in.Kind != "bundle" {
			continue
		}
		rows++
		assertBundleDigestMatchesDisk(t, bundleDir, in)
	}
	if rows == 0 {
		t.Fatal("receipt carries no bundle rows")
	}
}

// TestFetchBundleDigestsCoverThreadsAndBudget closes the gap the provenance
// goldens leave: the golden scrub blanks the digest of threads.json and
// budget.json, so this consented PR fixture — threads.json from the thread
// read, budget.json from the consented blind receipt — is the only place their
// value is checked against disk.
func TestFetchBundleDigestsCoverThreadsAndBudget(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	settings := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settings, []byte(`{"spend":{"require_known_estimate":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var human, diag bytes.Buffer
	var warnings []string
	req := fetchTestReq(fetchHappyCommander(t), &human, &diag, &warnings)
	req.SettingsPath = settings
	req.Flags.BudgetConsent = true
	if _, err := Run(context.Background(), req); err != nil {
		t.Fatalf("Run: %v", err)
	}
	bundleDir := filepath.Join(".verdict", "state", "7")
	r, _ := readReceiptFile(t, bundleDir)
	assertBundleDigestsMatchDisk(t, bundleDir, &r)
	for _, name := range []string{"threads.json", "budget.json"} {
		assertBundleRowNamed(t, &r, name)
	}
}

// assertBundleRowNamed proves the receipt carries a bundle row for one named
// artifact, so the digest check above cannot quietly skip it.
func assertBundleRowNamed(t *testing.T, r *core.ProvenanceReceipt, name string) {
	t.Helper()
	for i := range r.Inputs {
		if r.Inputs[i].Kind == "bundle" && r.Inputs[i].Name == name {
			return
		}
	}
	t.Fatalf("receipt carries no bundle row for %s", name)
}

func assertBundleDigestMatchesDisk(t *testing.T, bundleDir string, in *core.ProvenanceInput) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(bundleDir, in.Name))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if want := hex.EncodeToString(sum[:]); in.SHA256 != want {
		t.Fatalf("bundle %s sha256 = %s, want %s (digest of its disk bytes)", in.Name, in.SHA256, want)
	}
	assertBundleBytes(t, in, int64(len(raw)))
}

func assertBundleBytes(t *testing.T, in *core.ProvenanceInput, want int64) {
	t.Helper()
	if in.Bytes == nil {
		t.Fatalf("bundle %s bytes = nil, want %d", in.Name, want)
	}
	if *in.Bytes != want {
		t.Fatalf("bundle %s bytes = %d, want %d", in.Name, *in.Bytes, want)
	}
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
