package recordcore_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
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
	writeFile(t, ".review/local-dry/findings.json", "[]\n")
	writeFile(t, ".review/local-dry/record.json", `{"old":true}`)
	c := &collector{}
	req := recordRequest(t, c, "local-dry")
	req.DryRun = true
	before := snapshotTree(t, ".review/local-dry")
	resp, ref := recordcore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	after := snapshotTree(t, ".review/local-dry")
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
	if resp.Data.Check == nil || resp.Data.Check.Comments != 1 || resp.Data.Check.Sections != 0 {
		t.Errorf("data.check = %+v", resp.Data.Check)
	}
	if resp.Data.Findings.Open != 1 || len(resp.Data.Items) != 1 {
		t.Errorf("dry-run summary = %+v", resp.Data)
	}
}

// TestDryRunAndRecordShareProblems proves both modes refuse with byte-identical
// review-input problems and write nothing.
func TestDryRunAndRecordShareProblems(t *testing.T) {
	bad := `{"schema_version":"1","body":"` + checkBody + `","approved":false,` +
		`"comments":[{"path":"notes.txt","line":2,"body":"x","rigor":"walked","brief":{"outcome":"Reject duplicate deliveries before anything is enqueued.","questions":"why"}}]}`
	bundle(t, "local-same", bad)
	c := &collector{}
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

// TestStrictPromotesWarnings proves --strict and controls strict_input turn a
// warning-only review into a refusal; without them it records and reports.
//
//nolint:gocognit,revive // the strict matrix reads as one block
func TestStrictPromotesWarnings(t *testing.T) {
	warn := `{"schema_version":"1","body":"` + checkBody + `","approved":false,` +
		`"comments":[{"path":"notes.txt","line":2,"body":"A finding with enough substance."}]}`
	cases := []struct {
		name    string
		strict  bool
		control bool
	}{
		{name: "flag", strict: true},
		{name: "control", control: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bundle(t, "local-strict", warn)
			c := &collector{}
			req := recordRequest(t, c, "local-strict")
			req.DryRun = true
			if tc.strict {
				req.Strict = true
			}
			if tc.control {
				setStrictControl(t, ".review/local-strict/controls.json")
			}
			if _, ref := recordcore.Run(context.Background(), req); ref == nil {
				t.Fatal("strict warning-only input recorded")
			} else if ref.Code != "bad-review-input" || len(ref.Problems) == 0 || ref.Problems[0].Level != core.ProblemLevelError {
				t.Errorf("refusal = %+v", ref)
			}
		})
	}
	t.Run("lenient", func(t *testing.T) {
		bundle(t, "local-lenient", warn)
		c := &collector{}
		req := recordRequest(t, c, "local-lenient")
		req.DryRun = true
		resp, ref := recordcore.Run(context.Background(), req)
		if ref != nil {
			t.Fatalf("warning-only input refused: %v", ref)
		}
		if !strings.Contains(strings.Join(resp.Warnings, "\n"), "rigor is unset") {
			t.Errorf("warnings = %v", resp.Warnings)
		}
	})
}

func setStrictControl(t *testing.T, path string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["strict_input"] = true
	updated, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, updated, 0o644); err != nil {
		t.Fatal(err)
	}
}
