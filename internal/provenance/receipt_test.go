package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/overrides"
)

func seedReceiptFixture(t *testing.T, controlContent string) (root, dir string) {
	t.Helper()
	root = t.TempDir()
	skillDir := filepath.Join(root, "skills", "verdict")
	if err := os.MkdirAll(filepath.Join(skillDir, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range promptFiles {
		if err := os.WriteFile(filepath.Join(skillDir, f), []byte("prompt:"+f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir = filepath.Join(root, ".verdict", "state", "77")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"PR.json":       `{"number":77}`,
		"full.diff":     "diff --git a/a.go b/a.go\n",
		"kb.txt":        "# Rule r1 body\n",
		"learnings.md":  "- Never skip errors\n",
		"controls.json": controlContent,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, dir
}

func shaOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func baseBuildInput(root, dir, ctrlBody string) *ReceiptBuildInput {
	capVal := 120
	return &ReceiptBuildInput{
		Root:         root,
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
				Path:   ".verdict/config.json",
				Status: "present",
				Source: "base",
				SHA256: shaOf(ctrlBody),
				Bytes:  int64(len(ctrlBody)),
			},
		},
		Overrides: []ControlInput{
			{
				Path:   ".verdict/overrides/preamble.md",
				Status: "present",
				Source: "base",
				SHA256: shaOf("override-text"),
				Bytes:  13,
			},
		},
		Learnings: &ControlInput{
			Path:   ".verdict/learnings.md",
			Status: "present",
			Source: "base",
			SHA256: shaOf("- Never skip errors\n"),
			Bytes:  20,
		},
		Exclusions: ExclusionsInput{
			Present:        true,
			RawDiffSHA256:  shaOf("raw"),
			DiffSHA256:     shaOf("diff"),
			PatternsSource: ".verdict/ignore@base",
			Excluded:       2,
			ExcludedBytes:  100,
			KeptBytes:      200,
			Defaults:       true,
			Ignore:         true,
		},
		KB: &KBRecallInput{
			Candidates: []KBCandidateInput{
				{ID: "r1", Status: "included"},
				{ID: "r2", Status: "included"},
				{ID: "r3", Status: "included"},
				{ID: "r4", Status: "omitted", Reason: "top_n"},
				{ID: "r5", Status: "omitted", Reason: "top_n"},
			},
			Cap:           &capVal,
			Reason:        "cap",
			Included:      3,
			Omitted:       2,
			CharsIncluded: 110,
			CharsTotal:    250,
			Truncated:     true,
			NotesWritten:  true,
		},
	}
}

func TestBuildWriteReadReceiptAndDeterminism(t *testing.T) {
	t.Parallel()
	const ctrlV1 = `{"defaults":{"kb_policy":{"max_chars":120}}}`
	root, dir := seedReceiptFixture(t, ctrlV1)

	in1 := baseBuildInput(root, dir, ctrlV1)
	r1 := BuildReceipt(in1)
	r2 := BuildReceipt(in1)
	if r1.RunID == "" || r1.RunID != r2.RunID {
		t.Fatalf("run_id not deterministic: r1=%q r2=%q", r1.RunID, r2.RunID)
	}

	const ctrlV2 = `{"defaults":{"kb_policy":{"max_chars":80}}}`
	inChanged := baseBuildInput(root, dir, ctrlV2)
	rChanged := BuildReceipt(inChanged)
	if rChanged.RunID == r1.RunID {
		t.Fatalf("expected changed control sha to change run_id (%q)", r1.RunID)
	}

	if err := WriteReceipt(dir, r1); err != nil {
		t.Fatalf("WriteReceipt: %v", err)
	}
	got, ok, err := ReadReceipt(dir)
	if err != nil || !ok || got == nil {
		t.Fatalf("ReadReceipt: ok=%v err=%v got=%v", ok, err, got)
	}
	if got.RunID != r1.RunID || got.SchemaVersion != "1" {
		t.Fatalf("roundtrip mismatch: got=%+v want run=%s", got, r1.RunID)
	}

	raw, err := os.ReadFile(filepath.Join(dir, ReceiptFile))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, forbidden := range []string{"# Rule r1 body", "Never skip errors", "SECRET", "override-text"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("provenance.json leaked content %q", forbidden)
		}
	}

	// Verify KB candidate counts, truncation metadata, and non-truncated learnings.
	var sawKB, sawLearnings, sawR4, sawR5, sawPrompt, sawPanel, sawBundle, sawConfig, sawExcl, sawCtrl bool
	for _, item := range got.Inputs {
		switch item.Kind {
		case "prompt":
			if item.Name == "SKILL.md" && item.Status == "included" && item.SHA256 != "" && item.Bytes != nil && *item.Bytes > 0 {
				sawPrompt = true
			}
		case "panel":
			if item.Status == "included" && len(item.Members) == 2 {
				sawPanel = true
			}
		case "bundle":
			if item.Name == "PR.json" && item.Status == "included" && item.SHA256 != "" {
				sawBundle = true
			}
		case "config":
			sawConfig = true
		case "control":
			if item.Name == ".verdict/config.json" && item.SHA256 == shaOf(ctrlV1) {
				sawCtrl = true
			}
		case "exclusions":
			if item.Excluded != nil && *item.Excluded == 2 {
				sawExcl = true
			}
		case "learnings":
			sawLearnings = true
			if item.Status != "included" {
				t.Fatalf("learnings status = %q, want included", item.Status)
			}
		case "kb":
			sawKB = true
			if item.Status != "truncated" || item.Reason != "cap" {
				t.Fatalf("kb status=%q reason=%q, want truncated/cap", item.Status, item.Reason)
			}
			if *item.Included != 3 || *item.Omitted != 2 || *item.CharsIncluded != 110 || *item.CharsTotal != 250 || *item.Cap != 120 {
				t.Fatalf("kb counts mismatch: %+v", item)
			}
		case "kb_candidate":
			if item.ID == "r4" && item.Status == "omitted" && item.Reason == "top_n" {
				sawR4 = true
			}
			if item.ID == "r5" && item.Status == "omitted" && item.Reason == "top_n" {
				sawR5 = true
			}
		}
	}
	if !sawKB || !sawLearnings || !sawR4 || !sawR5 || !sawPrompt || !sawPanel || !sawBundle || !sawConfig || !sawExcl || !sawCtrl {
		t.Fatalf("missing expected input sections: kb=%v learn=%v r4=%v r5=%v prompt=%v panel=%v bundle=%v cfg=%v excl=%v ctrl=%v",
			sawKB, sawLearnings, sawR4, sawR5, sawPrompt, sawPanel, sawBundle, sawConfig, sawExcl, sawCtrl)
	}
	if len(got.Truncated) != 3 {
		t.Fatalf("truncated len = %d, want 3 (kb cap + 2 top_n)", len(got.Truncated))
	}
}

func TestFooterAndOldBundleCompatibility(t *testing.T) {
	t.Parallel()
	const ctrl = `{}`
	root, dir := seedReceiptFixture(t, ctrl)
	oldDir := filepath.Join(root, ".verdict", "state", "78")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if line := RenderInputsFooterLine(oldDir); line != "" {
		t.Fatalf("old bundle without provenance.json rendered inputs line: %q", line)
	}
	oldFooter, err := RenderFooter(&FooterOpts{
		Overrides:          overrides.Set{},
		Dir:                oldDir,
		PublishFooter:      core.FlagOn,
		PublishAttribution: "product",
		Root:               root,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(oldFooter, "> Inputs:") {
		t.Fatalf("old bundle footer must omit Inputs line, got:\n%s", oldFooter)
	}

	r := BuildReceipt(baseBuildInput(root, dir, ctrl))
	if err := WriteReceipt(dir, r); err != nil {
		t.Fatal(err)
	}
	newFooter, err := RenderFooter(&FooterOpts{
		Overrides:          overrides.Set{},
		Dir:                dir,
		PublishFooter:      core.FlagOn,
		PublishAttribution: "product",
		Root:               root,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(newFooter, "> Inputs:"); got != 1 {
		t.Fatalf("expected 1 Inputs line, got %d in:\n%s", got, newFooter)
	}
	for _, want := range []string{"panel opus,sonnet", "kb 3/5 (truncated)", "learnings included", "excluded 2"} {
		if !strings.Contains(newFooter, want) {
			t.Fatalf("footer missing %q in:\n%s", want, newFooter)
		}
	}

	lines := FormatTable(r, MergeTruncated(r, &core.Truncation{
		Truncated:        true,
		TruncationReason: "scan_cap",
		Returned:         10,
	}))
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "kb") || !strings.Contains(joined, "r4") || !strings.Contains(joined, "bundle") {
		t.Fatalf("FormatTable missing rows:\n%s", joined)
	}
}

func TestReadReceiptMalformedAndOversizeDegrade(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if r, ok, err := ReadReceipt(dir); ok || err != nil || r != nil {
		t.Fatalf("missing receipt = (%v, %v, %v), want (nil, false, nil)", r, ok, err)
	}

	if err := os.WriteFile(filepath.Join(dir, ReceiptFile), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := ReadReceipt(dir); !ok || err == nil {
		t.Fatalf("malformed receipt: ok=%v err=%v, want ok=true err!=nil", ok, err)
	}
	if line := RenderInputsFooterLine(dir); line != "" {
		t.Fatalf("malformed receipt should render empty footer inputs line, got %q", line)
	}

	// Ensure oversized file (>maxReceiptBytes) returns an error instead of reading unbounded bytes.
	f, err := os.Create(filepath.Join(dir, ReceiptFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxReceiptBytes + 8); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	_ = f.Close()
	if _, ok, err := ReadReceipt(dir); !ok || err == nil {
		t.Fatalf("oversized receipt: ok=%v err=%v, want ok=true err!=nil", ok, err)
	}

	// EmptyReceipt must still serialize cleanly.
	b, err := json.Marshal(EmptyReceipt())
	if err != nil || !strings.Contains(string(b), `"schema_version":"1"`) {
		t.Fatalf("EmptyReceipt marshal: %q err=%v", string(b), err)
	}
}
