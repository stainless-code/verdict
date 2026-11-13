package fetchcore

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/exclude"
)

const (
	generatedSection = "diff --git a/vendor/.env b/vendor/.env\n--- a/vendor/.env\n+++ b/vendor/.env\n@@ -1 +1 @@\n-a\n+b\n"
	keptSection      = "diff --git a/app_test.go b/app_test.go\n--- a/app_test.go\n+++ b/app_test.go\n@@ -1 +1 @@\n-a\n+b"
)

func TestExcludedDiffReceipts(t *testing.T) {
	raw := generatedSection + keptSection
	filtered, receipt := excludedDiff(raw, exclude.Defaults(), nil, nil)
	if filtered != keptSection {
		t.Fatalf("filtered = %q; want test section byte-exact", filtered)
	}
	if len(receipt.Excluded) != 1 || receipt.Excluded[0].Path != "vendor/.env" || receipt.Excluded[0].RuleID != "default:generated" {
		t.Fatalf("receipt = %+v", receipt)
	}
	if receipt.Excluded[0].Bytes != 93 || receipt.ExcludedBytes != 93 {
		t.Fatalf("bytes = %d; want 93", receipt.ExcludedBytes)
	}
	hits := sensAllHits(raw, "")
	if len(hits) == 0 || hits[0].path != "vendor/.env" {
		t.Fatal("excluded credentials missing from raw sensitive scan")
	}
}

func TestAttributeExclusionsAndReinclude(t *testing.T) {
	attrs := []byte("*.go linguist-generated=true\napp_test.go -linguist-generated\n")
	filtered, receipt := excludedDiff(keptSection, nil, nil, attrs)
	if filtered != keptSection || len(receipt.Excluded) != 0 {
		t.Fatal("false attribute did not override true")
	}
	filtered, receipt = excludedDiff(keptSection, nil, nil, []byte("*.go linguist-vendored\n"))
	if filtered != "" || len(receipt.Excluded) != 1 || receipt.Excluded[0].RuleID != "default:attribute" {
		t.Fatal("attribute not excluded")
	}
	rules, err := exclude.ParseIgnore([]byte("!vendor/**"), "ignore")
	if err != nil {
		t.Fatal(err)
	}
	filtered, _ = excludedDiff(generatedSection, exclude.Defaults(), rules, nil)
	if filtered != generatedSection {
		t.Fatal("explicit negation failed to override default")
	}
}

func TestRawDiffPreservedAndFilteredChunks(t *testing.T) {
	dir := physicalTempDir(t)
	raw := generatedSection + keptSection
	if err := os.WriteFile(dir+"/full.diff", []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	warnings := []string{}
	fc := &fetchRunCtx{dir: dir, runner: &session{warnings: &warnings}}
	if err := filterExcluded(fc); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dir + "/full.raw.diff")
	if err != nil || string(got) != raw {
		t.Fatalf("raw changed: %q, %v", got, err)
	}
	got, err = os.ReadFile(dir + "/full.diff")
	if err != nil || string(got) != keptSection {
		t.Fatalf("filtered = %q, %v", got, err)
	}
	if !strings.Contains(exclusionTOC(dir), "vendor/.env (default:generated, 93 bytes)") {
		t.Fatal("TOC lacks receipt")
	}
	checkFilteredChunks(t, dir)
}

func checkFilteredChunks(t *testing.T, dir string) {
	t.Helper()
	if _, err := maybeWriteChunks(io.Discard, dir, 200, true, nil, "", "", "test", nil); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(dir + "/chunks/*.diff")
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for _, file := range files {
		part, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		all.Write(part)
	}
	if all.String() != keptSection {
		t.Fatalf("chunks = %q", all.String())
	}
}

func physicalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestIgnoreTooLargeRefusal(t *testing.T) {
	if ref := ignoreRefusal(exclude.ErrTooLarge); ref.Code != "ignore-too-large" {
		t.Fatalf("refusal = %v; want ignore-too-large", ref)
	}
}

func TestFetchExclusionOverrides(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		ignore     []string
		off        bool
	}{
		{name: "no-defaults", want: generatedSection + keptSection, off: true},
		{name: "ordered-cli", ignore: []string{"**", "!app_test.go"}, want: keptSection},
		{name: "cli-without-defaults", ignore: []string{"app_test.go"}, off: true, want: generatedSection},
	} {
		t.Run(tc.name, func(t *testing.T) { checkFetchExclusionOverride(t, tc.ignore, tc.off, tc.want) })
	}
}

func checkFetchExclusionOverride(t *testing.T, ignore []string, off bool, want string) {
	t.Helper()
	dir := physicalTempDir(t)
	if err := os.WriteFile(dir+"/full.diff", []byte(generatedSection+keptSection), 0o600); err != nil {
		t.Fatal(err)
	}
	warnings := []string{}
	fc := &fetchRunCtx{dir: dir, runner: &session{warnings: &warnings}, ignore: ignore, noDefaultExcludes: off}
	if err := filterExcluded(fc); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dir + "/full.diff")
	if err != nil || string(raw) != want {
		t.Fatalf("filtered = %q, %v; want %q", raw, err, want)
	}
	assertOverrideReceipt(t, dir, ignore, off)
}

func assertOverrideReceipt(t *testing.T, dir string, ignore []string, off bool) {
	t.Helper()
	raw, err := os.ReadFile(dir + "/excluded.json")
	if err != nil {
		t.Fatal(err)
	}
	var receipt ExcludedFile
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.NoDefaultExcludes != off || strings.Join(receipt.Ignore, "|") != strings.Join(ignore, "|") {
		t.Fatalf("override receipt = %+v", receipt)
	}
}
