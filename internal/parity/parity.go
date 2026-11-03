// Package parity provides the Go golden-test harness shared by the verb
// ports: it builds the Go binary once, runs it on hermetic fixtures, and
// pins everything observable — exit codes, stdout/stderr, and every file
// written anywhere (bundle, .review/ stores, kb files, HOME skill trees),
// plus artifact presence (R6) — against checked-in golden files (go-port
// 08: the bash comparison retired with the shell). A normalizer that hides
// real diffs is a defect, so the only scrubs are timestamps, the prompt
// nonce, and curl's connection timing.
package parity

import (
	"context"
	"encoding/json"
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
	"sync"
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
	// stubPerm marks the gh stub executable; the stub must run first on
	// PATH for the network-dead hermetic runs.
	stubPerm = 0o755
)

// osWindows names the Windows GOOS value (avoiding repeated literals).
const osWindows = "windows"

// Harness carries the built binary, stub tooling, and repo root.
type Harness struct {
	RepoRoot string
	BinDir   string
	StubBin  string
	// shim is the Windows stub launcher (see buildShim); "" elsewhere.
	shim string
}

// Build creates the harness binary and stub tooling once per test
// package. It mirrors the eval-harness preamble: a temp root holding the
// freshly built binary plus provenance inputs, a network-dead gh stub,
// and deterministic toolchain stubs (cargo, npm, pip, ...) first on PATH
// so executing legs never run real installers. The caller runs Teardown
// when done.
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
	h := &Harness{RepoRoot: repoRoot, BinDir: tmp}
	if h.shim, err = buildShim(repoRoot, tmp); err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}
	if err := h.writeStubs(); err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}
	return h, nil
}

// buildShim compiles the Windows stub launcher: Windows resolves only
// PATHEXT names, and a .cmd shim would re-parse stub arguments through
// cmd.exe (quotes, pipes), so each stub gets a real executable that runs
// the extensionless bash script beside it with the argv untouched. Off
// Windows no launcher is needed.
func buildShim(repoRoot, dir string) (string, error) {
	if runtime.GOOS != osWindows {
		return "", nil
	}
	shim := filepath.Join(dir, "stubshim.exe")
	build := exec.CommandContext(context.Background(), "go", "build", "-o", shim, "./internal/parity/stubshim")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return "", fmt.Errorf("build stub shim: %v\n%s", err, out)
	}
	return shim, nil
}

