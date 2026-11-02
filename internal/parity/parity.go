// Package parity provides the shell-parity test harness shared by the
// verb ports: it builds the Go binary once, runs BOTH scripts/review.sh
// and the binary on identical fixtures, and compares everything observable
// — exit codes, stdout/stderr (timestamps scrubbed only), and every file
// written anywhere (bundle, .review/ stores, kb files, HOME skill trees),
// plus artifact presence (R6). A normalizer that hides real diffs is a
// defect, so the only scrub is the timestamp shape, and the only carve-out
// is the documented shell-echo platform variance on one progress line.
package parity

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Harness temp-dir and walk-up-depth constants (revive add-constant:
// no magic numbers in production code).
const (
	// dirPerm grants owner+group+other traversal for harness sandboxes;
	// file bytes (not modes) are what the snapshots compare.
	dirPerm = 0o755
	// filePerm restricts harness-copied fixtures to owner read/write.
	filePerm = 0o600
	// maxRootDepth bounds the walk-up search for the checkout root.
	maxRootDepth = 10
	// numRoots is the [sandbox, home] x [bash, go] root count scrubbed
	// to placeholders before comparison.
	numRoots = 4
	// stubPerm marks the gh stub executable; the stub must run first on
	// PATH for the network-dead hermetic runs.
	stubPerm = 0o755
)

// Harness carries the built binary, stub tooling, and repo root.
type Harness struct {
	RepoRoot string
	BinDir   string
	StubBin  string
}

// Build creates the harness binary and stub tooling once per test
// package. It mirrors the eval-harness preamble: a temp root holding the
// freshly built binary plus provenance inputs, and a network-dead gh
// stub. The caller runs Teardown when done.
func Build() (*Harness, error) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "verdict-parity-root")
	if err != nil {
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return nil, fmt.Errorf("parity temp root: %v", err)
	}
	if err := buildBinary(repoRoot, tmp); err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}
	if err := copyProvenance(repoRoot, tmp); err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}
	stubBin, err := writeStub()
	if err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}
	return &Harness{RepoRoot: repoRoot, BinDir: tmp, StubBin: stubBin}, nil
}

// buildBinary compiles the CLI under test into dir.
func buildBinary(repoRoot, dir string) error {
	bin := filepath.Join(dir, "verdict")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	build := exec.CommandContext(context.Background(), "go", "build", "-o", bin, "./cmd/verdict")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return fmt.Errorf("build binary: %v\n%s", err, out)
	}
	return nil
}

// copyProvenance plants the provenance inputs beside the test binary (the
// binary resolves the repo root from its own directory; planted copies let
// the harness run from worktrees and MAIN alike).
func copyProvenance(repoRoot, tmp string) error {
	for _, f := range []string{"package.json", "SKILL.md"} {
		if err := copyFile(filepath.Join(repoRoot, f), filepath.Join(tmp, f)); err != nil {
			//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
			return fmt.Errorf("provenance %s: %v", f, err)
		}
	}
	for _, dir := range []string{"prompts", "scripts"} {
		if err := copyProvenanceDir(repoRoot, tmp, dir); err != nil {
			return err
		}
	}
	return nil
}

// copyProvenanceDir copies the flat files of one provenance directory.
func copyProvenanceDir(repoRoot, tmp, dir string) error {
	if err := os.MkdirAll(filepath.Join(tmp, dir), dirPerm); err != nil {
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return fmt.Errorf("provenance dir %s: %v", dir, err)
	}
	entries, err := os.ReadDir(filepath.Join(repoRoot, dir))
	if err != nil {
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return fmt.Errorf("provenance dir %s: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := copyFile(filepath.Join(repoRoot, dir, e.Name()), filepath.Join(tmp, dir, e.Name())); err != nil {
			//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
			return fmt.Errorf("provenance %s/%s: %v", dir, e.Name(), err)
		}
	}
	return nil
}

// stubScript is the network-dead gh stub: it logs and fails every call,
// so the parity runs never touch the network.
const stubScript = "#!/usr/bin/env bash\necho \"stub-gh $*\" >> \"${EVAL_STUBLOG:-/dev/null}\"\n" +
	"echo \"evals-stub-gh: network disabled ($*)\" >&2\nexit 1\n"

