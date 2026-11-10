package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	skillfs "github.com/stainless-code/verdict"
)

// skillTreeHash folds every relative path and its bytes into one digest.
//
//nolint:revive // fixture hashing walks the tree
func skillTreeHash(t *testing.T, root string) string {
	t.Helper()
	hash := sha256.New()
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
		hash.Write([]byte(filepath.ToSlash(rel)))
		hash.Write([]byte{0})
		hash.Write(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// TestSkillShowPath pins the byte-identical path read and its one refusal.
//
//nolint:cyclop,revive // the test walks the path matrix
func TestSkillShowPath(t *testing.T) {
	hermeticEnv(t)
	setTestVersion(t)
	want, err := skillfs.Read("prompts/review.md")
	if err != nil {
		t.Fatal(err)
	}
	code, out, derr := runCapture(t, []string{"skill", "show", "prompts/review.md"})
	if code != 0 || out != string(want) || derr != "" {
		t.Fatalf("skill show prompts/review.md = exit %d stderr %q (stdout %d bytes)", code, derr, len(out))
	}
	code, out, _ = runCapture(t, []string{"skill", "show"})
	if code != 0 || out == "" {
		t.Fatalf("bare skill show = exit %d (%d bytes)", code, len(out))
	}
	for _, path := range []string{"../SKILL.md", "/etc/passwd", "prompts/../SKILL.md", "nope.md"} {
		code, out, _ := runCapture(t, []string{"skill", "show", path, "--json"})
		doc := decodeSkillEnvelope(t, out)
		errObj, ok := doc["error"].(map[string]any)
		if code != 1 || !ok || errObj["code"] != "skill-path-not-inventory" {
			t.Fatalf("skill show %q = exit %d error %v", path, code, doc["error"])
		}
	}
	code, out, _ = runCapture(t, []string{"skill", "show", "SKILL.md", "--json"})
	doc := decodeSkillEnvelope(t, out)
	data := skillData(t, doc)
	if code != 0 || data["path"] != "SKILL.md" || data["content"] != string(wantSkillMD(t)) {
		t.Fatalf("skill show SKILL.md --json = exit %d data keys %v", code, data)
	}
	code, out, _ = runCapture(t, []string{"skill", "show", "SKILL.md", "--json", "--format", "md"})
	if code != 2 || out != "" {
		t.Fatalf("--json with --format md = exit %d stdout %q, want usage exit 2", code, out)
	}
	code, _, _ = runCapture(t, []string{"skill", "show", "SKILL.md", "prompts/review.md"})
	if code != 2 {
		t.Fatalf("two paths = exit %d, want usage exit 2", code)
	}
}

func wantSkillMD(t *testing.T) []byte {
	t.Helper()
	body, err := skillfs.Read("SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// TestSkillStatusCommand pins the read-only drift report and its exit codes.
//
//nolint:cyclop,gocognit,revive // the test walks the status matrix
func TestSkillStatusCommand(t *testing.T) {
	hermeticEnv(t)
	setTestVersion(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "verdict")
	if code, out, derr := runCapture(t, []string{"skill", "install", "--dir", dir, "--json"}); code != 0 {
		t.Fatalf("seed install = exit %d stderr %q\n%s", code, derr, out)
	}

	code, out, _ := runCapture(t, []string{"skill", "status", "--dir", dir, "--json"})
	doc := decodeSkillEnvelope(t, out)
	data := skillData(t, doc)
	if code != 0 || data["in_sync"] != true || data["status"] != "keep" {
		t.Fatalf("fresh status = exit %d data %v", code, data)
	}

	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("hand edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := skillTreeHash(t, target)
	code, out, _ = runCapture(t, []string{"skill", "status", "--dir", dir, "--diff", "--json"})
	doc = decodeSkillEnvelope(t, out)
	data = skillData(t, doc)
	if code != 1 || data["in_sync"] != false || data["status"] != "update" {
		t.Fatalf("edited status = exit %d data %v", code, data)
	}
	diffs, ok := data["diff"].([]any)
	if !ok || len(diffs) != 1 {
		t.Fatalf("diff data = %v, want one entry", data["diff"])
	}
	entry, ok := diffs[0].(map[string]any)
	if !ok || entry["path"] != "SKILL.md" || entry["truncated"] != false {
		t.Fatalf("diff entry = %v", diffs[0])
	}
	if body, ok := entry["diff"].(string); !ok || !strings.Contains(body, "-hand edited") {
		t.Fatalf("diff body = %v", entry["diff"])
	}
	if after := skillTreeHash(t, target); after != before {
		t.Fatal("skill status wrote to the installed tree")
	}

	code, out, _ = runCapture(t, []string{"skill", "status", "--dir", dir, "--diff"})
	if code != 1 || !strings.Contains(out, "status: drift") || !strings.Contains(out, "-hand edited") {
		t.Fatalf("human status = exit %d\n%s", code, out)
	}

	foreign := t.TempDir()
	if err := os.MkdirAll(filepath.Join(foreign, "verdict"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runCapture(t, []string{"skill", "status", "--dir", foreign, "--json"})
	doc = decodeSkillEnvelope(t, out)
	if code != 1 || skillData(t, doc)["status"] != "blocked" {
		t.Fatalf("foreign status = exit %d data %v", code, doc["data"])
	}
}

// TestSkillStatusAgentsAndDetect pins argv order and the --detect reader.
func TestSkillStatusAgentsAndDetect(t *testing.T) {
	hermeticEnv(t)
	setTestVersion(t)
	if code, out, _ := runCapture(t, []string{"skill", "install", "--agent", "claude", "--agent", "cursor", "--json"}); code != 0 {
		t.Fatalf("seed agents install = exit %d\n%s", code, out)
	}
	code, out, _ := runCapture(t, []string{"skill", "status", "--agent", "cursor", "--agent", "claude", "--json"})
	doc := decodeSkillEnvelope(t, out)
	data := skillData(t, doc)
	if code != 0 || data["in_sync"] != true {
		t.Fatalf("agent status = exit %d data %v", code, data)
	}
	agents, ok := data["agents"].([]any)
	if !ok || len(agents) != 2 || agents[0] != "cursor" || agents[1] != "claude" {
		t.Fatalf("status agents = %v, want [cursor claude]", data["agents"])
	}

	code, out, _ = runCapture(t, []string{"skill", "status", "--detect", "--json"})
	data = skillData(t, decodeSkillEnvelope(t, out))
	if code != 0 || data["in_sync"] != true {
		t.Fatalf("detect status = exit %d data %v", code, data)
	}
}

// TestSkillInstallRepeatableAgents pins one install per named agent.
//
//nolint:cyclop,revive // the test walks the repeatable-agent matrix
func TestSkillInstallRepeatableAgents(t *testing.T) {
	hermeticEnv(t)
	setTestVersion(t)
	code, out, _ := runCapture(t, []string{"skill", "install", "--agent", "claude", "--agent", "cursor", "--json"})
	if code != 0 {
		t.Fatalf("repeatable install = exit %d\n%s", code, out)
	}
	doc := decodeSkillEnvelope(t, out)
	data := skillData(t, doc)
	agents, ok := data["agents"].([]any)
	if !ok || len(agents) != 2 || agents[0] != "claude" || agents[1] != "cursor" {
		t.Fatalf("agents = %v, want [claude cursor]", data["agents"])
	}
	targets, ok := data["targets"].([]any)
	if !ok || len(targets) != 2 {
		t.Fatalf("targets = %v, want two", data["targets"])
	}
	for _, dir := range []string{".claude", ".cursor"} {
		if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), dir, "skills", "verdict", "SKILL.md")); err != nil {
			t.Fatalf("%s was not installed: %v", dir, err)
		}
	}

	code, out, _ = runCapture(t, []string{"skill", "install", "--agent", "claude", "--agent", "claude", "--json"})
	doc = decodeSkillEnvelope(t, out)
	data = skillData(t, doc)
	if code != 0 || data["status"] != "keep" {
		t.Fatalf("duplicate agent install = exit %d data %v", code, data)
	}
	if _, ok := data["targets"]; ok {
		t.Fatalf("one deduped agent produced the multi-target envelope: %v", data)
	}
}

// TestSkillUninstallCommand pins dry-run, the real run, and the kept file.
//
//nolint:cyclop,revive // the test walks the uninstall matrix
func TestSkillUninstallCommand(t *testing.T) {
	hermeticEnv(t)
	setTestVersion(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "verdict")
	if code, out, _ := runCapture(t, []string{"skill", "install", "--dir", dir, "--json"}); code != 0 {
		t.Fatalf("seed install = exit %d\n%s", code, out)
	}
	foreign := filepath.Join(target, "keep.txt")
	if err := os.WriteFile(foreign, []byte("foreign bytes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	before := skillTreeHash(t, target)
	code, out, _ := runCapture(t, []string{"skill", "uninstall", "--dir", dir, "--dry-run", "--json"})
	doc := decodeSkillEnvelope(t, out)
	data := skillData(t, doc)
	if code != 0 || data["dry_run"] != true || data["status"] != "remove" {
		t.Fatalf("dry-run uninstall = exit %d data %v", code, data)
	}
	if removed, ok := data["removed"].([]any); !ok || len(removed) != 0 {
		t.Fatalf("dry-run removed = %v, want none", data["removed"])
	}
	if after := skillTreeHash(t, target); after != before {
		t.Fatal("dry-run uninstall changed the tree")
	}

	code, out, _ = runCapture(t, []string{"skill", "uninstall", "--dir", dir, "--json"})
	doc = decodeSkillEnvelope(t, out)
	data = skillData(t, doc)
	if code != 0 || data["status"] != "removed" || data["receipt_removed"] != true {
		t.Fatalf("uninstall = exit %d data %v", code, data)
	}
	if kept, ok := data["kept"].([]any); !ok || len(kept) != 1 || kept[0] != "keep.txt" {
		t.Fatalf("kept = %v, want [keep.txt]", data["kept"])
	}
	if got, err := os.ReadFile(foreign); err != nil || string(got) != "foreign bytes\n" {
		t.Fatalf("foreign file = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("uninstall left SKILL.md behind")
	}

	code, _, _ = runCapture(t, []string{"skill", "uninstall", "--dir", dir, "--json"})
	if code != 1 {
		t.Fatalf("second uninstall = exit %d, want the typed no-receipt refusal", code)
	}
}

// TestSkillUninstallNoReceiptAndForce pins the foreign refusal and --force.
//
//nolint:revive // the test walks the force matrix
func TestSkillUninstallNoReceiptAndForce(t *testing.T) {
	hermeticEnv(t)
	setTestVersion(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "verdict")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "foreign.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runCapture(t, []string{"skill", "uninstall", "--dir", dir, "--json"})
	doc := decodeSkillEnvelope(t, out)
	errObj, ok := doc["error"].(map[string]any)
	if code != 1 || !ok || errObj["code"] != "skill-dir-foreign" {
		t.Fatalf("no-receipt uninstall = exit %d error %v", code, doc["error"])
	}
	if got, err := os.ReadFile(filepath.Join(target, "foreign.txt")); err != nil || string(got) != "keep" {
		t.Fatal("refusal touched the foreign file")
	}

	code, out, _ = runCapture(t, []string{"skill", "uninstall", "--dir", dir, "--force", "--json"})
	doc = decodeSkillEnvelope(t, out)
	if code != 0 || skillData(t, doc)["status"] != "keep" {
		t.Fatalf("force uninstall on a foreign-only dir = exit %d data %v", code, doc["data"])
	}
	if _, err := os.Stat(filepath.Join(target, "foreign.txt")); err != nil {
		t.Fatal("force uninstall removed the foreign file")
	}
}
