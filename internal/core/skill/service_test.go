package skill_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/skill"
)

// fixedNow is the injected clock for deterministic receipts.
func fixedNow() time.Time {
	return time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
}

// newRequest builds a hermetic request rooted in a fresh HOME.
func newRequest(t *testing.T) *skill.Request {
	t.Helper()
	return &skill.Request{
		Runtime: &core.Runtime{Env: map[string]string{"HOME": t.TempDir()}, Now: fixedNow},
		Version: "1.2.3",
	}
}

// listFiles reads the served inventory, failing the test on error.
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
	if shim.Mode().Perm()&0o111 == 0 {
		t.Error("installed shim is not executable")
	}
	receipt := readReceiptMap(t, filepath.Join(target, ".verdict-managed"))
	if receipt["tool"] != "verdict" || receipt["version"] != "1.2.3" || receipt["installed_at"] != "2026-11-02T12:00:00Z" {
		t.Fatalf("receipt = %v", receipt)
	}
	if leftovers := tempLeftovers(t, target); len(leftovers) != 0 {
		t.Fatalf("install left temp files: %v", leftovers)
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

// readReceiptMap parses a receipt for assertions.
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

// writeReceiptVersion rewrites one installed receipt's version.
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

// tempLeftovers lists atomic temp files under root.
func tempLeftovers(t *testing.T, root string) []string {
	t.Helper()
	var leftovers []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && len(entry.Name()) > 5 && entry.Name()[:5] == ".tmp-" {
			leftovers = append(leftovers, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return leftovers
}
