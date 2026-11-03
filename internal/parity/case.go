package parity

// One harness contract for every golden suite: a case is a sandboxed run of
// the built binary plus the assertion that its scrubbed output and every
// file it wrote match a checked-in golden. The suites differ only in their
// fixtures and in two opt-ins (a sandbox TMPDIR for the record store's
// atomic writes, and the read-only proof for the read verbs), so the runner
// lives here instead of being forked per suite.

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"testing"
)

// Case is one golden leg: fixtures, environment, argv, and the assertions
// beyond the byte comparison. Fields are declared in the order that keeps the
// GC scan shortest (govet fieldalignment), not in reading order.
type Case struct {
	// Env overlays the hermetic child environment.
	Env map[string]string
	// Setup writes the case fixtures into dir before the run. home is the
	// isolated HOME; most setups ignore it.
	Setup func(t *testing.T, dir, home string)
	// AllowWrites names the tagged keys ("C/rel", "H/rel") a read leg may
	// change — the artifacts the operator explicitly requested, which the
	// golden pins byte-exact instead.
	AllowWrites map[string]bool
	// Omit drops tagged keys the suite itself seeded and does not grade
	// (a machine-dependent snapshot, never product output).
	Omit func(key string) bool
	// Name is the golden basename under testdata/ (slashes nest the golden
	// directory, exactly as they nest the subtest).
	Name string
	// Args is the direct CLI argv.
	Args []string
	// TmpInSandbox points TMPDIR at the case sandbox, so a verb's
	// atomic-write temp files are created and removed inside the pinned
	// tree. The default leaves TMPDIR unset: the host temp dir holds
	// nothing a golden grades.
	TmpInSandbox bool
	// ReadOnly proves the run wrote nothing: every file in the sandbox or
	// home afterwards must exist unchanged beforehand, deletions included.
	ReadOnly bool
}

// Run executes one case, pins it against testdata/<Name>.golden, and returns
// the outputs for extra assertions.
func (c *Case) Run(t *testing.T, h *Harness) Outputs {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, dirPerm); err != nil {
		t.Fatal(err)
	}
	env := c.envAt(dir)
	if c.Setup != nil {
		c.Setup(t, dir, home)
		// Setup may add environment (a URL rewrite for a local git remote,
		// say), so the overlay is read only after the fixtures exist.
		env = c.envAt(dir)
	}
	var before map[string]string
	if c.ReadOnly {
		before = mergeMaps(snapshotTree(t, "C", dir), snapshotTree(t, "H", home))
	}
	out := h.Run(t, dir, home, env, c.Args...)
	Check(t, c.Name, Render(Outputs{
		Status: out.Status, Stdout: out.Stdout, Stderr: out.Stderr,
		Files: c.pinned(out.Files),
	}, []string{dir, home}))
	if c.ReadOnly {
		assertNoWrites(t, before, out.Files, c.AllowWrites)
	}
	return out
}

// envAt returns the child environment overlay for one sandbox: the case's
// own variables plus, when opted in, a TMPDIR inside the sandbox.
func (c *Case) envAt(dir string) map[string]string {
	env := maps.Clone(c.Env)
	if !c.TmpInSandbox {
		return env
	}
	if env == nil {
		env = map[string]string{}
	}
	if _, ok := env["TMPDIR"]; !ok {
		env["TMPDIR"] = dir
	}
	return env
}

// pinned filters the graded file map down to the keys the suite pins.
func (c *Case) pinned(files map[string]string) map[string]string {
	if c.Omit == nil {
		return files
	}
	kept := make(map[string]string, len(files))
	for k, v := range files {
		if !c.Omit(k) {
			kept[k] = v
		}
	}
	return kept
}

// Golden runs one case in a fresh sandbox and home and pins it. setup writes
// identical fixtures per run; extraEnv overlays variables; args are the
// direct CLI argv.
func Golden(
	t *testing.T, h *Harness, name string,
	setup func(t *testing.T, dir, home string),
	extraEnv map[string]string, args ...string,
) Outputs {
	t.Helper()
	return (&Case{Name: name, Setup: setup, Env: extraEnv, Args: args}).Run(t, h)
}

// GoldenRead is Golden plus the read-only proof: the verb may write only the
// artifacts in allowed, and nothing may be deleted. It runs as a parallel
// subtest, since independent read legs share no state.
func GoldenRead(
	t *testing.T, h *Harness, name string,
	setup func(t *testing.T, dir, home string),
	env map[string]string, allowed map[string]bool, args ...string,
) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		t.Helper()
		t.Parallel()
		c := &Case{
			Name: name, Setup: setup, Env: env, Args: args,
			ReadOnly: true, AllowWrites: allowed,
		}
		c.Run(t, h)
	})
}

// RunMain builds the harness once, stores it in dst (the suite's package-level
// harness variable), runs the case hooks in order, runs m, and returns the
// exit code. A suite's TestMain is one line, including the suites that adjust
// the shared stubs before any leg runs:
//
//	func TestMain(m *testing.M) { os.Exit(parity.RunMain(m, &harness, installStubs)) }
func RunMain(m *testing.M, dst **Harness, hooks ...func(*Harness) error) int {
	h, err := Build()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	defer h.Teardown()
	for _, hook := range hooks {
		if err := hook(h); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
	}
	*dst = h
	return m.Run()
}

// errorer is the slice of [testing.T] the read-only proof needs, so the
// helper's failing behavior is unit-testable with a recorder.
type errorer interface {
	Helper()
	Errorf(format string, args ...any)
}

// assertNoWrites proves the read-only contract: every file present after the
// run matches the pre-run snapshot, except the explicitly requested
// artifacts (which the golden pins byte-exact instead). Deletions fail too —
// readers remove nothing.
func assertNoWrites(t errorer, before, after map[string]string, allowed map[string]bool) {
	t.Helper()
	for k, va := range after {
		if allowed[k] {
			continue
		}
		checkKeptFile(t, k, va, before)
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			t.Errorf("go deleted file: %s", k)
		}
	}
}

// checkKeptFile proves one pre-existing file survived the run unchanged.
func checkKeptFile(t errorer, k, va string, before map[string]string) {
	t.Helper()
	vb, ok := before[k]
	if !ok {
		t.Errorf("go wrote unexpected file: %s", k)
		return
	}
	if Scrub(va) != Scrub(vb) {
		t.Errorf("go changed file: %s", k)
	}
}
