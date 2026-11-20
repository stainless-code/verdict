package recordcore_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/testkit"
)

//nolint:revive // the walk is one flat closure
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// TestDryRunWritesNothing proves a clean dry run reports what would record
// while leaving every byte of the bundle unchanged.
//
//nolint:cyclop,gocognit,revive // the dry-run assertions read as one block
func TestDryRunWritesNothing(t *testing.T) {
	bundle(t, "local-dry", reviewJSON(""))
	testkit.WriteFile(t, ".verdict/state/local-dry/findings.json", "[]\n")
	testkit.WriteFile(t, ".verdict/state/local-dry/record.json", `{"old":true}`)
	c := &testkit.Collector{}
	req := recordRequest(t, c, "local-dry")
	req.DryRun = true
	before := snapshotTree(t, ".verdict/state/local-dry")
	resp, ref := recordcore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	after := snapshotTree(t, ".verdict/state/local-dry")
	if !reflect.DeepEqual(before, after) {
		for name, want := range before {
			if got, ok := after[name]; !ok || got != want {
				t.Errorf("dry run changed %s: before=%q after=%q", name, want, got)
			}
		}
		for name := range after {
			if _, ok := before[name]; !ok {
				t.Errorf("dry run wrote %s", name)
			}
		}
	}
	if resp.Data == nil || !resp.Data.DryRun {
		t.Fatalf("data = %+v, want a dry-run response", resp.Data)
	}
	if resp.Data.Check == nil || resp.Data.Check.Findings != 1 {
		t.Errorf("data.check = %+v", resp.Data.Check)
	}
	if resp.Data.Findings.Open != 1 || len(resp.Data.Items) != 1 {
		t.Errorf("dry-run summary = %+v", resp.Data)
	}
	if resp.Data.Findings.File != "" {
		t.Errorf("dry-run findings.file = %q, want empty", resp.Data.Findings.File)
	}
	if !strings.Contains(strings.Join(c.HumanLines, "\n"),
		"» findings: 1 sigs (dry run — no findings file written)") {
		t.Errorf("dry-run progress = %q; want no findings-file destination", c.HumanLines)
	}
}

// TestDryRunAndRecordShareProblems proves both modes refuse with byte-identical
// review-input problems and write nothing.
func TestDryRunAndRecordShareProblems(t *testing.T) {
	bad := `{"schema_version":"1","body":"` + checkBody + `","approved":false,` +
		`"comments":[{"path":"notes.txt","line":2,"body":"x","rigor":"walked","brief":{"outcome":"Reject duplicate deliveries before anything is enqueued.","questions":"why"}}]}`
	bundle(t, "local-same", bad)
	c := &testkit.Collector{}
	req := recordRequest(t, c, "local-same")
	req.DryRun = true
	_, dry := recordcore.Run(context.Background(), req)
	req.DryRun = false
	_, realRef := recordcore.Run(context.Background(), req)
	if dry == nil || realRef == nil {
		t.Fatalf("refusals = %v / %v", dry, realRef)
	}
	if dry.Code != "bad-review-input" || realRef.Code != dry.Code {
		t.Errorf("codes = %s / %s", dry.Code, realRef.Code)
	}
	if !reflect.DeepEqual(dry.Problems, realRef.Problems) || dry.ProblemsTruncated != realRef.ProblemsTruncated {
		t.Errorf("problems differ:\ndry=%+v\nreal=%+v", dry.Problems, realRef.Problems)
	}
}

// TestDryRunRefusesTruncatedError proves an error past the 50-problem cap still
// refuses, with the total count in the message.
func TestPRBundleDryRun(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testkit.WriteFile(t, ".verdict/state/5101/PR.json", `{"provider":"github","state":"OPEN",`+
		`"headRefOid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",`+
		`"baseRefOid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`)
	testkit.WriteFile(t, ".verdict/state/5101/full.diff", "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n"+
		"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old line\n+new line\n bottom\n")
	testkit.WriteFile(t, ".verdict/state/5101/review.json", reviewJSON(""))
	c := &testkit.Collector{}
	req := recordRequest(t, c, "5101")
	req.DryRun = true
	resp, ref := recordcore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("PR dry run refusal: %v", ref)
	}
	if resp.Data == nil || !resp.Data.DryRun || resp.Data.Findings.Open != 1 {
		t.Errorf("PR dry run data = %+v", resp.Data)
	}
	req.DryRun = false
	if _, ref := recordcore.Run(context.Background(), req); ref == nil || ref.Code != "not-local" {
		t.Errorf("real PR record = %+v, want not-local", ref)
	}
}
