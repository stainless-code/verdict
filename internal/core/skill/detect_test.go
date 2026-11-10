package skill_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/core/skill"
)

// seedSkillsDir creates one agent's skills dir and puts a file in it, the
// shape install-skill.sh's --detect rule treats as a live setup.
func seedSkillsDir(t *testing.T, base string, parts ...string) string {
	t.Helper()
	dir := filepath.Join(append([]string{base}, append(parts, "skills")...)...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, "existing.md"), "an existing skill\n")
	return dir
}

// writeTestFile writes one file, creating parents.
func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestInstallDetectedPresentAgents pins the detection rule: only an agent whose
// skills dir already exists AND holds a file is a target, in agent-table order
// (codex is created first and still lands second), and exactly those targets
// receive the payload.
//
//nolint:cyclop,gocognit,revive // the test walks the detection matrix
func TestInstallDetectedPresentAgents(t *testing.T) {
	t.Parallel()
	req := newRequest(t)
	home := req.Env["HOME"]
	codex := seedSkillsDir(t, home, ".codex")
	claude := seedSkillsDir(t, home, ".claude")
	// Present but empty: not a live setup, so not a target.
	cursor := filepath.Join(home, ".cursor", "skills")
	if err := os.MkdirAll(cursor, 0o755); err != nil {
		t.Fatal(err)
	}
	gemini := filepath.Join(home, ".gemini", "skills") // absent entirely

	det, ref := skill.InstallDetected(context.Background(), req)
	if ref != nil {
		t.Fatalf("detect: %v", ref)
	}
	if want := []string{"claude", "codex"}; !slices.Equal(det.Agents, want) {
		t.Fatalf("detected agents = %v, want %v", det.Agents, want)
	}
	for i, dir := range []string{claude, codex} {
		want := filepath.Join(dir, "verdict")
		if det.Plans[i].Target != want {
			t.Errorf("plan %d target = %q, want %q", i, det.Plans[i].Target, want)
		}
		if det.Plans[i].Status != skill.StatusCreate || det.Plans[i].Written == 0 {
			t.Errorf("plan %d = %+v, want a create with bytes written", i, det.Plans[i])
		}
	}
	body, err := skillfs.Read(skillfs.SkillMD)
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{claude, codex} {
		got, err := os.ReadFile(filepath.Join(dir, "verdict", "SKILL.md"))
		if err != nil {
			t.Fatalf("read installed SKILL.md under %s: %v", dir, err)
		}
		if !bytes.Equal(got, body) {
			t.Errorf("installed SKILL.md under %s differs from the embedded bytes", dir)
		}
		if _, err := os.Stat(filepath.Join(dir, "existing.md")); err != nil {
			t.Errorf("install removed the existing skill file under %s: %v", dir, err)
		}
	}
	for _, dir := range []string{cursor, gemini} {
		if _, err := os.Stat(filepath.Join(dir, "verdict")); !os.IsNotExist(err) {
			t.Errorf("installed into a non-live skills dir %s (stat err %v)", dir, err)
		}
	}
}

// TestInstallDetectedNoneRefuses pins the two empty-scope refusals: nothing
// live refuses typed and names --agent, and an environment that resolves no
// skills dir at all refuses skill-no-home instead of blaming an absent agent.
//
//nolint:revive // the test walks both refusal arms
func TestInstallDetectedNoneRefuses(t *testing.T) {
	t.Parallel()
	req := newRequest(t)
	home := req.Env["HOME"]
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, ref := skill.InstallDetected(context.Background(), req)
	if ref == nil || ref.Code != "skill-no-detected-agent" {
		t.Fatalf("empty-scope refusal = %v, want skill-no-detected-agent", ref)
	}
	if !strings.Contains(ref.Message, "--agent") {
		t.Errorf("refusal message does not name --agent: %q", ref.Message)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != ".claude" {
		t.Fatalf("refusal wrote into HOME: %v", entries)
	}

	req.Env = map[string]string{}
	if _, ref := skill.InstallDetected(context.Background(), req); ref == nil || ref.Code != "skill-no-home" {
		t.Fatalf("unresolvable environment refusal = %v, want skill-no-home", ref)
	}
}

// TestInstallDetectedDryRunWritesNothing pins the preview: the plans name each
// create, and no target directory appears on disk.
func TestInstallDetectedDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	req := newRequest(t)
	req.DryRun = true
	claude := seedSkillsDir(t, req.Env["HOME"], ".claude")
	det, ref := skill.InstallDetected(context.Background(), req)
	if ref != nil {
		t.Fatalf("dry-run detect: %v", ref)
	}
	if want := []string{"claude"}; !slices.Equal(det.Agents, want) {
		t.Fatalf("detected agents = %v, want %v", det.Agents, want)
	}
	if det.Plans[0].Status != skill.StatusCreate || det.Plans[0].Written != 0 {
		t.Fatalf("dry-run plan = %+v", det.Plans[0])
	}
	if _, err := os.Stat(filepath.Join(claude, "verdict")); !os.IsNotExist(err) {
		t.Fatal("dry-run created a target")
	}
}

