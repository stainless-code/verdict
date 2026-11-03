// Package record_test holds the golden contract for the Go record core:
// the harness runs the Go binary on hermetic fixture bundles and pins exit
// codes, stdout envelopes, findings.json, record.json, and every other
// store side effect against checked-in goldens (go-port 08: the bash
// comparison retired with the shell).
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

	"github.com/stainless-code/verdict/internal/parity"
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
// package.json, so the harness runs from worktrees and MAIN alike.
func findRepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("cannot locate test source")
	}
	dir := filepath.Dir(file)
	for range 10 {
		if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
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

// runOne runs the Go binary in dir, capturing exit code, streams, and the
// top-level store files. Stream capture happens after Run so the golden is
// real (a pre-Run snapshot would compare empty builders). extraEnv overlays
// additional variables (e.g. KB policy knobs) on the hermetic child
// environment.
func runOne(t *testing.T, dir, home string, args ...string) outputs {
	t.Helper()
	return runOneEnv(t, dir, home, nil, args...)
}

// runOneEnv is runOne with extra environment variables.
func runOneEnv(t *testing.T, dir, home string, extraEnv map[string]string, args ...string) outputs {
	t.Helper()
	seedControls(t, dir)
	cmd := sideCommand(t, args)
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

// sideCommand builds the Go command for one golden run.
func sideCommand(t *testing.T, args []string) *exec.Cmd {
	t.Helper()
	bin := filepath.Join(binDir, "verdict")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	return exec.CommandContext(t.Context(), bin, args...)
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

// goldenBoth runs the Go binary on hermetic fixtures and pins exit codes,
// streams, and every written file against the checked-in golden. setup
// writes the bundle into dir; fixtures must be deterministic.
func goldenBoth(t *testing.T, name string, slug string, setup func(t *testing.T, dir string), args ...string) {
	t.Helper()
	goldenBothEnv(t, name, slug, setup, nil, args...)
}

// goldenBothEnv is goldenBoth with extra environment variables.
func goldenBothEnv(t *testing.T, name string, slug string, setup func(t *testing.T, dir string), extraEnv map[string]string, args ...string) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		t.Helper()
		dir := t.TempDir()
		home := t.TempDir()
		setup(t, dir)
		full := append([]string{"record", "acme/app", slug}, args...)
		out := runOneEnv(t, dir, home, extraEnv, full...)
		pinGolden(t, name, dir, out)
	})
}

// pinGolden snapshots every file under dir and pins the scrubbed run
// against testdata/<name>.golden.
func pinGolden(t *testing.T, name, dir string, out outputs) {
	t.Helper()
	files := map[string]string{}
	if err := filepath.Walk(dir, collectGoldenFile(dir, files)); err != nil {
		t.Fatal(err)
	}
	parity.Check(t, name, parity.Render(parity.Outputs{
		Status: out.status, Stdout: out.stdout, Stderr: out.stderr, Files: files,
	}, []string{dir}))
}

// collectGoldenFile returns a Walk callback recording one file's bytes
// under the C/ tag, relative to root.
func collectGoldenFile(root string, files map[string]string) filepath.WalkFunc {
	return func(p string, fi os.FileInfo, err error) error {
		rel, rerr := filepath.Rel(root, p)
		if err != nil || rerr != nil || fi.IsDir() || isControlsArtifact(rel) {
			return err
		}
		raw, err := os.ReadFile(p)
		files["C/"+rel] = string(raw)
		return err
	}
}
