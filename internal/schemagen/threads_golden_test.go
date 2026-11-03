package schemagen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestThreadsGoldensValidate proves every checked-in threads.json golden
// (written by the fetch parity suite for all six hosts) validates against the
// published schema. It walks the golden snapshots rather than one sample, so
// a mapper field the schema forgot fails here instead of surprising a reader.
//
//nolint:revive // the golden walk reads as one assertion loop
func TestThreadsGoldensValidate(t *testing.T) {
	t.Parallel()
	doc := loadSchemaDocument(t, "threads.schema.json")
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("RepoRoot: %v", err)
	}
	files, err := filepath.Glob(filepath.Join(root, "internal", "fetch", "testdata", "*.golden"))
	if err != nil {
		t.Fatalf("glob goldens: %v", err)
	}
	validated := 0
	for _, path := range files {
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			t.Fatalf("read %s: %v", path, rerr)
		}
		for _, block := range goldenThreadsBlocks(string(raw)) {
			var v any
			// The parity harness scrubs timestamps (0-valued time), so the
			// date-time format is restored before validation: the runtime
			// feed is covered by TestRuntimeThreadsOutputValidates.
			block = strings.ReplaceAll(block, "0000-00-00T00:00:00Z", "2026-01-01T00:00:00Z")
			dec := json.NewDecoder(strings.NewReader(block))
			dec.UseNumber()
			if derr := dec.Decode(&v); derr != nil {
				t.Errorf("%s: threads.json golden is not JSON: %v", path, derr)
				continue
			}
			if serr := validationError(doc, doc, v); serr != nil {
				t.Errorf("%s: threads.json golden does not validate: %v", path, serr)
			}
			validated++
		}
	}
	if validated < 20 {
		t.Fatalf("validated %d threads.json goldens, want at least 20 (did the golden format change?)", validated)
	}
}

// goldenThreadsBlocks extracts every threads.json file block from one parity
// golden snapshot.
func goldenThreadsBlocks(raw string) []string {
	const marker = "--- file C/"
	var out []string
	for _, part := range strings.Split(raw, marker)[1:] {
		name, body, ok := strings.Cut(part, " ---\n")
		if !ok || !strings.HasSuffix(name, "/threads.json") {
			continue
		}
		out = append(out, strings.TrimSuffix(body, "\n"))
	}
	return out
}