// writeStub creates the network-dead gh stub directory.
func writeStub() (string, error) {
	stubBin, err := os.MkdirTemp("", "verdict-parity-stub")
	if err != nil {
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return "", fmt.Errorf("stub temp dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stubBin, "gh"), []byte(stubScript), stubPerm); err != nil {
		os.RemoveAll(stubBin)
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return "", fmt.Errorf("write gh stub: %v", err)
	}
	return stubBin, nil
}

// Teardown removes the harness temp roots.
func (h *Harness) Teardown() {
	os.RemoveAll(h.BinDir)
	os.RemoveAll(h.StubBin)
	h.BinDir, h.StubBin = "", ""
}

// findRepoRoot walks up from this source file to the checkout carrying
// scripts/review.sh.
func findRepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		//nolint:wrapcheck // fresh error created here; nothing to wrap
		return "", errors.New("cannot locate test source")
	}
	dir := filepath.Dir(file)
	for range maxRootDepth {
		if _, err := os.Stat(filepath.Join(dir, "scripts", "review.sh")); err == nil {
			return dir, nil
		}
		dir = filepath.Dir(dir)
	}
	//nolint:wrapcheck // fresh error created here; nothing to wrap
	return "", fmt.Errorf("repo root not found above %s", file)
}

// copyFile copies one file's bytes with owner-only permissions. Both
// paths derive from the harness temp dir and the repo checkout listing;
// no external input reaches them.
func copyFile(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return fmt.Errorf("read %s: %v", src, err)
	}
	//nolint:gosec // G703: dst is Join()s of the harness temp dir and repo-listed names; no external input reaches it.
	if err := os.WriteFile(dst, raw, filePerm); err != nil {
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return fmt.Errorf("write %s: %v", dst, err)
	}
	return nil
}

