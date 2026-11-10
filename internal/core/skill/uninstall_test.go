package skill_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core/skill"
)

// TestUninstallRemovesOwnedAndKeepsForeign pins the foreign-file contract.
//
//nolint:revive // the test walks the removal arm
func TestUninstallRemovesOwnedAndKeepsForeign(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	req := newRequest(t)
	req.Dir = dir
	target := filepath.Join(dir, "verdict")
	if _, ref := skill.Install(context.Background(), req); ref != nil {
		t.Fatalf("seed install: %v", ref)
	}
	foreign := filepath.Join(target, "keep.txt")
	writeTestFile(t, foreign, "foreign bytes\n")

	rm, ref := skill.Uninstall(context.Background(), req)
	if ref != nil {
		t.Fatalf("Uninstall: %v", ref)
	}
	if rm.Status != skill.StatusRemoved || !rm.ReceiptRemoved {
		t.Fatalf("removal = %+v, want removed with the receipt", rm)
	}
	if !slices.Equal(rm.Kept, []string{"keep.txt"}) {
		t.Fatalf("kept = %v, want [keep.txt]", rm.Kept)
	}
	if got, err := os.ReadFile(foreign); err != nil || string(got) != "foreign bytes\n" {
		t.Fatalf("foreign file = %q, %v; want it untouched", got, err)
	}
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("uninstall left SKILL.md behind")
	}
	if _, err := os.Stat(filepath.Join(target, skill.ReceiptName)); !os.IsNotExist(err) {
		t.Fatal("uninstall left the receipt behind")
	}
}

// TestUninstallRefusesForeignWithoutForce pins the no-receipt refusal.
func TestUninstallRefusesForeignWithoutForce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	req := newRequest(t)
	req.Dir = dir
	target := filepath.Join(dir, "verdict")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(target, "keep.txt"), "foreign\n")

	_, ref := skill.Uninstall(context.Background(), req)
	if ref == nil || ref.Code != "skill-dir-foreign" {
		t.Fatalf("foreign refusal = %v, want skill-dir-foreign", ref)
	}
	if _, err := os.Stat(filepath.Join(target, "keep.txt")); err != nil {
		t.Fatal("refusal touched the foreign file")
	}

	req.DryRun = true
	rm, ref := skill.Uninstall(context.Background(), req)
	if ref != nil {
		t.Fatalf("dry-run foreign preview: %v", ref)
	}
	if rm.Status != skill.StatusBlocked || len(rm.Would) != 0 {
		t.Fatalf("dry-run foreign removal = %+v, want blocked with no inventory files", rm)
	}
}

// TestUninstallForceRemovesOnlyInventory pins the --force boundary.
func TestUninstallForceRemovesOnlyInventory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	req := newRequest(t)
	req.Dir = dir
	target := filepath.Join(dir, "verdict")
	if _, ref := skill.Install(context.Background(), req); ref != nil {
		t.Fatalf("seed install: %v", ref)
	}
	if err := os.Remove(filepath.Join(target, skill.ReceiptName)); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(target, "keep.txt"), "foreign\n")

	req.Force = true
	rm, ref := skill.Uninstall(context.Background(), req)
	if ref != nil {
		t.Fatalf("force uninstall: %v", ref)
	}
	if rm.ReceiptRemoved {
		t.Fatal("force uninstall claims to remove an absent receipt")
	}
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("force uninstall left a payload file behind")
	}
	if got, err := os.ReadFile(filepath.Join(target, "keep.txt")); err != nil || string(got) != "foreign\n" {
		t.Fatalf("force uninstall touched the foreign file: %q, %v", got, err)
	}
}

// TestUninstallDryRunWritesNothing pins the dry-run plan and its no-write.
func TestUninstallDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	req := newRequest(t)
	req.Dir = dir
	target := filepath.Join(dir, "verdict")
	if _, ref := skill.Install(context.Background(), req); ref != nil {
		t.Fatalf("seed install: %v", ref)
	}
	before := treeSnapshot(t, target)

	req.DryRun = true
	rm, ref := skill.Uninstall(context.Background(), req)
	if ref != nil {
		t.Fatalf("dry-run uninstall: %v", ref)
	}
	if rm.Status != skill.StatusRemove || len(rm.Removed) != 0 || len(rm.Would) == 0 {
		t.Fatalf("dry-run removal = %+v, want a plan and no removals", rm)
	}
	if after := treeSnapshot(t, target); !mapsEqual(before, after) {
		t.Fatal("dry-run uninstall changed the tree")
	}
}

// TestUninstallNotInstalled pins the absent-target refusal.
func TestUninstallNotInstalled(t *testing.T) {
	t.Parallel()
	req := newRequest(t)
	req.Dir = t.TempDir()
	_, ref := skill.Uninstall(context.Background(), req)
	if ref == nil || ref.Code != "skill-not-installed" {
		t.Fatalf("absent target refusal = %v, want skill-not-installed", ref)
	}
}

