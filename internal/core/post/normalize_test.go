package postcore

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
)

// normalizedParityReview is a weak-model review: fenced, with aliased keys
// and a rigor synonym. Record repairs it; post must merge the same object.
const normalizedParityReview = "```json\n" +
	`{"schema_version":"1","body":"> ✅ Looks good — minor note below.","approved":false,` +
	`"comments":[{"file":"notes.txt","line":2,"body":"Consider clarifying this line.","rung":"traced"}]}` +
	"\n```"

// TestPostReadsNormalizedReview proves post merges the same normalized
// review record checks and stores: an aliased, fenced review records one
// finding via record and the identical finding via post, with no duplicate
// or null-path entries and no unanchored section entry.
func TestPostReadsNormalizedReview(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeParityBundles(t)
	recorded := runParityRecord(t)
	posted := runParityPost(t)
	checkParityEntries(t, recorded, posted)
}

// writeParityBundles stages the same weak-model review in a local bundle
// for record and a PR bundle for post.
func writeParityBundles(t *testing.T) {
	t.Helper()
	postBundle(t, normalizedParityReview)
	if err := writeTestFile(".review/local-par/source.json",
		`{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`); err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile(".review/local-par/full.diff",
		"diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n"+
			"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile(".review/local-par/review.json", normalizedParityReview); err != nil {
		t.Fatal(err)
	}
	seedPostControls(t, ".review/local-par", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
}

// runParityRecord records the local bundle and returns its findings.
func runParityRecord(t *testing.T) []map[string]any {
	t.Helper()
	root := ""
	read := core.ReadRequest{
		Runtime: &core.Runtime{
			Now:      func() time.Time { return postTestTime },
			Progress: testProgress{warnings: &[]string{}},
		},
		Repo: "acme/app", Pr: "local-par",
		Forces: &core.GlobalForces{}, Root: &root,
	}
	recReq := &core.RecordRequest{ReadRequest: read, Effective: core.Defaults()}
	if _, ref := recordcore.Run(context.Background(), recReq); ref != nil {
		t.Fatalf("record refusal: %v", ref)
	}
	return readFindingEntries(t, ".review/local-par/findings.json")
}

// runParityPost dry-runs post over the PR bundle and returns its findings.
func runParityPost(t *testing.T) []map[string]any {
	t.Helper()
	cmdr := postCommander(t)
	var warnings, human []string
	postReq := postTestReq(cmdr, core.PostFlags{Approve: "false", ReviewFile: ".review/7/review.json", DryRun: true}, &warnings, &human)
	if _, ref := Run(context.Background(), postReq); ref != nil {
		t.Fatalf("post dry-run refusal: %v", ref)
	}
	return readFindingEntries(t, ".review/7/findings.dryrun.json")
}

// checkParityEntries proves both verbs stored one identical inline finding:
// the alias resolved to notes.txt with the walked rung, and no section or
// null-path entry appeared.
func checkParityEntries(t *testing.T, recorded, posted []map[string]any) {
	t.Helper()
	if len(recorded) != 1 || len(posted) != 1 {
		t.Fatalf("recorded = %d, posted = %d, want 1 and 1: %v / %v", len(recorded), len(posted), recorded, posted)
	}
	for _, entries := range []map[string]any{recorded[0], posted[0]} {
		checkParityEntry(t, entries)
	}
	if recorded[0]["sig"] != posted[0]["sig"] {
		t.Errorf("record sig %v != post sig %v: verbs read different reviews", recorded[0]["sig"], posted[0]["sig"])
	}
	recordedJSON, err := json.Marshal(recorded[0])
	if err != nil {
		t.Fatalf("marshal record entry: %v", err)
	}
	postedJSON, err := json.Marshal(posted[0])
	if err != nil {
		t.Fatalf("marshal post entry: %v", err)
	}
	if !bytes.Equal(recordedJSON, postedJSON) {
		t.Errorf("record entry differs from post entry:\nrecord: %s\npost:   %s", recordedJSON, postedJSON)
	}
}

// checkParityEntry proves one stored finding resolved its alias to notes.txt
// with the walked rung, and is not a spurious section entry.
func checkParityEntry(t *testing.T, entries map[string]any) {
	t.Helper()
	if entries["path"] != "notes.txt" {
		t.Errorf("entry path = %v, want notes.txt (a null path means post read the raw aliases)", entries["path"])
	}
	if entries["rigor"] != "walked" {
		t.Errorf("entry rigor = %v, want walked", entries["rigor"])
	}
	if entries["kind"] == "section" {
		t.Errorf("spurious section entry: %v", entries)
	}
}

// readFindingEntries decodes one findings store into generic maps.
func readFindingEntries(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("decode %s: %v\n%s", path, err, raw)
	}
	return entries
}
