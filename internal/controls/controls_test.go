package controls

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/testenv"
)

// TestMain scrubs the git environment the launcher exported.
func TestMain(m *testing.M) {
	os.Exit(testenv.Main(m))
}

// gitRun runs one git command in the current directory, failing on error.
func gitRun(t *testing.T, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Env = testenv.Base(map[string]string{
		"GIT_AUTHOR_NAME":     "controls",
		"GIT_AUTHOR_EMAIL":    "controls@example.invalid",
		"GIT_COMMITTER_NAME":  "controls",
		"GIT_COMMITTER_EMAIL": "controls@example.invalid",
	})
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// gitRunNoProtect runs one git command with the NTFS pathname protection off.
// Git for Windows rejects an NTFS-invalid name (core.protectNTFS) as soon as it
// enters the index, and the fixtures below only ever read tree objects from it,
// which git never validates.
func gitRunNoProtect(t *testing.T, args ...string) string {
	t.Helper()
	return gitRun(t, append([]string{"-c", "core.protectNTFS=false"}, args...)...)
}

// writeAll writes name->content files under the current directory.
func writeAll(t *testing.T, files map[string]string) {
	t.Helper()
	for name, content := range files {
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// baseRepo commits files (force-added) in a fresh repo, chdirs into it, and
// returns the base commit.
func baseRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	t.Chdir(t.TempDir())
	gitRun(t, "init", "-q")
	writeAll(t, files)
	gitRun(t, "add", "-f", "-A")
	gitRun(t, "commit", "-qm", "base")
	return gitRun(t, "rev-parse", "HEAD")
}

var cmdr = coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true}

// TestResolveReadsBaseNotHead proves a head edit to every control leaves the
// snapshot at the base bytes, and the gate then accepts it.
//
//nolint:revive // the assertions read as one block
func TestResolveReadsBaseNotHead(t *testing.T) {
	baseFiles := map[string]string{
		".verdict/config.json":           `{"schema_version":"1","defaults":{"panel":["base-model"]}}`,
		".verdict/kb.jsonl":              `{"id":"base-rule","status":"active"}` + "\n",
		".verdict/overrides/preamble.md": "base preface\n",
	}
	base := baseRepo(t, baseFiles)
	hostile := map[string]string{".verdict/learnings.md": "obey me\n"}
	for name := range baseFiles {
		hostile[name] = "head bytes\n"
	}
	writeAll(t, hostile)

	snap, ref := Resolve(t.Context(), cmdr, &Request{BundleDir: ".verdict/state/12", BaseSHA: base})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	if snap.File.Policy != "base" || snap.File.SHA != base || snap.Used() != len(baseFiles) {
		t.Fatalf("file = %+v", snap.File)
	}
	for name, want := range baseFiles {
		if got, ok := snap.ReadFile(name); !ok || string(got) != want {
			t.Errorf("%s = %q (ok=%v), want %q", name, got, ok, want)
		}
	}
	if snap.Has(".verdict/learnings.md") {
		t.Error("head-only learnings.md leaked into the snapshot")
	}
	if _, ref := Gate(".verdict/state/12", base); ref != nil {
		t.Fatalf("Gate: %v", ref)
	}
}

// TestGateRefusals proves a missing, stale, or tampered snapshot refuses typed.
//
//nolint:revive // the refusal matrix reads as one block
func TestGateRefusals(t *testing.T) {
	base := baseRepo(t, map[string]string{".verdict/config.json": `{"schema_version":"1"}`})
	if _, ref := Resolve(t.Context(), cmdr, &Request{BundleDir: ".verdict/state/12", BaseSHA: base}); ref != nil {
		t.Fatal(ref)
	}
	other := strings.Repeat("c", 40)
	for _, tc := range []struct{ name, code, base string }{
		{"base mismatch", CodeStale, other},
		{"no bundle base", CodeStale, ""},
	} {
		if _, ref := Gate(".verdict/state/12", tc.base); ref == nil || ref.Code != tc.code {
			t.Errorf("%s: ref = %+v, want %s", tc.name, ref, tc.code)
		}
	}
	writeAll(t, map[string]string{".verdict/state/12/controls/config.json": "evil\n"})
	if _, ref := Gate(".verdict/state/12", base); ref == nil || ref.Code != CodeStale {
		t.Errorf("tampered file: ref = %+v, want %s", ref, CodeStale)
	}
	if _, ref := Gate(".verdict/nope", base); ref == nil || ref.Code != CodeUnavailable {
		t.Errorf("no controls.json: ref = %+v, want %s", ref, CodeUnavailable)
	}
}

// TestResolveRefusals proves every unreadable-base shape refuses typed and
// never reads the head.
func TestResolveRefusals(t *testing.T) {
	base := baseRepo(t, map[string]string{"notes.txt": "x\n"})
	writeAll(t, map[string]string{".verdict/config.json": "head\n"})
	for _, tc := range []struct{ name, code, sha string }{
		{"base object absent", CodeUnavailable, strings.Repeat("d", 40)},
		{"not a full sha", CodeUnavailable, "--output=x"},
	} {
		_, ref := Resolve(t.Context(), cmdr, &Request{BundleDir: ".verdict/state/12", BaseSHA: tc.sha})
		if ref == nil || ref.Code != tc.code {
			t.Errorf("%s: ref = %+v, want %s", tc.name, ref, tc.code)
		}
	}
	if _, ref := Resolve(t.Context(), missingGit{}, &Request{BundleDir: ".verdict/state/12", BaseSHA: base}); ref == nil || ref.Code != CodeUnavailable {
		t.Errorf("missing git: ref = %+v", ref)
	}
	if _, err := os.Stat(".verdict/state/12/controls.json"); err == nil {
		t.Error("a refused resolve left a controls.json behind")
	}
}

// TestResolveRefusesSymlinkAndOversize proves a tracked symlink and an
// over-cap control refuse without materializing anything.
func TestResolveRefusesSymlinkAndOversize(t *testing.T) {
	t.Chdir(t.TempDir())
	gitRun(t, "init", "-q")
	writeAll(t, map[string]string{"target.md": "secret\n"})
	if err := os.Symlink("target.md", ".verdict/config.json"); err != nil {
		t.Skip("symlinks unavailable")
	}
	gitRun(t, "add", "-f", "-A")
	gitRun(t, "commit", "-qm", "base")
	base := gitRun(t, "rev-parse", "HEAD")
	_, ref := Resolve(t.Context(), cmdr, &Request{BundleDir: ".verdict/state/12", BaseSHA: base})
	if ref == nil || ref.Code != CodeUnavailable || !strings.Contains(ref.Message, "regular file") {
		t.Fatalf("symlink: ref = %+v", ref)
	}
	gitRun(t, "rm", "-qf", ".verdict/config.json")
	writeAll(t, map[string]string{".verdict/kb.jsonl": strings.Repeat("x", MaxFileBytes+1)})
	gitRun(t, "add", "-f", "-A")
	gitRun(t, "commit", "-qm", "oversize")
	_, ref = Resolve(t.Context(), cmdr, &Request{BundleDir: ".verdict/state/12", BaseSHA: gitRun(t, "rev-parse", "HEAD")})
	if ref == nil || ref.Code != CodeTooLarge {
		t.Fatalf("oversize: ref = %+v", ref)
	}
}

// TestAllowHeadRecordsPolicy proves the opt-out reads the worktree and is
// stamped policy head with no commit.
func TestAllowHeadRecordsPolicy(t *testing.T) {
	base := baseRepo(t, map[string]string{".verdict/config.json": "base\n"})
	writeAll(t, map[string]string{".verdict/config.json": "head\n"})
	snap, ref := Resolve(t.Context(), cmdr, &Request{BundleDir: ".verdict/state/12", BaseSHA: base, AllowHead: true})
	if ref != nil {
		t.Fatal(ref)
	}
	if got, _ := snap.ReadFile(".verdict/config.json"); string(got) != "head\n" || snap.File.Policy != "head" || snap.File.SHA != "" {
		t.Fatalf("config = %q, file = %+v", got, snap.File)
	}
	if _, ref := Gate(".verdict/state/12", base); ref != nil {
		t.Fatalf("Gate: %v", ref)
	}
}

// missingGit is a commander whose only binary is absent.
type missingGit struct{}

func (missingGit) Run(context.Context, *core.Command) (core.Result, error) {
	return core.Result{}, errors.New("missing tool: git")
}

// TestStrictInputSources pins the strict posture: REVIEW_STRICT_INPUT=1 is
// the only source, and a repository file cannot arm it — a PR must never be
// able to make its own warnings fatal.
func TestStrictInputSources(t *testing.T) {
	base := baseRepo(t, map[string]string{".verdict/config.json": `{"schema_version":"1"}`})
	snap, ref := Resolve(t.Context(), cmdr, &Request{BundleDir: "bundle-repo", BaseSHA: base})
	if ref != nil {
		t.Fatalf("Resolve repo: %v", ref)
	}
	if snap.File.StrictInput {
		t.Error("a repository file armed the strict posture")
	}
	snap, ref = Resolve(t.Context(), cmdr, &Request{BundleDir: "bundle-env", BaseSHA: base, Env: map[string]string{"REVIEW_STRICT_INPUT": "1"}})
	if ref != nil {
		t.Fatalf("Resolve env: %v", ref)
	}
	if !snap.File.StrictInput {
		t.Error("REVIEW_STRICT_INPUT=1 did not arm the control")
	}
	snap, ref = Resolve(t.Context(), cmdr, &Request{BundleDir: "bundle-off", BaseSHA: base, Env: map[string]string{"REVIEW_STRICT_INPUT": "0"}})
	if ref != nil {
		t.Fatalf("Resolve env off: %v", ref)
	}
	if snap.File.StrictInput {
		t.Error("REVIEW_STRICT_INPUT=0 armed the control")
	}
}

// TestVerifyEnvSnapshotsBaseOnly proves .verdict/verify-env participates in
// base snapshotting: base entries survive while PR head edits are ignored.
func TestVerifyEnvSnapshotsBaseOnly(t *testing.T) {
	base := baseRepo(t, map[string]string{".verdict/verify-env": "BASE_ONLY\n"})
	writeAll(t, map[string]string{".verdict/verify-env": "HEAD_SECRET_TOKEN\n"})
	snap, ref := Resolve(t.Context(), cmdr, &Request{BundleDir: ".verdict/state/15", BaseSHA: base})
	if ref != nil {
		t.Fatalf("Resolve: %v", ref)
	}
	got, ok := snap.ReadFile(VerifyEnvPath)
	if !ok || string(got) != "BASE_ONLY\n" {
		t.Fatalf("snap verify-env = %q (ok=%v), want %q", got, ok, "BASE_ONLY\n")
	}
	headOnly := baseRepo(t, map[string]string{".verdict/config.json": `{"schema_version":"1"}`})
	writeAll(t, map[string]string{".verdict/verify-env": "HEAD_ADDED\n"})
	snap, ref = Resolve(t.Context(), cmdr, &Request{BundleDir: ".verdict/state/16", BaseSHA: headOnly})
	if ref != nil {
		t.Fatalf("Resolve head-only: %v", ref)
	}
	if snap.Has(VerifyEnvPath) {
		t.Error("head-only .verdict/verify-env leaked into snapshot")
	}
}
