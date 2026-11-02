package record_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ovGood is the minimal valid local bundle for the override legs.
func ovGood() map[string]string {
	return map[string]string{
		".review/local-ov/source.json": `{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","head_ref":"work"}`,
		".review/local-ov/full.diff":   genDiff(2),
		".review/local-ov/review.json": `{"schema_version":"1","body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[]}`,
	}
}

// TestParityOverridesExtra checks the remaining override branches: a
// directory in place of a fragment, duplicate keys, the `=` threshold form,
// and a locked-block marker variant.
func TestParityOverridesExtra(t *testing.T) {
	runBoth(t, "overrides/dir-as-fragment", "local-ov", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, ovGood())
		if err := os.MkdirAll(filepath.Join(dir, ".review/overrides/preamble.md"), 0o755); err != nil {
			t.Fatal(err)
		}
	})
	runBoth(t, "overrides/duplicate-footer", "local-ov", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, ovGood())
		writeFiles(t, dir, map[string]string{".review/overrides/footer-order.md": strings.Repeat("link\n", 2)})
	})
	runBoth(t, "overrides/duplicate-threshold", "local-ov", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, ovGood())
		writeFiles(t, dir, map[string]string{".review/overrides/walkthrough-threshold.md": "files: 3\nfiles: 4\n"})
	})
	runBoth(t, "overrides/eq-threshold", "local-ov", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, ovGood())
		writeFiles(t, dir, map[string]string{".review/overrides/walkthrough-threshold.md": "files=3\n"})
	})
	runBoth(t, "overrides/locked-marker", "local-ov", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, ovGood())
		writeFiles(t, dir, map[string]string{".review/overrides/preamble.md": "> ✅ verdict marker stays first\n"})
	})
}

// TestParityBodies checks body-shape garnish parity: non-ASCII content,
// a carried rendered footer, and an agent mermaid block.
func TestParityBodies(t *testing.T) {
	runBoth(t, "body/non-ascii", "local-dg", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, map[string]string{
			".review/local-dg/source.json": `{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","head_ref":"work"}`,
			".review/local-dg/full.diff":   genDiff(2),
			".review/local-dg/review.json": "{\"schema_version\":\"1\",\"body\":\"> ✅ ünïcodé marker plus enough length to pass.\",\"approved\":false,\"comments\":[{\"path\":\"notes.txt\",\"line\":2,\"side\":\"RIGHT\",\"body\":\"🎉 fix the thïng with a non-breaking space.\"}]}",
		})
	}, "--json")
	runBoth(t, "body/receipt-collision", "local-dg", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, map[string]string{
			".review/local-dg/source.json": `{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","head_ref":"work"}`,
			".review/local-dg/full.diff":   genDiff(2),
			".review/local-dg/review.json": "{\"schema_version\":\"1\",\"body\":\"> ✅ marker plus enough length to pass the guard ok.\\n\\n<!-- verdict-footer -->\\n> old receipt\\n\",\"approved\":false,\"comments\":[]}",
		})
	}, "--json")
	runBoth(t, "body/mermaid-stripped", "local-dg", func(t *testing.T, dir string) {
		t.Helper()
		writeFiles(t, dir, map[string]string{
			".review/local-dg/source.json": `{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","head_ref":"work"}`,
			".review/local-dg/full.diff":   genDiff(2),
			".review/local-dg/review.json": "{\"schema_version\":\"1\",\"body\":\"> ✅ marker plus enough length to pass the guard ok.\\n\\n### Flow\\n\\n```mermaid\\nflowchart TD\\n    A --> B\\n```\\n\",\"approved\":false,\"comments\":[]}",
		})
	}, "--json")
}

// TestParityChunkDrift checks the pinned-snapshot drift warning (exit 0).
func TestParityChunkDrift(t *testing.T) {
	chunkDiff := func(body string) string {
		return "diff --git a/c1.txt b/c1.txt\nindex 1111111..2222222 100644\n--- a/c1.txt\n+++ b/c1.txt\n@@ -1,2 +1,2 @@\n a\n" + body + "\n"
	}
	runBoth(t, "chunk/drift", "local-ch", func(t *testing.T, dir string) {
		t.Helper()
		c1 := chunkDiff("-old1\n+new1")
		c2 := chunkDiff("-old2\n+new2")
		writeFiles(t, dir, map[string]string{
			".review/local-ch/source.json":          `{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","head_ref":"work"}`,
			".review/local-ch/full.diff":            c1 + c2 + "# drifted after fetch\n",
			".review/local-ch/review.json":          `{"schema_version":"1","chunks":["a","b"],"body":"> ✅ marker plus enough length to pass the guard ok.","approved":false,"comments":[]}`,
			".review/local-ch/chunks/manifest.json": `{"schema_version":"1","chunks":[{"name":"a","index":0,"diff":".review/local-ch/chunks/a.diff","files":["c1.txt"]},{"name":"b","index":1,"diff":".review/local-ch/chunks/b.diff","files":["c1.txt"]}]}`,
			".review/local-ch/chunks/a.diff":        c1,
			".review/local-ch/chunks/b.diff":        c2,
		})
	})
}