// TestUninstallRefusesConflictBeforeDeleting pins the preflight.
func TestUninstallRefusesConflictBeforeDeleting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	req := newRequest(t)
	req.Dir = dir
	target := filepath.Join(dir, "verdict")
	if _, ref := skill.Install(context.Background(), req); ref != nil {
		t.Fatalf("seed install: %v", ref)
	}
	if err := os.Remove(filepath.Join(target, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(target, "SKILL.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, ref := skill.Uninstall(context.Background(), req)
	if ref == nil || ref.Code != "skill-foreign-conflict" {
		t.Fatalf("conflict refusal = %v, want skill-foreign-conflict", ref)
	}
	if _, err := os.Stat(filepath.Join(target, "prompts", "review.md")); err != nil {
		t.Fatal("conflict refusal deleted an earlier payload file")
	}
}

// TestUninstallRefusesPayloadDirEscapingTheTarget pins the root boundary: an
// inventory subdirectory that links outside the target is a conflict in every
// mode and never touches the file it reaches.
func TestUninstallRefusesPayloadDirEscapingTheTarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	req := newRequest(t)
	req.Dir = dir
	target := filepath.Join(dir, "verdict")
	if _, ref := skill.Install(context.Background(), req); ref != nil {
		t.Fatalf("seed install: %v", ref)
	}
	outside := filepath.Join(dir, "outside")
	outsideFile := filepath.Join(outside, "review.md")
	writeTestFile(t, outsideFile, "outside bytes\n")
	if err := os.RemoveAll(filepath.Join(target, "prompts")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(target, "prompts")); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}
	modes := []struct {
		name  string
		force bool
		dry   bool
	}{
		{name: "receipt-owned"},
		{name: "force", force: true},
		{name: "dry-run", dry: true},
	}
	for _, mode := range modes {
		at := *req
		at.Force = mode.force
		at.DryRun = mode.dry
		assertEscapeRefusal(t, &at, mode.name, target, outsideFile)
	}
}

func assertEscapeRefusal(t *testing.T, req *skill.Request, label, target, outsideFile string) {
	t.Helper()
	_, ref := skill.Uninstall(context.Background(), req)
	if got, err := os.ReadFile(outsideFile); err != nil || string(got) != "outside bytes\n" {
		t.Fatalf("%s outside file = %q, %v; want it untouched", label, got, err)
	}
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); err != nil {
		t.Fatalf("%s removed a payload file before refusing", label)
	}
	if _, err := os.Stat(filepath.Join(target, skill.ReceiptName)); err != nil {
		t.Fatalf("%s removed the receipt before refusing", label)
	}
	if ref == nil || ref.Code != "skill-foreign-conflict" {
		t.Fatalf("%s refusal = %v, want skill-foreign-conflict", label, ref)
	}
	if !strings.Contains(ref.Message, "path escapes from parent") {
		t.Fatalf("%s message = %q, want the root escape named", label, ref.Message)
	}
}

// TestUninstallRefusesSymlinkedTarget pins the symlink wording and that the
// directory behind the link is untouched.
func TestUninstallRefusesSymlinkedTarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	req := newRequest(t)
	req.Dir = dir
	realDir := filepath.Join(dir, "real-skill")
	keep := filepath.Join(realDir, "keep.txt")
	writeTestFile(t, keep, "keep\n")
	target := filepath.Join(dir, "verdict")
	if err := os.Symlink(realDir, target); err != nil {
		t.Skipf("this platform cannot create symlinks here: %v", err)
	}

	_, ref := skill.Uninstall(context.Background(), req)
	if ref == nil || ref.Code != "skill-target-file" {
		t.Fatalf("symlinked target refusal = %v, want skill-target-file", ref)
	}
	if want := target + " is a symlink; refusing to remove through it"; ref.Message != want {
		t.Fatalf("symlinked target message = %q, want %q", ref.Message, want)
	}
	if got, err := os.ReadFile(keep); err != nil || string(got) != "keep\n" {
		t.Fatalf("symlinked target file = %q, %v; want it untouched", got, err)
	}
}

// TestUninstallAgents pins one removal per named agent.
//
//nolint:revive // the test walks the multi-agent arm
func TestUninstallAgents(t *testing.T) {
	t.Parallel()
	req := newRequest(t)
	home := req.Env["HOME"]
	env := map[string]string{"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config")}
	for _, agent := range []string{"claude", "cursor"} {
		at := newRequest(t)
		at.Env = env
		at.Agent = agent
		if _, ref := skill.Install(context.Background(), at); ref != nil {
			t.Fatalf("seed %s install: %v", agent, ref)
		}
	}
	req.Env = env
	set, ref := skill.UninstallAgents(context.Background(), req, []string{"cursor", "claude"})
	if ref != nil {
		t.Fatalf("UninstallAgents: %v", ref)
	}
	if want := []string{"cursor", "claude"}; !slices.Equal(set.Agents, want) {
		t.Fatalf("agents = %v, want %v", set.Agents, want)
	}
	for i := range set.Targets {
		if set.Targets[i].Status != skill.StatusRemoved {
			t.Fatalf("target %d = %+v, want removed", i, set.Targets[i])
		}
		if _, err := os.Stat(set.Targets[i].Target); !os.IsNotExist(err) {
			t.Fatalf("target %s survived an empty uninstall", set.Targets[i].Target)
		}
	}
}