// tsRe scrubs timestamps; everything else compares byte-exact.
var tsRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z`)

// killNoticeRe drops the shell's job-control kill notice
// (`path: line N: PID Killed: 9  bash -c ...`) from stderr. The notice
// carries the killed PID, so it can never be byte-stable across runs
// (not even bash-vs-bash); the kill itself stays proven by the 137 exit,
// the timedOut flag, and the timeout marker, which all still compare.
// A verifyCmd's own stderr never reaches the script's stderr (it is
// redirected into .verify.out), so no command output can match this.
var killNoticeRe = regexp.MustCompile(`(?m)^.*Killed: 9.*\n?`)

// npmLogRe scrubs npm's "complete log" trailer. On failure npm prints the
// timestamped debug-log path into its own output, so the path differs by
// construction between runs (not even bash-vs-bash is stable); the failure
// itself stays byte-compared, only the log pointer is normalized.
var npmLogRe = regexp.MustCompile(`A complete log of this run can be found in: [^ \n]*`)

// curlMsRe scrubs curl's connection-failure timing (`after 0 ms` vs
// `after 1 ms`). The millisecond value is scheduling noise that differs by
// construction between any two runs (not even bash-vs-bash is stable);
// the failure itself stays byte-compared, only the timing is normalized.
var curlMsRe = regexp.MustCompile(`(Failed to connect to \S+ port \d+ after )\d+( ms)`)

// scrub normalizes timestamps, npm log pointers, and nothing else (R6:
// the sanctioned scrub, extended only for tool-runtime metadata that
// differs by construction).
func scrub(s string) string {
	s = tsRe.ReplaceAllString(s, "0000-00-00T00:00:00Z")
	s = npmLogRe.ReplaceAllString(s, "A complete log of this run can be found in: LOG")
	return curlMsRe.ReplaceAllString(s, "${1}0${2}")
}

// Outputs captures one implementation run.
type Outputs struct {
	Files  map[string]string
	Stdout string
	Stderr string
	Status int
}

// envDropPrefixes are stripped from the developer shell before the
// hermetic child environment is built.
var envDropPrefixes = []string{"REVIEW_", "XDG_CONFIG_HOME=", "NO_COLOR=", "HOME=", "TMPDIR="}

// childEnv builds the hermetic environment both implementations share:
// isolated HOME, the network-dead gh stub first on PATH, no REVIEW_*
// leakage from the developer shell. extraEnv overlays test variables.
func childEnv(home string, extraEnv map[string]string, stubBin string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !keepEnv(kv, extraEnv) {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "HOME="+home)
	path := os.Getenv("PATH")
	env = append(env, "PATH="+stubBin+string(os.PathListSeparator)+path)
	for k, v := range extraEnv {
		env = append(env, k+"="+v)
	}
	return env
}

// keepEnv reports whether a parent-process entry survives into the
// hermetic child environment.
func keepEnv(kv string, extraEnv map[string]string) bool {
	for _, prefix := range envDropPrefixes {
		if strings.HasPrefix(kv, prefix) {
			return false
		}
	}
	if k, _, ok := strings.Cut(kv, "="); ok {
		if _, override := extraEnv[k]; override {
			return false
		}
	}
	return true
}

// Run runs a single implementation in dir, capturing exit code, streams,
// and the written tree. extraEnv overlays additional variables.
func (h *Harness) Run(t *testing.T, useGo bool, dir, home string, extraEnv map[string]string, args ...string) Outputs {
	t.Helper()
	var cmd *exec.Cmd
	//nolint:gosec // G204: the harness intentionally executes the CLI under test with fixture args.
	if useGo {
		bin := filepath.Join(h.BinDir, "verdict")
		if runtime.GOOS == "windows" {
			bin += ".exe"
		}
		cmd = exec.CommandContext(t.Context(), bin, args...)
	} else {
		script := filepath.Join(h.RepoRoot, "scripts", "review.sh")
		cmd = exec.CommandContext(t.Context(), "bash", append([]string{script}, args...)...)
	}
	cmd.Dir = dir
	cmd.Env = childEnv(home, extraEnv, h.StubBin)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	out := Outputs{Status: 0, Files: map[string]string{}}
	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			out.Status = exitErr.ExitCode()
		} else {
			t.Fatalf("run %v: %v", args, err)
		}
	}
	out.Stdout = stdout.String()
	out.Stderr = stderr.String()
	out.Files = snapshotTree(t, "C", dir)
	out.Files = mergeMaps(out.Files, snapshotTree(t, "H", home))
	return out
}

// snapshotTree reads every file under root (except .git internals),
// keyed by a tag plus the relative path. Unreadable entries abort the
// snapshot: silently skipping them would hide real diffs.
func snapshotTree(t *testing.T, tag, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, snapshotVisit(tag, root, out))
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return out
}

// snapshotVisit returns the Walk callback recording file bytes into out.
func snapshotVisit(tag, root string, out map[string]string) filepath.WalkFunc {
	return func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			//nolint:errorlint // %v seals the boundary by design (GO-ERRORS-03)
			return fmt.Errorf("walk %s: %v", p, err)
		}
		if fi.IsDir() || isGitPath(p) {
			return nil
		}
		return snapshotFile(tag, root, out, p)
	}
}

// snapshotFile records one file's bytes into out.
func snapshotFile(tag, root string, out map[string]string, p string) error {
	raw, err := os.ReadFile(p)
	if err != nil {
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return fmt.Errorf("snapshot %s: %v", p, err)
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return fmt.Errorf("rel %s: %v", p, err)
	}
	out[tag+"/"+rel] = string(raw)
	return nil
}

// isGitPath skips .git internals (hashes and timestamps, never graded;
// the evals grade worktree files, not the object store).
func isGitPath(p string) bool {
	return slices.Contains(strings.Split(filepath.ToSlash(p), "/"), ".git")
}

// mergeMaps unions two file maps.
func mergeMaps(a, b map[string]string) map[string]string {
	maps.Copy(a, b)
	return a
}

// RunBoth runs bash and Go on identical fixtures and compares everything.
// setup writes the bundle into dir (called once per side with a fresh
// sandbox and home, so fixtures must be deterministic). extraEnv overlays
// variables on both sides. carveVerify Echo applies the documented
// shell-echo carve-out to the `» verify: ` progress line when the command
// carries a backslash.
func (h *Harness) RunBoth(
	t *testing.T, name string,
	setup func(t *testing.T, dir, home string),
	extraEnv map[string]string, carveVerifyEcho bool, args ...string,
) {
	t.Helper()
	h.RunBothSplit(t, name, setup, extraEnv, carveVerifyEcho, args, args)
}

// RunBothSplit is RunBoth with per-side args. Position-less verbs (learn,
// init, install) take no repo/pr positionals on the shell side, while the
// dispatched binary always receives cmd+repo+pr+rest (empty positionals),
// so those verbs pass bashArgs without and goArgs with the empty slots.
func (h *Harness) RunBothSplit(
	t *testing.T, name string,
	setup func(t *testing.T, dir, home string),
	extraEnv map[string]string, carveVerifyEcho bool, bashArgs, goArgs []string,
) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		t.Helper()
		dirA := t.TempDir()
		homeA := filepath.Join(t.TempDir(), "home")
		dirB := t.TempDir()
		homeB := filepath.Join(t.TempDir(), "home")
		if err := os.MkdirAll(homeA, dirPerm); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(homeB, dirPerm); err != nil {
			t.Fatal(err)
		}
		setup(t, dirA, homeA)
		setup(t, dirB, homeB)
		roots := [numRoots]string{dirA, homeA, dirB, homeB}
		a := h.Run(t, false, dirA, homeA, extraEnv, bashArgs...)
		b := h.Run(t, true, dirB, homeB, extraEnv, goArgs...)
		compareOutputs(t, a, b, carveVerifyEcho, roots)
	})
}

// compareOutputs compares exit codes, streams, and every written file of
// the two sides. Timestamps scrub to a placeholder (the sanctioned scrub);
// the two sandbox roots and homes scrub to placeholders too — they differ
// by construction (separate temp dirs, like the eval suite's scrub_slug),
// so only bytes beyond the roots can fail the comparison.
func compareOutputs(t *testing.T, a, b Outputs, carveVerifyEcho bool, roots [numRoots]string) {
	t.Helper()
	compareStreams(t, a, b, carveVerifyEcho, roots)
	compareFiles(t, a, b, roots)
}

// compareStreams compares exit codes and captured streams.
func compareStreams(t *testing.T, a, b Outputs, carveVerifyEcho bool, roots [numRoots]string) {
	t.Helper()
	if a.Status != b.Status {
		t.Errorf("exit: bash=%d go=%d\nbash stderr:\n%s\ngo stderr:\n%s",
			a.Status, b.Status, scrubRoots(a.Stderr, roots), scrubRoots(b.Stderr, roots))
	}
	as, bs := scrub(scrubRoots(a.Stdout, roots)), scrub(scrubRoots(b.Stdout, roots))
	if carveVerifyEcho {
		as = carveVerifyLine(as)
		bs = carveVerifyLine(bs)
	}
	if as != bs {
		t.Errorf("stdout differs:\n--- bash ---\n%s\n--- go ---\n%s", a.Stdout, b.Stdout)
	}
	ae, be := scrub(scrubRoots(a.Stderr, roots)), scrub(scrubRoots(b.Stderr, roots))
	ae, be = killNoticeRe.ReplaceAllString(ae, ""), killNoticeRe.ReplaceAllString(be, "")
	if ae != be {
		t.Errorf("stderr differs:\n--- bash ---\n%s\n--- go ---\n%s", a.Stderr, b.Stderr)
	}
}

// compareFiles compares every written file byte for byte.
func compareFiles(t *testing.T, a, b Outputs, roots [numRoots]string) {
	t.Helper()
	seen := map[string]bool{}
	for k, va := range a.Files {
		seen[k] = true
		vb, ok := b.Files[k]
		if !ok {
			t.Errorf("file presence differs: %s bash=present go=absent", k)
			continue
		}
		compareFile(t, k, va, vb, roots)
	}
	for k := range b.Files {
		if !seen[k] {
			t.Errorf("file presence differs: %s bash=absent go=present", k)
		}
	}
}

// compareFile compares one file present on the bash side.
func compareFile(t *testing.T, k, va, vb string, roots [numRoots]string) {
	t.Helper()
	fa, fb := scrub(scrubRoots(va, roots)), scrub(scrubRoots(vb, roots))
	if fa != fb {
		t.Errorf("file differs: %s\n--- bash ---\n%s\n--- go ---\n%s", k, va, vb)
	}
}

// scrubRoots normalizes the two sandbox roots and homes to placeholders.
// The four roots are pairwise distinct temp dirs; replacement order puts
// the longer (home) paths first so nested prefixes cannot shadow.
func scrubRoots(s string, roots [numRoots]string) string {
	ordered := append([]string{}, roots[:]...)
	slices.SortFunc(ordered, func(a, b string) int { return len(b) - len(a) })
	for _, r := range ordered {
		if r == "" {
			continue
		}
		s = strings.ReplaceAll(s, r, "ROOT")
	}
	return s
}

// carveVerifyLine drops the `» verify: ` progress line from a stream. The
// shell prints the raw command through echo, whose backslash interpretation
// differs across bash versions (macOS 3.2 literal vs GNU interpretation),
// so the line is not part of the stable contract when the command carries
// a backslash; the envelope, files, and exit still compare byte-exact.
func carveVerifyLine(s string) string {
	var out []string
	for ln := range strings.SplitSeq(s, "\n") {
		if strings.HasPrefix(ln, "» verify: ") && strings.Contains(ln, "\\") {
			continue
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}