// TestInstallDetectedPerTargetSafety pins that every target keeps the
// single-target rules: a foreign target refuses without --force before ANY
// target is written, --force writes only verdict's payload, a live target then
// reads keep, and a target that is not a directory refuses even under --force.
//
//nolint:cyclop,gocognit,revive // the test walks the per-target safety matrix
func TestInstallDetectedPerTargetSafety(t *testing.T) {
	t.Parallel()
	req := newRequest(t)
	home := req.Env["HOME"]
	claude := seedSkillsDir(t, home, ".claude")
	codex := seedSkillsDir(t, home, ".codex")
	foreign := filepath.Join(claude, "verdict", "keep.txt")
	writeTestFile(t, foreign, "foreign bytes\n")
	codexTarget := filepath.Join(codex, "verdict")

	_, ref := skill.InstallDetected(context.Background(), req)
	if ref == nil || ref.Code != "skill-dir-foreign" {
		t.Fatalf("foreign refusal = %v, want skill-dir-foreign", ref)
	}
	if !strings.Contains(ref.Message, filepath.Join(claude, "verdict")) {
		t.Errorf("refusal does not name the blocked target: %q", ref.Message)
	}
	if _, err := os.Stat(codexTarget); !os.IsNotExist(err) {
		t.Fatal("a blocked target still wrote the other agent's target")
	}
	if got, err := os.ReadFile(foreign); err != nil || string(got) != "foreign bytes\n" {
		t.Fatalf("refusal touched the foreign file: %q, %v", got, err)
	}

	req.Force = true
	det, ref := skill.InstallDetected(context.Background(), req)
	if ref != nil {
		t.Fatalf("force detect: %v", ref)
	}
	if want := []string{"claude", "codex"}; !slices.Equal(det.Agents, want) {
		t.Fatalf("forced agents = %v, want %v", det.Agents, want)
	}
	for i := range det.Plans {
		if det.Plans[i].Written == 0 {
			t.Errorf("forced plan %d wrote nothing: %+v", i, det.Plans[i])
		}
	}
	if got, err := os.ReadFile(foreign); err != nil || string(got) != "foreign bytes\n" {
		t.Fatalf("force touched the foreign file: %q, %v", got, err)
	}
	for _, target := range []string{filepath.Join(claude, "verdict"), codexTarget} {
		if got := readReceiptMap(t, filepath.Join(target, skill.ReceiptName))["version"]; got != "1.2.3" {
			t.Errorf("receipt under %s = %v, want 1.2.3", target, got)
		}
	}

	req.Force = false
	det, ref = skill.InstallDetected(context.Background(), req)
	if ref != nil {
		t.Fatalf("re-run detect: %v", ref)
	}
	for i := range det.Plans {
		if det.Plans[i].Status != skill.StatusKeep || det.Plans[i].Written != 0 {
			t.Errorf("re-run plan %d = %+v, want keep", i, det.Plans[i])
		}
	}

	gemini := seedSkillsDir(t, home, ".gemini")
	if err := os.WriteFile(filepath.Join(gemini, "verdict"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	req.Force = true
	if _, ref := skill.InstallDetected(context.Background(), req); ref == nil || ref.Code != "skill-target-file" {
		t.Fatalf("target-file refusal = %v, want skill-target-file", ref)
	}
	if got, err := os.ReadFile(filepath.Join(gemini, "verdict")); err != nil || string(got) != "not a dir" {
		t.Fatalf("target file was modified: %q, %v", got, err)
	}
}

// TestInstallDetectedPathConflictWritesNothing pins that a path-shape conflict
// in a later target (a verdict-owned copy whose prompts/ is a file) refuses
// typed before the earlier target is written.
func TestInstallDetectedPathConflictWritesNothing(t *testing.T) {
	t.Parallel()
	req := newRequest(t)
	home := req.Env["HOME"]
	claude := seedSkillsDir(t, home, ".claude")
	codex := seedSkillsDir(t, home, ".codex")
	// Own codex first, then break its prompts/ into a file.
	own := newRequest(t)
	own.Env = map[string]string{"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config")}
	own.Agent = "codex"
	if _, ref := skill.Install(context.Background(), own); ref != nil {
		t.Fatalf("seed codex install: %v", ref)
	}
	prompts := filepath.Join(codex, "verdict", "prompts")
	if err := os.RemoveAll(prompts); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, prompts, "a file where a dir belongs\n")

	_, ref := skill.InstallDetected(context.Background(), req)
	if ref == nil || ref.Code != "skill-foreign-conflict" {
		t.Fatalf("path-conflict refusal = %v, want skill-foreign-conflict", ref)
	}
	if _, err := os.Stat(filepath.Join(claude, "verdict")); !os.IsNotExist(err) {
		t.Fatal("a refused run still wrote the earlier target")
	}
}

