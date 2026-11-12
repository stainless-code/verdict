package skill_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/skill"
)

func fixedNow() time.Time {
	return time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
}

func newRequest(t *testing.T) *skill.Request {
	t.Helper()
	return &skill.Request{
		Runtime: &core.Runtime{Env: map[string]string{"HOME": t.TempDir()}, Now: fixedNow},
		Version: "1.2.3",
	}
}

func listFiles(t *testing.T) skill.Inventory {
	t.Helper()
	inv, ref := skill.List(context.Background(), newRequest(t))
	if ref != nil {
		t.Fatalf("List: %v", ref)
	}
	return inv
}

// TestShowAndList pins the embedded read surface: SKILL.md bytes and the
// sorted, complete payload inventory.
//
//nolint:revive // the test walks the embedded read surface
func TestShowAndList(t *testing.T) {
	t.Parallel()
	body, ref := skill.Show(context.Background(), newRequest(t))
	if ref != nil {
		t.Fatalf("Show: %v", ref)
	}
	want, err := skillfs.Read(skillfs.SkillMD)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, want) {
		t.Fatal("Show did not return the embedded SKILL.md")
	}
	inv := listFiles(t)
	if inv.Name != "verdict" || inv.Version != "1.2.3" {
		t.Fatalf("inventory = %q v%s", inv.Name, inv.Version)
	}
	if !slices.IsSorted(inv.Files) {
		t.Fatal("inventory files are not sorted")
	}
	for _, path := range []string{"SKILL.md", "prompts/review.md", "schemas/envelope.schema.json", "scripts/verdict"} {
		if !slices.Contains(inv.Files, path) {
			t.Errorf("inventory misses %s", path)
		}
	}
}

// TestInstallCreateDryRunApply covers the happy path: dry-run touches
// nothing, apply writes every byte with the payload modes, and a re-run is
// keep.
//
//nolint:cyclop,gocognit,revive // the test is the create/apply matrix
func TestInstallCreateDryRunApply(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	req := newRequest(t)
	req.Dir = dir
	target := filepath.Join(dir, "verdict")

	req.DryRun = true
	plan, ref := skill.Install(context.Background(), req)
	if ref != nil {
		t.Fatalf("dry-run: %v", ref)
	}
	if plan.Status != skill.StatusCreate || plan.Written != 0 {
		t.Fatalf("dry-run plan = %+v", plan)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("dry-run created the target")
	}

	req.DryRun = false
	plan, ref = skill.Install(context.Background(), req)
	if ref != nil {
		t.Fatalf("install: %v", ref)
	}
	if plan.Status != skill.StatusCreate || plan.Written != len(plan.Files) {
		t.Fatalf("install plan = %+v", plan)
	}
	for _, rel := range plan.Files {
		want, err := skillfs.Read(rel)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read installed %s: %v", rel, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("installed %s differs from the embedded bytes", rel)
		}
	}
	shim, err := os.Stat(filepath.Join(target, "scripts", "verdict"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && shim.Mode().Perm()&0o111 == 0 { // NTFS carries no executable bit
		t.Error("installed shim is not executable")
	}
	receipt := readReceiptMap(t, filepath.Join(target, skill.ReceiptName))
	if receipt["tool"] != "verdict" || receipt["version"] != "1.2.3" || receipt["installed_at"] != "2026-11-02T12:00:00Z" {
		t.Fatalf("receipt = %v", receipt)
	}
	planned := append(append([]string{}, plan.Files...), skill.ReceiptName)
	if extra := extraFiles(t, target, planned); len(extra) != 0 {
		t.Fatalf("install left unplanned files (temp leftovers included): %v", extra)
	}

	plan, ref = skill.Install(context.Background(), req)
	if ref != nil {
		t.Fatalf("re-run: %v", ref)
	}
	if plan.Status != skill.StatusKeep || plan.Written != 0 {
		t.Fatalf("re-run plan = %+v", plan)
	}
}

// TestInstallUpdateAndReceiptRefresh covers byte drift and a skewed receipt.
//
//nolint:cyclop,revive // the test walks update and receipt refresh
func TestInstallUpdateAndReceiptRefresh(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	req := newRequest(t)
	req.Dir = dir
	target := filepath.Join(dir, "verdict")
	if _, ref := skill.Install(context.Background(), req); ref != nil {
		t.Fatalf("seed install: %v", ref)
	}
	stale := filepath.Join(target, "SKILL.md")
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, ref := skill.Install(context.Background(), req)
	if ref != nil {
		t.Fatalf("update: %v", ref)
	}
	if plan.Status != skill.StatusUpdate || !slices.Equal(plan.Changed, []string{"SKILL.md"}) {
		t.Fatalf("update plan = %+v", plan)
	}
	want, err := skillfs.Read(skillfs.SkillMD)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(stale)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("update did not restore SKILL.md")
	}

	writeReceiptVersion(t, target, "0.0.1")
	plan, ref = skill.Install(context.Background(), req)
	if ref != nil {
		t.Fatalf("keep refresh: %v", ref)
	}
	if plan.Status != skill.StatusKeep || !plan.ReceiptUpdated {
		t.Fatalf("keep plan = %+v", plan)
	}
	if got := readReceiptMap(t, filepath.Join(target, ".verdict-managed"))["version"]; got != "1.2.3" {
		t.Fatalf("receipt version = %v, want 1.2.3", got)
	}
}

