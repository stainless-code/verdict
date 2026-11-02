// Package record_test holds the shell-parity proof for the Go record core:
// a Go harness that runs BOTH scripts/review.sh record and the Go binary on
// identical fixture bundles and compares exit codes, stdout envelopes
// (normalizing only at/timestamps), findings.json, record.json, and every
// other store side effect byte-for-byte where bash writes deterministically.
package record_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

var (
	repoRoot string
	binDir   string
	stubBin  string
)

// TestMain builds the Go binary once and runs the parity suite.
func TestMain(m *testing.M) {
	os.Exit(runParityMain(m))
}

// runParityMain builds the Go binary and stub tooling once, runs the suite,
// and reports the exit code; setup failures clean up and return 2.
func runParityMain(m *testing.M) int {
	if err := setupParity(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		teardownParity()
		return 2
	}
	defer teardownParity()
	return m.Run()
}

// teardownParity removes the harness temp roots recorded by setupParity.
func teardownParity() {
	for _, dir := range []string{binDir, stubBin} {
		if dir == "" {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}
	binDir, stubBin = "", ""
}

// setupParity mirrors the shell's eval-harness preamble: a temp root holding
// the freshly built Go binary plus provenance inputs, and a stub bin dir.
func setupParity() error {
	var err error
	repoRoot, err = findRepoRoot()
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "verdict-parity-root")
	if err != nil {
		return err
	}
	binDir = tmp
	if err := buildParityBinary(tmp); err != nil {
		return err
	}
	// The binary resolves the repo root from its own directory (mirroring
	// the shell's script-dir root), so the harness plants identical copies
	// of the provenance inputs beside it.
	if err := plantParityInputs(tmp); err != nil {
		return err
	}
	stubBin, err = os.MkdirTemp("", "verdict-parity-stub")
	if err != nil {
		return err
	}
	stub := "#!/usr/bin/env bash\necho \"stub-gh $*\" >> \"${EVAL_STUBLOG:-/dev/null}\"\necho \"evals-stub-gh: network disabled ($*)\" >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(stubBin, "gh"), []byte(stub), 0o755); err != nil {
		return err
	}
	return nil
}

// buildParityBinary compiles the Go binary under test into tmp.
func buildParityBinary(tmp string) error {
	bin := filepath.Join(tmp, "verdict")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.CommandContext(context.Background(), "go", "build", "-o", bin, "./cmd/verdict")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("build binary: %w\n%s", err, out)
	}
	return nil
}

// plantParityInputs copies the provenance inputs beside the test binary.
func plantParityInputs(tmp string) error {
	for _, f := range []string{"package.json", "SKILL.md"} {
		if err := copyFile(filepath.Join(repoRoot, f), filepath.Join(tmp, f)); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(tmp, "prompts"), 0o755); err != nil {
		return err
	}
	for _, f := range []string{"system.md", "review.md", "diagrams.md"} {
		if err := copyFile(filepath.Join(repoRoot, "prompts", f), filepath.Join(tmp, "prompts", f)); err != nil {
			return err
		}
	}
	return nil
}

// findRepoRoot walks up from this source file to the checkout carrying
// scripts/review.sh, so the harness runs from worktrees and MAIN alike.
func findRepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("cannot locate test source")
	}
	dir := filepath.Dir(file)
	for range 10 {
		if _, err := os.Stat(filepath.Join(dir, "scripts", "review.sh")); err == nil {
			return dir, nil
		}
		dir = filepath.Dir(dir)
	}
	return "", fmt.Errorf("repo root not found above %s", file)
}

func copyFile(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, raw, 0o644)
}

var tsRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z`)

// scrub normalizes at/timestamps; everything else compares byte-exact.
func scrub(s string) string {
	return tsRe.ReplaceAllString(s, "0000-00-00T00:00:00Z")
}

// childEnv builds the hermetic environment both implementations share:
// isolated HOME, the network-dead gh stub first on PATH, no REVIEW_*
// leakage from the developer shell. extraEnv overlays test variables.
func childEnv(home, tmp string, extraEnv map[string]string) []string {
	env := scrubParentEnv(extraEnv)
	env = append(env, "HOME="+home, "TMPDIR="+tmp)
	path := os.Getenv("PATH")
	env = append(env, "PATH="+stubBin+string(os.PathListSeparator)+path)
	for k, v := range extraEnv {
		env = append(env, k+"="+v)
	}
	return env
}

// scrubParentEnv drops shell leakage (REVIEW_*, HOME, TMPDIR, NO_COLOR) and
// any variable the test overrides.
func scrubParentEnv(extraEnv map[string]string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if dropParentVar(kv, extraEnv) {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// dropParentVar reports whether a parent variable stays out of the harness.
func dropParentVar(kv string, extraEnv map[string]string) bool {
	for _, prefix := range []string{"REVIEW_", "XDG_CONFIG_HOME=", "NO_COLOR=", "HOME=", "TMPDIR="} {
		if strings.HasPrefix(kv, prefix) {
			return true
		}
	}
	if k, _, ok := strings.Cut(kv, "="); ok {
		_, override := extraEnv[k]
		return override
	}
	return false
}

// outputs captures one record run.
type outputs struct {
	files  map[string]string
	stdout string
	stderr string
	status int
}

// runOne runs a single implementation in dir, capturing exit code, streams,
// and the top-level store files. Stream capture happens after Run so the
// comparison is real (a pre-Run snapshot would compare empty builders).
// extraEnv overlays additional variables (e.g. KB policy knobs) on the
// hermetic child environment.
func runOne(t *testing.T, useGo bool, dir, home string, args ...string) outputs {
	t.Helper()
	return runOneEnv(t, useGo, dir, home, nil, args...)
}

// runOneEnv is runOne with extra environment variables.
func runOneEnv(t *testing.T, useGo bool, dir, home string, extraEnv map[string]string, args ...string) outputs {
	t.Helper()
	cmd := sideCommand(t, useGo, args)
	cmd.Dir = dir
	cmd.Env = childEnv(home, dir, extraEnv)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	out := outputs{status: 0, files: map[string]string{}}
	if err := cmd.Run(); err != nil {
		out.status = runStatus(t, err, args)
	}
	out.stdout = stdout.String()
	out.stderr = stderr.String()
	for _, rel := range []string{
		".review/store.json", ".review/kb.jsonl", ".review/kb-joins.jsonl",
	} {
		raw, err := os.ReadFile(filepath.Join(dir, rel))
		if err == nil {
			out.files[rel] = string(raw)
		}
	}
	return out
}

// sideCommand builds the bash or Go command for one parity side.
func sideCommand(t *testing.T, useGo bool, args []string) *exec.Cmd {
	t.Helper()
	if useGo {
		bin := filepath.Join(binDir, "verdict")
		if runtime.GOOS == "windows" {
			bin += ".exe"
		}
		return exec.CommandContext(t.Context(), bin, args...)
	}
	script := filepath.Join(repoRoot, "scripts", "review.sh")
	return exec.CommandContext(t.Context(), "bash", append([]string{script}, args...)...)
}

// runStatus maps a command error to its exit status, failing on start errors.
func runStatus(t *testing.T, err error, args []string) int {
	t.Helper()
	if exitErr, _ := errors.AsType[*exec.ExitError](err); exitErr != nil {
		return exitErr.ExitCode()
	}
	t.Fatalf("run %v: %v", args, err)
	return 0
}

// bundleFiles lists every file under dir/.review/<slug>.
func bundleFiles(t *testing.T, dir, slug string) map[string]string {
	t.Helper()
	out := map[string]string{}
	root := filepath.Join(dir, ".review", slug)
	if err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			//nolint:nilerr // WalkFunc nil means "skip this path", not success
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			//nolint:nilerr // unreadable fixture files are skipped, not fatal
			return nil
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			//nolint:nilerr // unrelativizable paths are skipped, not fatal
			return nil
		}
		out[rel] = string(raw)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// runBoth runs bash and Go on identical fixtures and compares everything the
// ticket's parity proof covers. setup writes the bundle into dir (called
// once per side, so fixtures must be deterministic).
func runBoth(t *testing.T, name string, slug string, setup func(t *testing.T, dir string), args ...string) {
	t.Helper()
	runBothEnv(t, name, slug, setup, nil, args...)
}

// runBothEnv is runBoth with extra environment variables for both sides.
func runBothEnv(t *testing.T, name string, slug string, setup func(t *testing.T, dir string), extraEnv map[string]string, args ...string) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		t.Helper()
		dirA := t.TempDir()
		dirB := t.TempDir()
		setup(t, dirA)
		setup(t, dirB)
		full := append([]string{"record", "acme/app", slug}, args...)
		a := runOneEnv(t, false, dirA, t.TempDir(), extraEnv, full...)
		b := runOneEnv(t, true, dirB, t.TempDir(), extraEnv, full...)
		compareOutputs(t, a, b)
		compareBundles(t, bundleFiles(t, dirA, slug), bundleFiles(t, dirB, slug))
	})
}

// compareOutputs compares exit codes, streams (timestamps scrubbed), and the
// top-level store files of the two sides.
func compareOutputs(t *testing.T, a, b outputs) {
	t.Helper()
	if a.status != b.status {
		t.Errorf("exit: bash=%d go=%d\nbash stderr:\n%s\ngo stderr:\n%s", a.status, b.status, a.stderr, b.stderr)
	}
	if scrub(a.stdout) != scrub(b.stdout) {
		t.Errorf("stdout differs:\n--- bash ---\n%s\n--- go ---\n%s", a.stdout, b.stdout)
	}
	if scrub(a.stderr) != scrub(b.stderr) {
		t.Errorf("stderr differs:\n--- bash ---\n%s\n--- go ---\n%s", a.stderr, b.stderr)
	}
	compareFiles(t, a.files, b.files)
}

// compareFiles compares the top-level store files, scrubbing timestamps.
func compareFiles(t *testing.T, fa, fb map[string]string) {
	t.Helper()
	seen := map[string]bool{}
	for k, va := range fa {
		seen[k] = true
		vb, ok := fb[k]
		compareFile(t, k, va, vb, ok)
	}
	for k := range fb {
		if !seen[k] {
			t.Errorf("store file presence differs: %s bash=absent go=present", k)
		}
	}
}

// compareFile compares one store file present on the bash side.
func compareFile(t *testing.T, k, va, vb string, ok bool) {
	t.Helper()
	if !ok {
		t.Errorf("store file presence differs: %s bash=present go=absent", k)
		return
	}
	if scrub(va) != scrub(vb) {
		t.Errorf("store file differs: %s\n--- bash ---\n%s\n--- go ---\n%s", k, va, vb)
	}
}

// compareBundles compares every bundle file of the two sides.
func compareBundles(t *testing.T, fa, fb map[string]string) {
	t.Helper()
	compareMissing(t, fa, fb)
	compareContents(t, fa, fb)
}

// compareMissing reports files present on one side only.
func compareMissing(t *testing.T, fa, fb map[string]string) {
	t.Helper()
	for k := range fa {
		if _, ok := fb[k]; !ok {
			checkPresence(t, k, true, false)
		}
	}
	for k := range fb {
		if _, ok := fa[k]; !ok {
			checkPresence(t, k, false, true)
		}
	}
}

// checkPresence reports a one-sided bundle file. Derived snapshots are
// compared like everything else now: both sides leave identical residues,
// so a presence gap is a real diff (R6).
func checkPresence(t *testing.T, k string, oka, okb bool) {
	t.Helper()
	t.Errorf("file presence differs: %s bash=%v go=%v", k, oka, okb)
}

// compareContents compares files present on both sides.
func compareContents(t *testing.T, fa, fb map[string]string) {
	t.Helper()
	for k, va := range fa {
		vb, ok := fb[k]
		if !ok {
			continue
		}
		if scrub(va) != scrub(vb) {
			t.Errorf("file differs: %s\n--- bash ---\n%s\n--- go ---\n%s", k, va, vb)
		}
	}
}
