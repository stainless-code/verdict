package skill_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/core/skill"
)

// TestPanelSkillLifecycle removes the installed panel reference directory.
func TestPanelSkillLifecycle(t *testing.T) {
	req := newRequest(t)
	req.Dir = t.TempDir()
	if _, ref := skill.Install(context.Background(), req); ref != nil {
		t.Fatal(ref)
	}
	target := filepath.Join(req.Dir, "verdict")
	raw, err := os.ReadFile(filepath.Join(target, "references/panel.md"))
	if err != nil || len(raw) == 0 {
		t.Fatalf("installed panel reference=%q, %v", raw, err)
	}
	if _, ref := skill.Uninstall(context.Background(), req); ref != nil {
		t.Fatal(ref)
	}
	if _, err := os.Stat(target); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("uninstall left target behind: %v", err)
	}
}
