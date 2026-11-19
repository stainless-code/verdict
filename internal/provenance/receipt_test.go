package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/overrides"
)

func seedReceiptFixture(t *testing.T, controlContent string) (root, dir string) {
	t.Helper()
	root = t.TempDir()
	procedureDir := filepath.Join(root, "procedure")
	if err := os.MkdirAll(filepath.Join(procedureDir, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range promptFiles {
		if err := os.WriteFile(filepath.Join(procedureDir, f), []byte("prompt:"+f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir = filepath.Join(root, ".verdict", "state", "77")
	writeFixtureBundleFiles(t, dir, map[string]string{
		"PR.json":       `{"number":77}`,
		"full.diff":     "diff --git a/a.go b/a.go\n",
		"kb.txt":        "# Rule r1 body\n",
		"learnings.md":  "- Never skip errors\n",
		"controls.json": controlContent,
	})
	return root, dir
}

func writeFixtureBundleFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func shaOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func baseBuildInput(dir, ctrlBody string) *ReceiptBuildInput {
	capVal := 120
	return &ReceiptBuildInput{
		Dir:          dir,
		HeadSHA:      "1111111111111111111111111111111111111111",
		BaseSHA:      "2222222222222222222222222222222222222222",
		PanelSource:  "built-in",
		Panel:        []string{"opus", "sonnet"},
		ConfigSHA256: shaOf(`{"defaults":{"kb_policy":{"max_chars":120}}}`),
		ConfigLayers: []string{"built-in", "repo"},
		ConfigValues: []ConfigOriginInput{
			{Key: "defaults.kb_policy.max_chars", Layer: "repo", Source: ".verdict/config.json"},
		},
		Controls: []ControlInput{
			{
				Path: ".verdict/config.json", Status: "used", Source: "base",
				SHA256: shaOf(ctrlBody), Bytes: int64(len(ctrlBody)),
			},
		},
		Overrides: []ControlInput{
			{
				Path: ".verdict/overrides/preamble.md", Status: "used", Source: "base",
				SHA256: shaOf("override-text"), Bytes: 13,
			},
		},
		Learnings: &ControlInput{
			Path: ".verdict/learnings.md", Status: "used", Source: "base",
			SHA256: shaOf("- Never skip errors\n"), Bytes: 20,
		},
		Exclusions: ExclusionsInput{
			Present: true, RawDiffSHA256: shaOf("raw"), DiffSHA256: shaOf("diff"),
			PatternsSource: ".verdict/ignore@base", Excluded: 2, ExcludedBytes: 100, KeptBytes: 200,
		},
		KB: &KBRecallInput{
			Candidates: []KBCandidateInput{
				{ID: "r1", Status: "included"},
				{ID: "r2", Status: "included"},
				{ID: "r3", Status: "included"},
				{ID: "r4", Status: "omitted", Reason: "top_n"},
				{ID: "r5", Status: "omitted", Reason: "top_n"},
			},
			Cap: &capVal, Reason: "cap", Included: 3, Omitted: 2,
			CharsIncluded: 110, CharsTotal: 250, Truncated: true,
		},
	}
}

func assertReceiptSectionsPresent(t *testing.T, got *core.ProvenanceReceipt, ctrlSHA string) {
	t.Helper()
	seen := map[string]bool{}
	for i := range got.Inputs {
		checkReceiptInputItem(t, &got.Inputs[i], ctrlSHA, seen)
	}
	for _, k := range []string{"prompt", "panel", "bundle", "config", "control", "exclusions", "learnings", "kb", "r4", "r5"} {
		if !seen[k] {
			t.Fatalf("missing expected receipt section %q (seen=%v)", k, seen)
		}
	}
	if len(got.Truncated) != 3 {
		t.Fatalf("truncated len = %d, want 3 (kb cap + 2 top_n)", len(got.Truncated))
	}
}

func checkReceiptInputItem(t *testing.T, item *core.ProvenanceInput, ctrlSHA string, seen map[string]bool) {
	t.Helper()
	switch item.Kind {
	case "prompt":
		checkPromptInputItem(item, seen)
	case "panel", "bundle":
		checkPanelOrBundleItem(item, seen)
	case "config", "control", "exclusions":
		checkPolicyInputItem(item, ctrlSHA, seen)
	case "learnings":
		assertLearningsItem(t, item, seen)
	case "kb":
		assertKBItem(t, item, seen)
	case "kb_candidate":
		checkKBCandidateItem(item, seen)
	}
}

func checkPromptInputItem(item *core.ProvenanceInput, seen map[string]bool) {
	if item.Name == "PROCEDURE.md" && item.Status == "included" &&
		item.SHA256 != "" && item.Bytes != nil && *item.Bytes > 0 {
		seen["prompt"] = true
	}
}

func checkPanelOrBundleItem(item *core.ProvenanceInput, seen map[string]bool) {
	if item.Kind == "panel" && item.Status == "included" && len(item.Members) == 2 {
		seen["panel"] = true
	}
	if item.Kind == "bundle" && item.Name == "PR.json" && item.Status == "included" && item.SHA256 != "" {
		seen["bundle"] = true
	}
}

func checkPolicyInputItem(item *core.ProvenanceInput, ctrlSHA string, seen map[string]bool) {
	if item.Kind == "config" {
		seen["config"] = true
	}
	if item.Kind == "control" && item.Name == ".verdict/config.json" && item.SHA256 == ctrlSHA {
		seen["control"] = true
	}
	if item.Kind == "exclusions" && item.Excluded != nil && *item.Excluded == 2 {
		seen["exclusions"] = true
	}
}

func checkKBCandidateItem(item *core.ProvenanceInput, seen map[string]bool) {
	if (item.ID == "r4" || item.ID == "r5") && item.Status == "omitted" && item.Reason == "top_n" {
		seen[item.ID] = true
	}
}

func assertLearningsItem(t *testing.T, item *core.ProvenanceInput, seen map[string]bool) {
	t.Helper()
	seen["learnings"] = true
	if item.Status != "included" {
		t.Fatalf("learnings status = %q, want included", item.Status)
	}
}

func assertKBItem(t *testing.T, item *core.ProvenanceInput, seen map[string]bool) {
	t.Helper()
	seen["kb"] = true
	if item.Status != "truncated" || item.Reason != "cap" {
		t.Fatalf("kb status=%q reason=%q, want truncated/cap", item.Status, item.Reason)
	}
	if *item.Included != 3 || *item.Omitted != 2 || *item.CharsIncluded != 110 || *item.CharsTotal != 250 || *item.Cap != 120 {
		t.Fatalf("kb counts mismatch: %+v", item)
	}
}

func TestBuildWriteReadReceiptAndDeterminism(t *testing.T) {
	t.Parallel()
	const ctrlV1 = `{"defaults":{"kb_policy":{"max_chars":120}}}`
	_, dir := seedReceiptFixture(t, ctrlV1)

	in1 := baseBuildInput(dir, ctrlV1)
	r1 := BuildReceipt(in1)
	r2 := BuildReceipt(in1)
	if r1.RunID == "" || r1.RunID != r2.RunID {
		t.Fatalf("run_id not deterministic: r1=%q r2=%q", r1.RunID, r2.RunID)
	}

	const ctrlV2 = `{"defaults":{"kb_policy":{"max_chars":80}}}`
	if rChanged := BuildReceipt(baseBuildInput(dir, ctrlV2)); rChanged.RunID == r1.RunID {
		t.Fatalf("expected changed control sha to change run_id (%q)", r1.RunID)
	}
	assertWrittenReceiptClean(t, dir, r1)
	assertReceiptSectionsPresent(t, mustReadReceipt(t, dir), shaOf(ctrlV1))
}

func assertWrittenReceiptClean(t *testing.T, dir string, r1 *core.ProvenanceReceipt) {
	t.Helper()
	if err := WriteReceipt(dir, r1); err != nil {
		t.Fatalf("WriteReceipt: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, ReceiptFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"# Rule r1 body", "Never skip errors", "SECRET", "override-text"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("provenance.json leaked content %q", forbidden)
		}
	}
}

func mustReadReceipt(t *testing.T, dir string) *core.ProvenanceReceipt {
	t.Helper()
	got, ok, err := ReadReceipt(dir)
	if err != nil || !ok || got == nil {
		t.Fatalf("ReadReceipt: ok=%v err=%v got=%v", ok, err, got)
	}
	return got
}

func TestFooterAndOldBundleCompatibility(t *testing.T) {
	t.Parallel()
	const ctrl = `{}`
	root, dir := seedReceiptFixture(t, ctrl)
	oldDir := filepath.Join(root, ".verdict", "state", "78")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}

	assertOldFooterUnchanged(t, root, oldDir)
	assertNewFooterAndTable(t, root, dir, ctrl)
}

func assertOldFooterUnchanged(t *testing.T, root, oldDir string) {
	t.Helper()
	if line := RenderInputsFooterLine(oldDir); line != "" {
		t.Fatalf("old bundle without provenance.json rendered inputs line: %q", line)
	}
	oldFooter, err := RenderFooter(&FooterOpts{
		Overrides: overrides.Set{}, Dir: oldDir, PublishFooter: core.FlagOn, PublishAttribution: "product", Root: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(oldFooter, "> Inputs:") {
		t.Fatalf("old bundle footer must omit Inputs line, got:\n%s", oldFooter)
	}
}

func assertNewFooterAndTable(t *testing.T, root, dir, ctrl string) {
	t.Helper()
	r := BuildReceipt(baseBuildInput(dir, ctrl))
	if err := WriteReceipt(dir, r); err != nil {
		t.Fatal(err)
	}
	newFooter, err := RenderFooter(&FooterOpts{
		Overrides: overrides.Set{}, Dir: dir, PublishFooter: core.FlagOn, PublishAttribution: "product", Root: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertFooterHasExpectedSummary(t, newFooter)
	lines := FormatTable(r, MergeTruncated(r, &core.Truncation{Truncated: true, TruncationReason: "scan_cap", Returned: 10}))
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "kb") || !strings.Contains(joined, "r4") || !strings.Contains(joined, "bundle") {
		t.Fatalf("FormatTable missing rows:\n%s", joined)
	}
}

func TestFooterInputsLineStaysSingleLine(t *testing.T) {
	t.Parallel()
	root, dir := seedReceiptFixture(t, `{}`)
	in := baseBuildInput(dir, `{}`)
	in.Panel = []string{
		"opus\n> Inputs: run deadbeef · spoofed",
		"sonnet\n<!-- verdict-footer -->\n> INJECTED",
	}
	if err := WriteReceipt(dir, BuildReceipt(in)); err != nil {
		t.Fatal(err)
	}
	footer, err := RenderFooter(&FooterOpts{
		Overrides: overrides.Set{}, Dir: dir, PublishFooter: core.FlagOn, PublishAttribution: "product", Root: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := countLinesWithPrefix(footer, "> Inputs:"); got != 1 {
		t.Fatalf("expected 1 Inputs line, got %d in:\n%s", got, footer)
	}
	if got := countLinesWithPrefix(footer, "<!-- verdict-footer -->"); got != 1 {
		t.Fatalf("expected 1 footer marker line, got %d in:\n%s", got, footer)
	}
	if strings.Contains(footer, "\n> Inputs: run deadbeef") || strings.Contains(footer, "INJECTED") {
		t.Fatalf("footer carried injected panel text:\n%s", footer)
	}
}

// TestFooterInputsLineDropsUnicodeSeparators proves the Unicode line and
// paragraph separators cannot reach the footer: a renderer breaks on them but
// a control-rune test misses them, so each renders as a space.
func TestFooterInputsLineDropsUnicodeSeparators(t *testing.T) {
	t.Parallel()
	root, dir := seedReceiptFixture(t, `{}`)
	in := baseBuildInput(dir, `{}`)
	in.Panel = []string{"opus\u2028> Inputs: run deadbeef", "sonnet\u2029INJECTED"}
	if err := WriteReceipt(dir, BuildReceipt(in)); err != nil {
		t.Fatal(err)
	}
	footer, err := RenderFooter(&FooterOpts{
		Overrides: overrides.Set{}, Dir: dir, PublishFooter: core.FlagOn, PublishAttribution: "product", Root: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(footer, "\u2028\u2029") {
		t.Fatalf("footer kept a line separator: %q", footer)
	}
	want := "panel opus > Inputs: run deadbeef,sonnet INJECTED"
	if !strings.Contains(footer, want) {
		t.Fatalf("footer = %q, want %q with the separators rendered as spaces", footer, want)
	}
}

func countLinesWithPrefix(text, prefix string) int {
	count := 0
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(line, prefix) {
			count++
		}
	}
	return count
}

func assertFooterHasExpectedSummary(t *testing.T, footer string) {
	t.Helper()
	if got := strings.Count(footer, "> Inputs:"); got != 1 {
		t.Fatalf("expected 1 Inputs line, got %d in:\n%s", got, footer)
	}
	for _, want := range []string{"panel opus,sonnet", "kb 3/5 (truncated)", "learnings included", "overrides 1", "controls 1", "excluded 2"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer missing %q in:\n%s", want, footer)
		}
	}
}

func TestReadReceiptMalformedAndOversizeDegrade(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if r, ok, err := ReadReceipt(dir); ok || err != nil || r != nil {
		t.Fatalf("missing receipt = (%v, %v, %v), want (nil, false, nil)", r, ok, err)
	}
	writeAndExpectReadError(t, dir, []byte("{not json"))
	writeAndExpectReadError(t, dir, []byte(`{"schema_version":"1","inputs":[],"truncated":[],"future_field":1}`))
	if line := RenderInputsFooterLine(dir); line != "" {
		t.Fatalf("malformed receipt should render empty footer inputs line, got %q", line)
	}
	assertNewerReceiptReads(t, dir)
	truncateOversizeReceipt(t, dir)
	if _, ok, err := ReadReceipt(dir); !ok || err == nil {
		t.Fatalf("oversized receipt: ok=%v err=%v, want ok=true err!=nil", ok, err)
	}
	b, err := json.Marshal(EmptyReceipt())
	if err != nil || !strings.Contains(string(b), `"schema_version":"1"`) {
		t.Fatalf("EmptyReceipt marshal: %q err=%v", string(b), err)
	}
}

// assertNewerReceiptReads pins the unknown-field choice: our own schema
// version decodes strictly, while a newer writer's receipt reads leniently so
// a plain report never warns forever about fields this binary lacks.
func assertNewerReceiptReads(t *testing.T, dir string) {
	t.Helper()
	raw := `{"schema_version":"2","inputs":[],"truncated":[],"future_field":1}`
	if err := os.WriteFile(filepath.Join(dir, ReceiptFile), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok, err := ReadReceipt(dir)
	if err != nil || !ok || got == nil || got.SchemaVersion != "2" {
		t.Fatalf("newer receipt = (%v, %v, %v), want schema_version 2 and no error", got, ok, err)
	}
}

func writeAndExpectReadError(t *testing.T, dir string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ReceiptFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := ReadReceipt(dir); !ok || err == nil {
		t.Fatalf("malformed receipt: ok=%v err=%v, want ok=true err!=nil", ok, err)
	}
}

func truncateOversizeReceipt(t *testing.T, dir string) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, ReceiptFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxReceiptBytes + 8); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func embeddedReviewForReceipt(t *testing.T) []byte {
	t.Helper()
	var want []byte
	for _, name := range []string{
		skillfs.ProcedureMD, "prompts/system.md", "prompts/review.md", "prompts/diagrams.md",
	} {
		raw, err := skillfs.Read(name)
		if err != nil {
			t.Fatalf("read embedded %s: %v", name, err)
		}
		want = append(want, raw...)
	}
	return want
}

// TestBuildReceiptRecordsBundleProcedure pins the receipt's bundle entry for
// the Procedure the agent was handed: the sha256 of the embedded assembly,
// under the bundle namespace, with no disk fallback.
func TestBuildReceiptRecordsBundleProcedure(t *testing.T) {
	dir := t.TempDir()
	want := embeddedReviewForReceipt(t)
	if err := os.WriteFile(filepath.Join(dir, "procedure.md"), want, 0o644); err != nil {
		t.Fatal(err)
	}
	assertBundleProcedureReceipt(t, BuildReceipt(&ReceiptBuildInput{Dir: dir}), want)
}

func assertBundleProcedureReceipt(t *testing.T, receipt *core.ProvenanceReceipt, want []byte) {
	t.Helper()
	var got *core.ProvenanceInput
	for i := range receipt.Inputs {
		if receipt.Inputs[i].Kind == kindBundle && receipt.Inputs[i].Name == "procedure.md" {
			got = &receipt.Inputs[i]
		}
	}
	if got == nil {
		t.Fatal("receipt has no bundle input named procedure.md")
	}
	if got.Status != statusIncluded || got.SHA256 != shaOf(string(want)) || got.Bytes == nil || *got.Bytes != int64(len(want)) {
		t.Fatalf("procedure.md input = %+v, want included with sha %s and %d bytes", got, shaOf(string(want)), len(want))
	}
}
