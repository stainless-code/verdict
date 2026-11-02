package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	skillfs "github.com/stainless-code/verdict"
)

// setTestVersion pins the linked build version for deterministic envelopes.
func setTestVersion(t *testing.T) {
	t.Helper()
	old := version
	version = "v9.9.9"
	t.Cleanup(func() { version = old })
}

// decodeSkillEnvelope parses exactly one envelope from stdout.
func decodeSkillEnvelope(t *testing.T, out string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(out))
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("stdout is not one JSON doc: %v\n%s", err, out)
	}
	if dec.More() {
		t.Fatalf("stdout holds more than one JSON doc:\n%s", out)
	}
	return doc
}

// skillData returns the envelope's data object.
func skillData(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	data, ok := doc["data"].(map[string]any)
	if !ok {
		t.Fatalf("envelope data = %v", doc["data"])
	}
	return data
}

// TestSkillShowAndFiles pins the read commands: SKILL.md bytes, the sorted
// file list, and the JSON inventory envelope.
//
//nolint:cyclop,gocognit,revive // the test walks the read surface
func TestSkillShowAndFiles(t *testing.T) {
	hermeticEnv(t)
	setTestVersion(t)
	want, err := skillfs.Read(skillfs.SkillMD)
	if err != nil {
		t.Fatal(err)
	}
	code, out, derr := runCapture(t, []string{"skill"})
	if code != 0 || out != string(want) || derr != "" {
		t.Fatalf("skill = exit %d stderr %q (stdout %d bytes)", code, derr, len(out))
	}
	code, out, _ = runCapture(t, []string{"skill", "show", "--format", "md"})
	if code != 0 || out != string(want) {
		t.Fatalf("skill show --format md = exit %d", code)
	}
	code, out, _ = runCapture(t, []string{"skill", "files"})
	if code != 0 {
		t.Fatalf("skill files exit = %d", code)
	}
	lines := strings.Fields(out)
	for _, path := range []string{"SKILL.md", "scripts/verdict", "prompts/review.md", "schemas/validate.sh"} {
		if !slices.Contains(lines, path) {
			t.Errorf("skill files misses %s", path)
		}
	}
	if !slices.IsSorted(lines) {
		t.Error("skill files output is not sorted")
	}

	code, out, _ = runCapture(t, []string{"skill", "--format", "json"})
	if code != 0 {
		t.Fatalf("skill --format json exit = %d", code)
	}
	doc := decodeSkillEnvelope(t, out)
	if doc["verb"] != "skill" || doc["error"] != nil {
		t.Fatalf("envelope = %v", doc)
	}
	data := skillData(t, doc)
	if data["name"] != "verdict" || data["version"] != "9.9.9" {
		t.Fatalf("inventory = %v", data)
	}
	if files, ok := data["files"].([]any); !ok || len(files) == 0 {
		t.Fatalf("inventory files = %v", data["files"])
	}
	code, out, _ = runCapture(t, []string{"skill", "files", "--json"})
	if code != 0 || decodeSkillEnvelope(t, out)["verb"] != "skill" {
		t.Fatalf("skill files --json = exit %d", code)
	}
}