// buildBinary compiles the CLI under test into dir.
func buildBinary(repoRoot, dir string) error {
	bin := filepath.Join(dir, "verdict")
	if runtime.GOOS == osWindows {
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

// toolStubNames are toolchain and network-client binaries replaced by
// deterministic stubs first on the harness PATH. With HOME sandboxed to
// the case dir, the real cargo/rustup proxy (and pip, npm, ...) would
// otherwise materialize toolchains into the snapshot — network-dependent
// and racy across the parallel legs. curl/wget hit only the discard port
// and docker is absent on runners, but stubbing them keeps every executing
// leg hermetic by construction instead of by runner luck.
var toolStubNames = []string{
	"bun", "bunx", "cargo", "docker", "go", "npm", "npx",
	"parallel", "pip", "pip3", "pnpm", "rustc", "rustup", "yarn",
}

// toolStubScript records the invocation on stdout and exits 0: the gate
// decision (execute) and the recorded proof stay graded byte-identical on
// both sides without running real toolchains. All current probes pass
// these tools static argv, so the record is deterministic by construction.
const toolStubScript = "#!/usr/bin/env bash\necho \"stub-${0##*/} $*\"\nexit 0\n"

// fetchStubNames are the loopback fetch clients with their own stub shape:
// one probe embeds $(date +%s) in the URL, so echoing argv would leak the
// wall clock into the proof (the real clients never echo it: curl prints a
// fixed connection-failed diagnostic, wget -q a one-line failure). The
// stubs mimic that connection-refused record deterministically, with the
// real exit codes (7/4).
var fetchStubNames = []string{"curl", "wget"}

// fetchStubScripts maps each fetch client to its connection-refused record:
// fixed text (no argv echo), real exit code.
var fetchStubScripts = map[string]string{
	"curl": "#!/usr/bin/env bash\necho \"curl: (7) Failed to connect to 127.0.0.1 port 9 after 0 ms: Connection refused\" >&2\nexit 7\n",
	"wget": "#!/usr/bin/env bash\necho \"Connecting to 127.0.0.1:9... failed: Connection refused.\" >&2\nexit 4\n",
}

// writeStubs creates the stub directory holding the network-dead gh stub
// plus the deterministic toolchain stubs, all through [Harness.WriteStub] so
// every OS resolves the same stubs first on PATH.
func (h *Harness) writeStubs() error {
	stubBin, err := os.MkdirTemp("", "verdict-parity-stub")
	if err != nil {
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return fmt.Errorf("stub temp dir: %v", err)
	}
	h.StubBin = stubBin
	stubs := append([]stubDef{{name: "gh", script: stubScript}}, allStubs()...)
	for _, s := range stubs {
		if err := h.WriteStub(s.name, s.script); err != nil {
			os.RemoveAll(stubBin)
			h.StubBin = ""
			return err
		}
	}
	return nil
}

// stubDef pairs a stubbed binary name with its script.
type stubDef struct {
	name   string
	script string
}

// allStubs lists every harness stub: the argv-recording toolchain stubs
// plus the connection-refused fetch-client records.
func allStubs() []stubDef {
	var out []stubDef
	for _, name := range toolStubNames {
		out = append(out, stubDef{name: name, script: toolStubScript})
	}
	for _, name := range fetchStubNames {
		out = append(out, stubDef{name: name, script: fetchStubScripts[name]})
	}
	return out
}

// WriteStub plants one executable stub script in the stub directory.
// Windows [exec.LookPath] resolves only PATHEXT names, so Windows also gets a
// stubshim copy named name.exe beside the extensionless script: the binary
// under test then finds the stub (never the runner's real tool) and the shim
// hands the script to bash.
func (h *Harness) WriteStub(name, script string) error {
	if err := os.WriteFile(filepath.Join(h.StubBin, name), []byte(script), stubPerm); err != nil {
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return fmt.Errorf("write %s stub: %v", name, err)
	}
	if h.shim == "" {
		return nil
	}
	if err := copyFile(h.shim, filepath.Join(h.StubBin, name+".exe")); err != nil {
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return fmt.Errorf("write %s exe stub: %v", name, err)
	}
	return nil
}

// Teardown removes the harness temp roots.
func (h *Harness) Teardown() {
	os.RemoveAll(h.BinDir)
	os.RemoveAll(h.StubBin)
	h.BinDir, h.StubBin = "", ""
}

// findRepoRoot walks up from this source file to the checkout carrying
// package.json.
func findRepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		//nolint:wrapcheck // fresh error created here; nothing to wrap
		return "", errors.New("cannot locate test source")
	}
	dir := filepath.Dir(file)
	for range maxRootDepth {
		if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
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
var curlMsRe = regexp.MustCompile(`(Failed to connect to \S+ port \d+ after )\d+( ms)`)

// versionScrub replaces the manifest version with a placeholder so goldens
// stay version-independent (a release bump must not invalidate snapshots;
// the version-source eval pins the real rendering through the binary).
var versionScrub = sync.OnceValue(func() func(string) string {
	version := ""
	if raw, err := os.ReadFile(filepath.Join(repoRoot(), "package.json")); err == nil {
		var doc struct {
			Version string `json:"version"`
		}
		if jerr := json.Unmarshal(raw, &doc); jerr == nil {
			version = doc.Version
		}
	}
	return func(s string) string {
		if version == "" {
			return s
		}
		return strings.ReplaceAll(s, version, "VERSION")
	}
})

// repoRoot walks up from this source file to the checkout carrying
// package.json.
func repoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	dir := filepath.Dir(file)
	for range maxRootDepth {
		if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
	return "."
}

// scrub normalizes timestamps, prompt nonces, the manifest version, and
// (R6: the sanctioned scrub, extended only for tool-runtime metadata that
// differs by construction). Everything else compares byte-exact.
func scrub(s string) string {
	s = tsRe.ReplaceAllString(s, "0000-00-00T00:00:00Z")
	s = scrubNonce(s)
	s = versionScrub()(s)
	return curlMsRe.ReplaceAllString(s, "${1}0${2}")
}

// nonceRe matches the trust-boundary nonce line; the same nonce fences
// every untrusted region of a prompt dump.
var nonceRe = regexp.MustCompile(`with NONCE replaced by this run's nonce: ([0-9a-f]{16})\.`)

// scrubNonce replaces a run's prompt nonce with a placeholder. Dumps carry
// no other random material; SHAs are 40 hex chars and never match the
// 16-hex boundary pattern.
func scrubNonce(s string) string {
	m := nonceRe.FindStringSubmatch(s)
	if len(m) != 2 {
		return s
	}
	return strings.ReplaceAll(s, m[1], "NONCE")
}

// scrubRoots normalizes sandbox roots and homes to placeholders so golden
// files stay machine-independent. Longer (home) paths sort first so nested
// prefixes cannot shadow.
func scrubRoots(s string, roots []string) string {
	ordered := append([]string{}, roots...)
	slices.SortFunc(ordered, func(a, b string) int { return len(b) - len(a) })
	for _, r := range ordered {
		if r == "" {
			continue
		}
		s = strings.ReplaceAll(s, r, "ROOT")
	}
	return s
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
//
// GH_ and GITHUB_ keep the runner's gh auth (GH_TOKEN, GITHUB_TOKEN,
// GH_HOST, ...) out of the "hermetic" runs: a case that needs one sets it
// through extraEnv, which overlays after this filter.
var envDropPrefixes = []string{
	"REVIEW_", "GH_", "GITHUB_", "XDG_CONFIG_HOME=", "NO_COLOR=", "HOME=", "TMPDIR=",
}

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

// Run runs the Go binary in dir, capturing exit code, streams, and the
// written tree. extraEnv overlays additional variables.
func (h *Harness) Run(t *testing.T, dir, home string, extraEnv map[string]string, args ...string) Outputs {
	t.Helper()
	bin := filepath.Join(h.BinDir, "verdict")
	if runtime.GOOS == osWindows {
		bin += ".exe"
	}
	cmd := exec.CommandContext(t.Context(), bin, args...)
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
	out[tag+"/"+filepath.ToSlash(rel)] = string(raw)
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
