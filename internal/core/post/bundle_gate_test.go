package postcore

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
)

const missingBundleHint = "try: run the fetch/record step named above first, then re-run"

type bundleGateCase struct {
	name     string
	flags    core.PostFlags
	setup    func(t *testing.T)
	wantCode string
	wantMsg  []string
}

// TestRunGatesBeforeStoreBackfill pins the bundle gates ahead of the store
// backfill: an unfetched PR refuses typed, and no refusal leaves a file or
// directory behind.
func TestRunGatesBeforeStoreBackfill(t *testing.T) {
	closed := func(*testing.T) {}
	dirOnly := func(t *testing.T) { t.Helper(); mkdirTest(t, ".verdict/state/7") }
	noDiff := func(t *testing.T) { t.Helper(); writeTestFileOrFail(t, ".verdict/state/7/review.json", postReview) }
	review := ".verdict/state/7/review.json"
	for _, tc := range []bundleGateCase{
		{
			name:     "no bundle dir",
			flags:    core.PostFlags{Approve: "false", ReviewFile: review},
			setup:    closed,
			wantCode: codeMissingBundle,
			wantMsg:  []string{"missing .verdict/state/7 ", "run fetch acme/app 7 first"},
		},
		{
			name:     "no bundle dir on dry run",
			flags:    core.PostFlags{Approve: "false", ReviewFile: review, DryRun: true},
			setup:    closed,
			wantCode: codeMissingBundle,
			wantMsg:  []string{"missing .verdict/state/7 ", "run fetch acme/app 7 first"},
		},
		{
			name:     "bundle dir without review",
			flags:    core.PostFlags{Approve: "false", ReviewFile: review},
			setup:    dirOnly,
			wantCode: codeMissingReviewFile,
			wantMsg:  []string{"missing .verdict/state/7/review.json"},
		},
		{
			name:     "bundle without diff",
			flags:    core.PostFlags{Approve: "false", ReviewFile: review},
			setup:    noDiff,
			wantCode: codeMissingDiff,
			wantMsg:  []string{"missing .verdict/state/7/full.diff"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			tc.setup(t)
			before := treePaths(t, ".")
			var warnings, human []string
			_, ref := Run(context.Background(), postTestReq(postCommander(t), tc.flags, &warnings, &human))
			checkGateRefusal(t, ref, &tc)
			if after := treePaths(t, "."); !slices.Equal(before, after) {
				t.Errorf("refused run changed the tree: %v -> %v", before, after)
			}
		})
	}
}

func checkGateRefusal(t *testing.T, ref *core.RefusalError, tc *bundleGateCase) {
	t.Helper()
	if ref == nil {
		t.Fatalf("no refusal, want code %q", tc.wantCode)
	}
	if ref.Code != tc.wantCode {
		t.Errorf("code = %q, want %q (message %q)", ref.Code, tc.wantCode, ref.Message)
	}
	for _, piece := range tc.wantMsg {
		if !strings.Contains(ref.Message, piece) {
			t.Errorf("message %q lacks %q", ref.Message, piece)
		}
	}
	if got := core.HintFor(tc.wantCode); got != missingBundleHint {
		t.Errorf("hint for %q = %q, want %q", tc.wantCode, got, missingBundleHint)
	}
}

func mkdirTest(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeTestFileOrFail(t *testing.T, path, content string) {
	t.Helper()
	if err := writeTestFile(path, content); err != nil {
		t.Fatal(err)
	}
}

func treePaths(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	if err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != root {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	slices.Sort(paths)
	return paths
}