// TestSkillInstallJSONAndForce covers create, dry-run, and the foreign/force
// matrix through the command layer, with one envelope per call.
//
//nolint:cyclop,gocognit,revive // the test walks the install matrix
func TestSkillInstallJSONAndForce(t *testing.T) {
	hermeticEnv(t)
	setTestVersion(t)
	dir := t.TempDir()
	code, out, _ := runCapture(t, []string{"skill", "install", "--dir", dir, "--json"})
	if code != 0 {
		t.Fatalf("install exit = %d\n%s", code, out)
	}
	doc := decodeSkillEnvelope(t, out)
	data := skillData(t, doc)
	if data["status"] != "create" || data["target"] != filepath.Join(dir, "verdict") {
		t.Fatalf("install data = %v", data)
	}
	if written, ok := data["written"].(float64); !ok || written == 0 {
		t.Fatalf("written = %v", data["written"])
	}
	if _, err := os.Stat(filepath.Join(dir, "verdict", "scripts", "verdict")); err != nil {
		t.Fatalf("shim not installed: %v", err)
	}

	dry := t.TempDir()
	code, out, _ = runCapture(t, []string{"skill", "install", "--dir", dry, "--dry-run", "--json"})
	doc = decodeSkillEnvelope(t, out)
	if code != 0 || skillData(t, doc)["dry_run"] != true {
		t.Fatalf("dry-run exit = %d data = %v", code, doc["data"])
	}
	if _, err := os.Stat(filepath.Join(dry, "verdict")); !os.IsNotExist(err) {
		t.Fatal("dry-run created the target")
	}

	foreign := t.TempDir()
	if err := os.MkdirAll(filepath.Join(foreign, "verdict"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreign, "verdict", "foreign.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runCapture(t, []string{"skill", "install", "--dir", foreign, "--json"})
	doc = decodeSkillEnvelope(t, out)
	errObj, ok := doc["error"].(map[string]any)
	if code != 1 || !ok || errObj["code"] != "skill-dir-foreign" {
		t.Fatalf("foreign install = exit %d error %v", code, doc["error"])
	}
	code, out, _ = runCapture(t, []string{"skill", "install", "--dir", foreign, "--force", "--json"})
	if code != 0 {
		t.Fatalf("force install = exit %d\n%s", code, out)
	}
	if got, err := os.ReadFile(filepath.Join(foreign, "verdict", "foreign.txt")); err != nil || string(got) != "keep" {
		t.Fatal("force install touched the foreign file")
	}
}

// TestSkillUsageErrors pins the per-command flag grammar.
func TestSkillUsageErrors(t *testing.T) {
	hermeticEnv(t)
	for _, args := range [][]string{
		{"skill", "bogus"},
		{"skill", "install", "--dir"},
		{"skill", "install", "--dir", "/tmp/x", "--agent", "claude"},
		{"skill", "files", "--bogus"},
		{"skill", "--format", "yaml"},
	} {
		code, out, derr := runCapture(t, args)
		if code != 2 || out != "" || derr == "" {
			t.Errorf("%v = exit %d stdout %q stderr %q, want usage exit 2", args, code, out, derr)
		}
	}
	code, out, _ := runCapture(t, []string{"skill", "--help"})
	if code != 0 || !strings.Contains(out, "skill install") {
		t.Fatalf("skill --help = exit %d out %q", code, out)
	}
}

// TestSkillDefaultsToSharedAgentsDir pins the no-flag target to the shared
// ~/.agents/skills/verdict slot.
func TestSkillDefaultsToSharedAgentsDir(t *testing.T) {
	hermeticEnv(t)
	setTestVersion(t)
	code, out, _ := runCapture(t, []string{"skill", "install", "--dry-run", "--json"})
	if code != 0 {
		t.Fatalf("default install preview exit = %d\n%s", code, out)
	}
	want := filepath.Join(os.Getenv("HOME"), ".agents", "skills", "verdict")
	if data := skillData(t, decodeSkillEnvelope(t, out)); data["target"] != want {
		t.Fatalf("default target = %v, want %s", data["target"], want)
	}
}

// TestSkillShowJSONBuildsOneDoc pins that no human line leaks into stdout.
func TestSkillShowJSONBuildsOneDoc(t *testing.T) {
	hermeticEnv(t)
	setTestVersion(t)
	code, out, derr := runCapture(t, []string{"skill", "--format", "json"})
	if code != 0 || derr != "" {
		t.Fatalf("exit = %d stderr = %q", code, derr)
	}
	if strings.Count(out, `"schema_version": "1"`) != 1 {
		t.Fatalf("stdout is not one envelope:\n%s", out)
	}
}