// TestInstallForeign covers the typed refusal, the dry-run preview, and
// --force preserving foreign files.
//
//nolint:cyclop,revive // the test walks the foreign/force matrix
func TestInstallForeign(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	req := newRequest(t)
	req.Dir = dir
	target := filepath.Join(dir, "verdict")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(target, "foreign.txt")
	if err := os.WriteFile(foreign, []byte("not verdict's"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, ref := skill.Install(context.Background(), req); ref == nil || ref.Code != "skill-dir-foreign" {
		t.Fatalf("foreign refusal = %v, want skill-dir-foreign", ref)
	}
	if got, err := os.ReadFile(foreign); err != nil || string(got) != "not verdict's" {
		t.Fatalf("refusal touched the foreign file: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("refusal wrote the payload")
	}

	req.DryRun = true
	plan, ref := skill.Install(context.Background(), req)
	if ref != nil || plan.Status != skill.StatusBlocked || plan.Written != 0 {
		t.Fatalf("dry-run foreign plan = %+v, %v", plan, ref)
	}
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("dry-run wrote the payload")
	}

	req.DryRun = false
	req.Force = true
	plan, ref = skill.Install(context.Background(), req)
	if ref != nil {
		t.Fatalf("force install: %v", ref)
	}
	if plan.Written != len(plan.Files) {
		t.Fatalf("force plan = %+v", plan)
	}
	if got, err := os.ReadFile(foreign); err != nil || string(got) != "not verdict's" {
		t.Fatalf("force touched the foreign file: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(target, ".verdict-managed")); err != nil {
		t.Fatalf("force did not write the receipt: %v", err)
	}
}

// TestInstallTargetFile pins the never-replace-a-file refusal (even --force).
func TestInstallTargetFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	req := newRequest(t)
	req.Dir = dir
	target := filepath.Join(dir, "verdict")
	if err := os.WriteFile(target, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	req.DryRun = true
	plan, ref := skill.Install(context.Background(), req)
	if ref != nil || plan.Status != skill.StatusBlocked || !plan.TargetFile {
		t.Fatalf("dry-run target-file plan = %+v, %v", plan, ref)
	}
	req.DryRun = false
	req.Force = true
	if _, ref := skill.Install(context.Background(), req); ref == nil || ref.Code != "skill-target-file" {
		t.Fatalf("target-file refusal = %v", ref)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "not a dir" {
		t.Fatal("target file was modified")
	}
}

// TestInstallAgentResolution covers the --agent matrix and its refusals.
//
//nolint:revive // the test is the agent-resolution matrix
func TestInstallAgentResolution(t *testing.T) {
	t.Parallel()
	req := newRequest(t)
	req.Agent = "claude"
	plan, ref := skill.Install(context.Background(), req)
	if ref != nil {
		t.Fatalf("claude install: %v", ref)
	}
	want := filepath.Join(req.Env["HOME"], ".claude", "skills", "verdict")
	if plan.Target != want {
		t.Fatalf("claude target = %q, want %q", plan.Target, want)
	}
	req.DryRun = true
	req.Agent = "sublime"
	if _, ref := skill.Install(context.Background(), req); ref == nil || ref.Code != "skill-unknown-agent" {
		t.Fatalf("unknown agent refusal = %v", ref)
	}
	req.Agent = "codex"
	req.Env = map[string]string{"CODEX_HOME": filepath.Join(t.TempDir(), "codex")}
	plan, ref = skill.Install(context.Background(), req)
	if ref != nil || plan.Target != filepath.Join(req.Env["CODEX_HOME"], "skills", "verdict") {
		t.Fatalf("codex target = %+v, %v", plan, ref)
	}
	req.Env = map[string]string{}
	if _, ref := skill.Install(context.Background(), req); ref == nil || ref.Code != "skill-no-home" {
		t.Fatalf("no-home refusal = %v", ref)
	}
}

// TestInstallRefusesRelativeDir pins the absolute --dir contract.
func TestInstallRefusesRelativeDir(t *testing.T) {
	t.Parallel()
	req := newRequest(t)
	req.Dir = "relative/skills"
	if _, ref := skill.Install(context.Background(), req); ref == nil || ref.Code != "skill-bad-dir" {
		t.Fatalf("relative --dir refusal = %v", ref)
	}
}

// TestInstallConflictRefusesBeforeWriting pins the preflight: a foreign file
// where a payload directory belongs refuses with no partial write.
//
//nolint:revive // the test walks the preflight conflicts
func TestInstallConflictRefusesBeforeWriting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	req := newRequest(t)
	req.Dir = dir
	target := filepath.Join(dir, "verdict")
	if _, ref := skill.Install(context.Background(), req); ref != nil {
		t.Fatalf("seed install: %v", ref)
	}
	if err := os.RemoveAll(filepath.Join(target, "prompts")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "prompts"), []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ref := skill.Install(context.Background(), req); ref == nil || ref.Code != "skill-foreign-conflict" {
		t.Fatalf("conflict refusal = %v", ref)
	}
	after, err := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("conflict refusal partially wrote the payload")
	}
	if got, err := os.ReadFile(filepath.Join(target, "prompts")); err != nil || string(got) != "in the way" {
		t.Fatal("conflict refusal touched the foreign file")
	}
}

// TestInstallKeepsInSyncReadOnlyTarget pins that an in-sync keep writes
// nothing, so a re-run over a read-only target stays a no-op instead of
// refusing a write it would never make.
func TestInstallKeepsInSyncReadOnlyTarget(t *testing.T) {
	t.Parallel()
	req := newRequest(t)
	req.Dir = t.TempDir()
	target := filepath.Join(req.Dir, "verdict")
	if _, ref := skill.Install(context.Background(), req); ref != nil {
		t.Fatalf("seed install: %v", ref)
	}
	readOnly(t, target)

	plan, ref := skill.Install(context.Background(), req)
	if ref != nil {
		t.Fatalf("in-sync keep on a read-only target: %v", ref)
	}
	if plan.Status != skill.StatusKeep || plan.Written != 0 || plan.ReceiptUpdated {
		t.Fatalf("re-run plan = %+v, want an untouched keep", plan)
	}
}

// TestInstallRefusesUnwritablePayloadDir pins the preflight for an owned
// target: a read-only payload dir refuses typed, names the target, and stops
// the update before the writable files ahead of it are rewritten.
func TestInstallRefusesUnwritablePayloadDir(t *testing.T) {
	t.Parallel()
	req := newRequest(t)
	req.Dir = t.TempDir()
	target := filepath.Join(req.Dir, "verdict")
	if _, ref := skill.Install(context.Background(), req); ref != nil {
		t.Fatalf("seed install: %v", ref)
	}
	drifted := filepath.Join(target, "SKILL.md")
	if err := os.WriteFile(drifted, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	readOnly(t, filepath.Join(target, "prompts"))

	_, ref := skill.Install(context.Background(), req)
	if ref == nil || ref.Code != "skill-install-failed" {
		t.Fatalf("unwritable payload dir refusal = %v, want skill-install-failed", ref)
	}
	if !strings.Contains(ref.Message, target) {
		t.Errorf("refusal does not name the target %s: %q", target, ref.Message)
	}
	got, err := os.ReadFile(drifted)
	if err != nil || string(got) != "stale" {
		t.Fatalf("refused run rewrote SKILL.md (%d bytes, read err %v)", len(got), err)
	}
}

// TestInstallRefusesFileWherePacksDirBelongsTo pins the directory-level
// preflight for the pack payload: a regular file at <target>/packs refuses
// skill-foreign-conflict before any write, not a raw ENOTDIR from a nested
// file.
//
//nolint:revive // the test walks the seed, refusal, and no-write arms
func TestInstallRefusesFileWherePacksDirBelongsTo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	req := newRequest(t)
	req.Dir = dir
	target := filepath.Join(dir, "verdict")
	if _, ref := skill.Install(context.Background(), req); ref != nil {
		t.Fatalf("seed install: %v", ref)
	}
	if err := os.RemoveAll(filepath.Join(target, "packs")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "packs"), []byte("in the way"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, ref := skill.Install(context.Background(), req)
	if ref == nil || ref.Code != "skill-foreign-conflict" {
		t.Fatalf("packs conflict refusal = %v, want skill-foreign-conflict", ref)
	}
	want := filepath.Join(target, "packs") + " exists and is not a directory; refusing to replace it"
	if ref.Message != want {
		t.Fatalf("message = %q, want %q", ref.Message, want)
	}
	if got, err := os.ReadFile(filepath.Join(target, "packs")); err != nil || string(got) != "in the way" {
		t.Fatal("conflict refusal touched the foreign file")
	}
}

// TestInstallRefusesPayloadDirEscapingTheTarget pins the root boundary: an
// install through a payload directory that links outside the target refuses
// skill-foreign-conflict before writing, in fresh, upgrade, dry-run, and
// --force modes, and leaves the outside directory byte-identical.
//
//nolint:cyclop,gocognit,revive // the test walks the escape matrix
func TestInstallRefusesPayloadDirEscapingTheTarget(t *testing.T) {
	t.Parallel()
	outside := filepath.Join(t.TempDir(), "outside")
	outsideFile := filepath.Join(outside, "review.md")
	writeTestFile(t, outsideFile, "outside bytes\n")
	modes := []struct {
		name  string
		owned bool
		force bool
		dry   bool
	}{
		{name: "fresh"},
		{name: "upgrade", owned: true},
		{name: "upgrade dry-run", owned: true, dry: true},
		{name: "force", force: true},
	}
	for _, mode := range modes {
		req := newRequest(t)
		req.Dir = t.TempDir()
		target := filepath.Join(req.Dir, "verdict")
		if mode.owned {
			if _, ref := skill.Install(context.Background(), req); ref != nil {
				t.Fatalf("%s seed install: %v", mode.name, ref)
			}
		} else if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(filepath.Join(target, "prompts")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(target, "prompts")); err != nil {
			t.Skipf("this platform cannot create symlinks here: %v", err)
		}
		req.Force = mode.force
		req.DryRun = mode.dry
		_, ref := skill.Install(context.Background(), req)
		if ref == nil || ref.Code != "skill-foreign-conflict" {
			t.Fatalf("%s refusal = %v, want skill-foreign-conflict", mode.name, ref)
		}
		if !strings.Contains(ref.Message, "path escapes from parent") {
			t.Fatalf("%s message = %q, want the root escape named", mode.name, ref.Message)
		}
		if got, err := os.ReadFile(outsideFile); err != nil || string(got) != "outside bytes\n" {
			t.Fatalf("%s outside file = %q, %v; want it untouched", mode.name, got, err)
		}
		if entries, err := os.ReadDir(outside); err != nil || len(entries) != 1 {
			t.Fatalf("%s outside dir = %v, %v; want only review.md", mode.name, entries, err)
		}
		if extra := tempLeftovers(t, target); len(extra) != 0 {
			t.Fatalf("%s left temp files in the target: %v", mode.name, extra)
		}
		if extra := tempLeftovers(t, outside); len(extra) != 0 {
			t.Fatalf("%s left temp files outside the target: %v", mode.name, extra)
		}
		if !mode.owned {
			if _, err := os.Stat(filepath.Join(target, "SKILL.md")); !os.IsNotExist(err) {
				t.Fatalf("%s wrote SKILL.md before refusing", mode.name)
			}
			if _, err := os.Stat(filepath.Join(target, skill.ReceiptName)); !os.IsNotExist(err) {
				t.Fatalf("%s wrote the receipt before refusing", mode.name)
			}
		}
	}
}

// TestInstallRefusesSymlinkedTarget pins the symlink wording and that the
// directory behind the link is untouched.
//
//nolint:revive // the test walks the symlink refusal
func TestInstallRefusesSymlinkedTarget(t *testing.T) {
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
	_, ref := skill.Install(context.Background(), req)
	if ref == nil || ref.Code != "skill-target-file" {
		t.Fatalf("symlinked target refusal = %v, want skill-target-file", ref)
	}
	if want := target + " is a symlink; refusing to install through it"; ref.Message != want {
		t.Fatalf("symlinked target message = %q, want %q", ref.Message, want)
	}
	if _, ref := skill.Status(context.Background(), req, false); ref == nil || ref.Code != "skill-target-file" {
		t.Fatalf("symlinked target status = %v, want skill-target-file", ref)
	}
	if got, err := os.ReadFile(keep); err != nil || string(got) != "keep\n" {
		t.Fatalf("symlinked target file = %q, %v; want it untouched", got, err)
	}
}

// tempLeftovers lists staged atomic temp files under root.
func tempLeftovers(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(entry.Name(), ".tmp-") {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func readReceiptMap(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]string
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func writeReceiptVersion(t *testing.T, target, version string) {
	t.Helper()
	path := filepath.Join(target, ".verdict-managed")
	doc := map[string]string{"tool": "verdict", "version": version, "installed_at": "2026-11-02T00:00:00Z"}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func extraFiles(t *testing.T, root string, planned []string) []string {
	t.Helper()
	allowed := map[string]bool{skill.ReceiptName: true}
	for _, rel := range planned {
		allowed[filepath.FromSlash(rel)] = true
	}
	var extra []string
	walkFiles(t, root, func(path string) {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatalf("rel %s: %v", path, err)
		}
		if !allowed[rel] {
			extra = append(extra, path)
		}
	})
	return extra
}

func walkFiles(t *testing.T, root string, fn func(path string)) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			fn(path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
