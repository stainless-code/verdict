package skill_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/core/skill"
)

// TestShowFilePathIsInventoryBytes pins the byte-identical path read.
func TestShowFilePathIsInventoryBytes(t *testing.T) {
	t.Parallel()
	req := newRequest(t)
	for _, rel := range []string{"SKILL.md", "prompts/review.md", "scripts/verdict"} {
		want, err := skillfs.Read(rel)
		if err != nil {
			t.Fatal(err)
		}
		got, ref := skill.ShowFile(context.Background(), req, rel)
		if ref != nil {
			t.Fatalf("ShowFile(%s): %v", rel, ref)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("ShowFile(%s) is not byte-identical to the embedded payload", rel)
		}
	}
}

// TestShowFilePathRefusesOutsideInventory pins the single typed refusal: a
// traversal, absolute, cleaned-away, or unknown path never reaches the
// embedded reader.
func TestShowFilePathRefusesOutsideInventory(t *testing.T) {
	t.Parallel()
	req := newRequest(t)
	for _, path := range []string{"../SKILL.md", "/etc/passwd", "prompts/../SKILL.md", "prompts//review.md", "nope.md", ""} {
		_, ref := skill.ShowFile(context.Background(), req, path)
		if ref == nil || ref.Code != "skill-path-not-inventory" {
			t.Errorf("ShowFile(%q) refusal = %v, want skill-path-not-inventory", path, ref)
		}
	}
}

// treeSnapshot captures every file under root for a read-only comparison.
//
//nolint:revive // fixture snapshotting walks the tree
func treeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		snapshot[rel] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