// TestInstallDetectedHonorsEnvLayouts pins detection through CLAUDE_CONFIG_DIR
// and XDG_CONFIG_HOME, the two overrides install-skill.sh also honors.
func TestInstallDetectedHonorsEnvLayouts(t *testing.T) {
	t.Parallel()
	req := newRequest(t)
	home := req.Env["HOME"]
	claudeCfg := filepath.Join(t.TempDir(), "claude-cfg")
	xdg := filepath.Join(t.TempDir(), "xdg")
	req.Env["CLAUDE_CONFIG_DIR"] = claudeCfg
	req.Env["XDG_CONFIG_HOME"] = xdg
	seedSkillsDir(t, home, ".claude")
	seedSkillsDir(t, claudeCfg)
	seedSkillsDir(t, xdg, "opencode")
	seedSkillsDir(t, home, ".config", "goose")

	det, ref := skill.InstallDetected(context.Background(), req)
	if ref != nil {
		t.Fatalf("detect: %v", ref)
	}
	if want := []string{"claude", "opencode"}; !slices.Equal(det.Agents, want) {
		t.Fatalf("detected agents = %v, want %v", det.Agents, want)
	}
	if want := filepath.Join(claudeCfg, "skills", "verdict"); det.Plans[0].Target != want {
		t.Fatalf("claude target = %q, want %q", det.Plans[0].Target, want)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "verdict")); !os.IsNotExist(err) {
		t.Fatal("installed into ~/.claude although CLAUDE_CONFIG_DIR is set")
	}
}

// TestInstallAgentsDedupesByResolvedTarget pins that two agent names mapped to
// one skills dir plan one install, under the first name.
func TestInstallAgentsDedupesByResolvedTarget(t *testing.T) {
	t.Parallel()
	req := newRequest(t)
	home := req.Env["HOME"]
	req.Env = map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": filepath.Join(home, ".agents")}
	req.DryRun = true
	det, ref := skill.InstallAgents(context.Background(), req, []string{"claude", "agents"})
	if ref != nil {
		t.Fatalf("InstallAgents: %v", ref)
	}
	if want := []string{"claude"}; !slices.Equal(det.Agents, want) {
		t.Fatalf("agents = %v, want %v", det.Agents, want)
	}
	if len(det.Plans) != 1 {
		t.Fatalf("plans = %+v, want one target", det.Plans)
	}
	if want := filepath.Join(home, ".agents", "skills", "verdict"); det.Plans[0].Target != want {
		t.Fatalf("target = %q, want %q", det.Plans[0].Target, want)
	}
}
