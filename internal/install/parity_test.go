// Package install_test holds the shell-parity proof for the Go install
// verb: the shared harness runs BOTH scripts/review.sh install and the Go
// binary on identical fixtures and compares exit codes, stdout/stderr
// (timestamps scrubbed only), and every written file byte for byte,
// including receipts, skew nudges, and the foreign-owned/undetected roots.
package install_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stainless-code/verdict/internal/parity"
)

var harness *parity.Harness

// TestMain builds the Go binary once and runs the parity suite.
func TestMain(m *testing.M) {
	h, err := parity.Build()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	harness = h
	code := m.Run()
	harness.Teardown()
	os.Exit(code)
}

// installBoth runs one install case with dispatch-faithful args.
func installBoth(t *testing.T, name string, setup func(t *testing.T, dir, home string), args ...string) {
	t.Helper()
	bashArgs := append([]string{args[0]}, args[3:]...)
	harness.RunBothSplit(t, name, setup, nil, false, bashArgs, args)
}

// seedRoots creates the detected repo + home skill dirs with foreign
// skills and one unowned verdict/ slot.
func seedRoots(t *testing.T, dir, home string) {
	t.Helper()
	makeSkillRoots(t, dir, home)
	plantForeignSkills(t, dir, home)
}

// makeSkillRoots creates the empty skill roots.
func makeSkillRoots(t *testing.T, dir, home string) {
	t.Helper()
	for _, root := range []string{
		filepath.Join(dir, ".agents", "skills"),
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(home, ".claude", "skills"),
	} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// plantForeignSkills plants foreign skills and one unowned verdict slot.
func plantForeignSkills(t *testing.T, dir, home string) {
	t.Helper()
	plantForeignRepo(t, dir)
	plantForeignHome(t, home)
}

// plantForeignRepo plants the repo-side foreign skill.
func plantForeignRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".agents", "skills", "foreign-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(dir, ".agents", "skills", "foreign-skill", "SKILL.md")
	if err := os.WriteFile(foreign, []byte("foreign repo skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// plantForeignHome plants home-side foreign skills.
func plantForeignHome(t *testing.T, home string) {
	t.Helper()
	other := filepath.Join(home, ".claude", "skills", "other")
	if err := os.WriteFile(other, []byte("foreign home skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".agents", "skills", "verdict"), 0o755); err != nil {
		t.Fatal(err)
	}
	owned := filepath.Join(home, ".agents", "skills", "verdict", "SKILL.md")
	if err := os.WriteFile(owned, []byte("someone else's verdict copy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ownedRoots crafts owned verdict/ slots with identical payload + valid
// receipts (from the harness tool copy both sides share), so the run reads
// keep.
func ownedRoots(t *testing.T, dir, home string) {
	t.Helper()
	seedRoots(t, dir, home)
	version := toolVersion(t)
	for _, root := range []string{
		filepath.Join(dir, ".agents", "skills"),
		filepath.Join(home, ".agents", "skills"),
	} {
		plantOwned(t, filepath.Join(root, "verdict"), version)
	}
}

// staleRoots crafts owned slots with a stale receipt version.
func staleRoots(t *testing.T, dir, home string) {
	t.Helper()
	seedRoots(t, dir, home)
	for _, root := range []string{
		filepath.Join(dir, ".agents", "skills"),
		filepath.Join(home, ".claude", "skills"),
	} {
		plantOwned(t, filepath.Join(root, "verdict"), "0.0.0-stale")
	}
}

// plantOwned copies the harness tool payload into the slot with a receipt.
func plantOwned(t *testing.T, target, version string) {
	t.Helper()
	copyTree(t, filepath.Join(harness.BinDir, "SKILL.md"), filepath.Join(target, "SKILL.md"))
	copyPayloadSubs(t, target)
	writeOwnedReceipt(t, target, version)
}

// copyPayloadSubs copies prompts and scripts payload dirs.
func copyPayloadSubs(t *testing.T, target string) {
	t.Helper()
	for _, sub := range []string{"prompts", "scripts"} {
		copyPayloadSub(t, target, sub)
	}
}

// copyPayloadSub copies one payload subdir into the slot.
func copyPayloadSub(t *testing.T, target, sub string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(harness.BinDir, sub))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := os.MkdirAll(filepath.Join(target, sub), 0o755); err != nil {
			t.Fatal(err)
		}
		copyTree(t, filepath.Join(harness.BinDir, sub, e.Name()), filepath.Join(target, sub, e.Name()))
	}
}

// writeOwnedReceipt writes the pretty owned receipt for a slot.
func writeOwnedReceipt(t *testing.T, target, version string) {
	t.Helper()
	// Pretty receipt like `jq -n` writes it.
	receipt := "{\n  \"tool\": \"verdict\",\n  \"version\": \"" + version + "\",\n  \"installed_at\": \"2026-01-01T00:00:00Z\"\n}\n"
	if err := os.WriteFile(filepath.Join(target, ".verdict-managed"), []byte(receipt), 0o644); err != nil {
		t.Fatal(err)
	}
}

// copyTree copies one file's bytes, creating parents.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// toolVersion reads the harness tool copy's version.
func toolVersion(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(harness.BinDir, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if v, ok := doc["version"].(string); ok {
		return v
	}
	return "0.0.0-unknown"
}

// TestInstallParity proves byte parity across previews, applies, keeps,
// skew nudges, and refusals.
func TestInstallParity(t *testing.T) {
	installBoth(t, "preview-refuse", seedRoots,
		"install", "", "")
	installBoth(t, "preview-refuse-json", seedRoots,
		"install", "", "", "--json")
	installBoth(t, "apply", seedRoots,
		"install", "", "", "--yes")
	installBoth(t, "apply-json", seedRoots,
		"install", "", "", "--yes", "--json")
	installBoth(t, "no-roots", func(_ *testing.T, _, _ string) {},
		"install", "", "", "--yes")
	installBoth(t, "keep", ownedRoots,
		"install", "", "")
	installBoth(t, "skew-nudge", staleRoots,
		"install", "", "")
	installBoth(t, "skew-refresh", staleRoots,
		"install", "", "", "--yes", "--json")
	installBoth(t, "unknown-flag", seedRoots,
		"install", "", "", "--bogus")
}
