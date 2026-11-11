//go:build unix

package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/record"
)

// These tests spawn a real fake agent through the production watchdog. No real
// agent CLI and no model is ever invoked: the fake is this test binary, symlinked
// into a PATH directory under an agent's name and switched into fake mode by an
// environment variable, so the child observes the exact argv (argv[0] included)
// the launcher built. A shell script cannot do that: the kernel replaces a
// script's argv[0] with the interpreter's script path.

// fakeModeEnv switches the test binary into the fake agent's role.
const fakeModeEnv = "VERDICT_TEST_FAKE_AGENT"

// TestMain runs the fake agent instead of the suite when the launcher
// spawned this binary in fake mode.
func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeModeEnv); mode != "" {
		os.Exit(fakeAgent(mode))
	}
	os.Exit(m.Run())
}

// fakeAgent is the child's side of the contract: it records its own argv
// NUL-separated (so the test can compare --print with the real invocation) and
// then behaves as the case asked.
func fakeAgent(mode string) int {
	if code := recordArgv(); code != 0 {
		return code
	}
	switch mode {
	case "write":
		return copyReview()
	case "exit":
		fmt.Fprintln(os.Stderr, "fake agent: the model refused the request")
		code, err := strconv.Atoi(os.Getenv("FAKE_EXIT"))
		if err != nil {
			fmt.Fprintln(os.Stderr, "fake agent: bad FAKE_EXIT:", err)
			return 93
		}
		return code
	case "sleep":
		time.Sleep(30 * time.Second)
		return 0
	default: // "silent": exit 0 and write nothing
		return 0
	}
}

// argvBytes renders argv NUL-separated so an argument holding a newline or a
// space survives the round trip.
func argvBytes(argv []string) []byte {
	return []byte(strings.Join(argv, "\x00") + "\x00")
}

// fixture is one run fixture: a local bundle and the PATH directory the fake
// agent lives in.
type fixture struct {
	root string // the reviewed repository root
	dir  string // the bundle directory
	bin  string // the PATH directory holding the fake agent
	slug string
}

// bundleSlug is the local bundle name the fixtures use, so the run and record
// handoff exercise the local-bundle branch.
const bundleSlug = "local-work"

// validReview is a review the shared record path accepts unchanged: a verdict
// marker, a section, and no comments to anchor.
const validReview = `{"schema_version":"1","body":"> ✅ no findings\n\n### Nothing to flag\n\n` +
	`Reviewed the diff and found nothing worth flagging.","approved":false,"comments":[]}`

// fixtureDiff is the bundle's diff: one small change, so the review has a
// patch to attest against.
const fixtureDiff = "diff --git a/notes.txt b/notes.txt\nindex 1111111..2222222 100644\n" +
	"--- a/notes.txt\n+++ b/notes.txt\n@@ -1,3 +1,3 @@\n top\n-old\n+new\n bottom\n"

// newFixture builds a local bundle with a diff and its metadata, plus an empty
// PATH directory for the fake agent and an isolated HOME.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".verdict", "state", bundleSlug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "source.json"),
		`{"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","base_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head_ref":"work"}`)
	writeFile(t, filepath.Join(dir, "full.diff"), fixtureDiff)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return &fixture{root: root, dir: dir, bin: t.TempDir(), slug: bundleSlug}
}

// plant symlinks this test binary into the fixture's PATH directory as the
// agent binary and puts that directory first on PATH, so the run resolves
// the fake and never a real agent CLI. It returns the file the child records
// its argv into.
func (f *fixture) plant(t *testing.T, mode string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve the test binary: %v", err)
	}
	if err := os.Symlink(exe, filepath.Join(f.bin, "claude")); err != nil {
		t.Fatalf("link the fake agent: %v", err)
	}
	argvFile := filepath.Join(t.TempDir(), "argv")
	t.Setenv("FAKE_ARGV", argvFile)
	t.Setenv("FAKE_ARTIFACT", filepath.Join(f.dir, "review.json"))
	t.Setenv(fakeModeEnv, mode)
	t.Setenv("PATH", f.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argvFile
}

// plantUnrunnable writes an executable file that cannot be executed at all,
// so the spawn (not the resolution) is what fails.
func (f *fixture) plantUnrunnable(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.bin, "claude"), []byte("#!/nonexistent/interpreter\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", f.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// pathless points PATH at an empty directory: nothing resolves, which is the
// "not installed" case.
func (f *fixture) pathless(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

// recordArgv writes this child's argv NUL-separated, so a test can compare
// --print with the real invocation. It returns a non-zero exit code on failure.
func recordArgv() int {
	path := os.Getenv("FAKE_ARGV")
	if path == "" {
		return 0
	}
	if err := os.WriteFile(path, argvBytes(os.Args), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "fake agent: write argv:", err)
		return 90
	}
	return 0
}

// copyReview is the "write" mode: the fake agent copies the planted review into
// the bundle path the launcher watches.
func copyReview() int {
	body, err := os.ReadFile(os.Getenv("FAKE_REVIEW_SRC"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake agent: read review:", err)
		return 91
	}
	if err := os.WriteFile(os.Getenv("FAKE_ARTIFACT"), body, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "fake agent: write review:", err)
		return 92
	}
	return 0
}

// writeReviewSrc plants the review the fake agent copies into the bundle.
func (f *fixture) writeReviewSrc(t *testing.T) {
	t.Helper()
	path := filepath.Join(f.dir, "..", "review-src.json")
	writeFile(t, path, validReview)
	t.Setenv("FAKE_REVIEW_SRC", path)
}

// writeFile plants one fixture file with the default mode.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runVerb runs the real adapter against the fixture. The global --json
// toggle is the adapter's caller's job (main strips it before dispatch), so
// it is peeled here and applied to the runner instead.
func runVerb(t *testing.T, f *fixture, args ...string) (int, string, string) {
	t.Helper()
	jsonMode := false
	local := make([]string, 0, len(args))
	for i, a := range args {
		if a == "--" {
			local = append(local, args[i:]...)
			break
		}
		if a == "--json" {
			jsonMode = true
			continue
		}
		local = append(local, a)
	}
	r, out, diag := testRunner()
	r.JSONMode = jsonMode
	if jsonMode {
		r.Human = diag
	}
	code := Run(context.Background(), r, local, Request{
		Env:  map[string]string{"HOME": os.Getenv("HOME"), "XDG_CONFIG_HOME": os.Getenv("XDG_CONFIG_HOME")},
		Repo: "acme/app", Pr: f.slug, Root: f.root, RepoRoot: f.root,
	})
	return code, out.String(), diag.String()
}

// readArgv reads the NUL-separated argv the fake agent recorded.
func readArgv(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the child's argv: %v", err)
	}
	trimmed := strings.TrimSuffix(string(raw), "\x00")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\x00")
}

// launcherRecord reads launcher.json back as a map.
func launcherRecord(t *testing.T, dir string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "launcher.json"))
	if err != nil {
		t.Fatalf("read launcher.json: %v", err)
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		t.Fatalf("decode launcher.json: %v", err)
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		t.Fatalf("launcher.json is not an object: %s", raw)
	}
	return m
}

// asInt renders one decoded JSON scalar as an int, so the assertions do not
// depend on how the decoder represents numbers.
func asInt(t *testing.T, v any) int {
	t.Helper()
	text := fmt.Sprint(v)
	n, err := strconv.Atoi(text)
	if err != nil {
		t.Fatalf("value %v is not an integer", v)
	}
	return n
}

// TestRunLaunchesAndHandsOff is the acceptance test: the fake agent runs, its
// artifact is asserted fresh, the provenance record names the launcher, and the
// existing record path accepts the review unchanged.
func TestRunLaunchesAndHandsOff(t *testing.T) {
	f := newFixture(t)
	f.plant(t, "write")
	f.writeReviewSrc(t)

	code, out, errOut := runVerb(t, f, "--agent", "claude")
	if code != cli.ExitOK {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	for _, want := range []string{
		"» run: claude -p <prompt>", "» wrote .verdict/state/local-work/review.json",
		"» recorded .verdict/state/local-work/launcher.json",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout does not carry %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(f.dir, ".run.out")); err != nil {
		t.Errorf("the child's output log is missing: %v", err)
	}
	rec := launcherRecord(t, f.dir)
	assertHandoffRecord(t, rec, f.bin)
	assertTimestamps(t, rec)

	// The existing record path accepts the produced review unchanged. A fetched
	// bundle carries the controls snapshot and typed capture this seeds; record
	// gates on them, and the launcher writes neither.
	seedControls(t, f)
	rec2, out2, diag2 := testRunner()
	rec2.Cmd = "record"
	rec2.Effective = cli.Defaults()
	code = record.Run(context.Background(), rec2, nil, &record.Request{
		Env:  map[string]string{"HOME": os.Getenv("HOME"), "XDG_CONFIG_HOME": os.Getenv("XDG_CONFIG_HOME")},
		Repo: "acme/app", Pr: f.slug, Root: f.root, RepoRoot: f.root,
		Forces: &cli.GlobalForces{},
	})
	if code != cli.ExitOK {
		t.Fatalf("record on the produced review exited %d:\n%s\n%s", code, out2.String(), diag2.String())
	}
	if _, err := os.Stat(filepath.Join(f.dir, "findings.json")); err != nil {
		t.Errorf("record wrote no findings.json: %v", err)
	}
}

// seedControls snapshots the fixture's controls and typed capture the way
// fetch does (the head opt-out stands in for the base-commit read), so the
// record handoff reaches the review validation instead of refusing a bundle
// no fetch built.
func seedControls(t *testing.T, f *fixture) {
	t.Helper()
	base := recordcore.BundleBase(f.dir, nil)
	if base == "" {
		t.Fatal("fixture bundle states no base SHA")
	}
	snap, ref := controls.Resolve(t.Context(), nil, &controls.Request{
		BundleDir: f.dir, BaseSHA: base, AllowHead: true, Root: f.root,
	})
	if ref != nil {
		t.Fatalf("seed controls: %v", ref)
	}
	inputs := &config.Inputs{BaseSHA: base}
	resolved, ref := config.Resolve(inputs)
	if ref != nil {
		t.Fatalf("resolve fixture config: %v", ref)
	}
	const capturedAt = "2026-11-02T12:00:00Z"
	evidence := config.Capture(inputs, resolved, nil, capturedAt)
	if err := config.WriteRunState(f.dir, &config.RunState{
		BaseSHA:      base,
		CapturedAt:   capturedAt,
		ConfigSHA256: evidence.ConfigSHA256,
		Evidence:     evidence,
		Resolved:     resolved,
		Root:         config.RootSet(inputs.Repo, base),
		Inventory:    config.InventoryHashes(snap),
	}); err != nil {
		t.Fatalf("seed run state: %v", err)
	}
}

// TestRunPrintMatchesTheRealInvocation proves --print renders byte-for-byte the
// argv the launcher really spawns, pass-through arguments included.
func TestRunPrintMatchesTheRealInvocation(t *testing.T) {
	f := newFixture(t)
	argvFile := f.plant(t, "write")
	f.writeReviewSrc(t)
	args := []string{"--agent", "claude", "--", "--model", "opus"}

	printed := assertPrintTouchesNothing(t, f, args)
	code, _, errOut := runVerb(t, f, args...)
	if code != cli.ExitOK {
		t.Fatalf("run exit = %d: %s", code, errOut)
	}
	argv := readArgv(t, argvFile)
	assertPrintedMatchesSpawned(t, printed, argv)
	assertArgvPrefix(t, argv, []string{"claude", "-p", "--model", "opus"})
}

// TestRunRefusalsWithFakeBinaries covers the six failure modes with a real
// fake binary: each refuses typed (asserted through the --json envelope) and
// carries a hint. The agent's own non-zero exit is also the process exit
// code.
func TestRunRefusalsWithFakeBinaries(t *testing.T) {
	cases := []refusalCase{
		{
			name:    "binary not on PATH",
			before:  func(t *testing.T, f *fixture) { t.Helper(); f.pathless(t) },
			want:    "agent-missing",
			wantMsg: "is not installed",
			wantOut: 1,
		},
		{
			name:    "spawn failure",
			before:  func(t *testing.T, f *fixture) { t.Helper(); f.plantUnrunnable(t) },
			want:    "agent-spawn-failed",
			wantMsg: "could not start agent",
			wantOut: 1,
		},
		{
			name:    "watchdog timeout",
			mode:    "sleep",
			before:  func(*testing.T, *fixture) {},
			args:    []string{"--timeout", "1"},
			want:    "agent-timeout",
			wantMsg: "watchdog",
			wantOut: 1,
		},
		{
			name:    "agent non-zero exit",
			mode:    "exit",
			before:  func(t *testing.T, _ *fixture) { t.Helper(); t.Setenv("FAKE_EXIT", "7") },
			want:    "agent-failed",
			wantMsg: "exited 7",
			wantOut: 7,
		},
		{
			name:    "no artifact written",
			mode:    "silent",
			before:  func(*testing.T, *fixture) {},
			want:    "no-review-artifact",
			wantMsg: "wrote no",
			wantOut: 1,
		},
		{
			name:    "artifact older than the run",
			mode:    "silent",
			want:    "stale-review-artifact",
			wantMsg: "predates this run",
			before: func(t *testing.T, f *fixture) {
				t.Helper()
				stale := filepath.Join(f.dir, "review.json")
				writeFile(t, stale, validReview)
				old := time.Now().Add(-time.Hour)
				if err := os.Chtimes(stale, old, old); err != nil {
					t.Fatal(err)
				}
			},
			wantOut: 1,
		},
	}
	for i := range cases {
		tc := &cases[i]
		t.Run(tc.name, func(t *testing.T) {
			assertFakeBinaryRefusal(t, tc)
		})
	}
}

// TestRunUnknownAgentListsKnownNames proves an unknown name refuses typed
// and names the table, which is the only place an operator learns it can
// grow.
func TestRunUnknownAgentListsKnownNames(t *testing.T) {
	f := newFixture(t)
	code, out, _ := runVerb(t, f, "--agent", "gpt", "--json")
	if code != cli.ExitRefused {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(out, `"code": "unknown-agent"`) {
		t.Fatalf("envelope does not refuse unknown-agent:\n%s", out)
	}
	for _, name := range []string{"claude", "codex", "cursor", "opencode"} {
		if !strings.Contains(out, name) {
			t.Errorf("the refusal does not list %q:\n%s", name, out)
		}
	}
}

// TestRunJSONSuccessEnvelope proves the machine contract: one envelope, the
// verb and repo identity, and the provenance in data.
func TestRunJSONSuccessEnvelope(t *testing.T) {
	f := newFixture(t)
	f.plant(t, "write")
	f.writeReviewSrc(t)
	code, out, errOut := runVerb(t, f, "--agent", "claude", "--json")
	if code != cli.ExitOK {
		t.Fatalf("exit = %d: %s", code, errOut)
	}
	assertRunSuccessEnvelope(t, out)
}

// refusalCase is one fake-binary failure mode: the fixture mutation, the agent
// mode, the expected exit, and the typed code with its message fragment.
type refusalCase struct {
	before  func(t *testing.T, f *fixture)
	name    string
	mode    string
	want    string
	wantMsg string
	args    []string
	wantOut int
}

// assertHandoffRecord pins the provenance a successful handoff leaves: a zero
// exit, a fresh artifact, the resolved agent identity, and the directive
// reduced to its placeholder.
func assertHandoffRecord(t *testing.T, rec map[string]any, bin string) {
	t.Helper()
	if asInt(t, rec["exit"]) != 0 || rec["artifact_fresh"] != true {
		t.Errorf("launcher.json exit/fresh = %v/%v", rec["exit"], rec["artifact_fresh"])
	}
	if rec["agent"] != "claude" || rec["binary"] != filepath.Join(bin, "claude") {
		t.Errorf("launcher.json agent/binary = %v/%v", rec["agent"], rec["binary"])
	}
	shape, isShape := rec["argv"].([]any)
	if !isShape || len(shape) == 0 || shape[len(shape)-1] != "<prompt>" {
		t.Errorf("launcher.json argv = %v, want the directive placeholder last", shape)
	}
}

// assertFakeBinaryRefusal drives one failure mode against a real fake binary:
// the exit code, then the envelope it answers with.
func assertFakeBinaryRefusal(t *testing.T, tc *refusalCase) {
	t.Helper()
	f := newFixture(t)
	if tc.before != nil {
		tc.before(t, f)
	}
	if tc.mode != "" {
		f.plant(t, tc.mode)
	}
	args := append([]string{"--agent", "claude", "--json"}, tc.args...)
	code, out, errOut := runVerb(t, f, args...)
	if code != tc.wantOut {
		t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", code, tc.wantOut, out, errOut)
	}
	assertRefusalEnvelope(t, out, tc.want, tc.wantMsg)
}

// assertRefusalEnvelope pins one refused envelope: the typed code, a message
// naming the cause with a hint, and no data.
func assertRefusalEnvelope(t *testing.T, out, wantCode, wantMsg string) {
	t.Helper()
	doc, err := jq.Decode([]byte(out))
	if err != nil {
		t.Fatalf("stdout is not one envelope: %v\n%s", err, out)
	}
	env, _ := jq.AsMap(doc)
	errObj, _ := jq.AsMap(env["error"])
	if errObj["code"] != wantCode {
		t.Fatalf("error.code = %v, want %q (message: %v)", errObj["code"], wantCode, errObj["message"])
	}
	msg := jq.StrOf(errObj["message"])
	if !strings.Contains(msg, wantMsg) {
		t.Errorf("message = %q, want it to name %q", msg, wantMsg)
	}
	if !strings.Contains(msg, "try: ") {
		t.Errorf("message carries no hint: %q", msg)
	}
	if env["data"] != nil {
		t.Errorf("refusal envelope data = %v, want null", env["data"])
	}
}

// assertTimestamps proves the record carries both ends of the run.
func assertTimestamps(t *testing.T, rec map[string]any) {
	t.Helper()
	for _, key := range []string{"started_at", "ended_at"} {
		if rec[key] == nil || rec[key] == "" {
			t.Errorf("launcher.json has no %s", key)
		}
	}
}

// assertPrintTouchesNothing runs --print and returns its output, proving the
// print-only path writes no provenance record.
func assertPrintTouchesNothing(t *testing.T, f *fixture, args []string) string {
	t.Helper()
	code, printed, _ := runVerb(t, f, append([]string{"--print"}, args...)...)
	if code != cli.ExitOK {
		t.Fatalf("--print exit = %d", code)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "launcher.json")); err == nil {
		t.Fatal("--print wrote launcher.json; it must touch nothing")
	}
	return printed
}

// assertPrintedMatchesSpawned proves --print is the real invocation, not a
// description of one: the recorded argv renders to the same line.
func assertPrintedMatchesSpawned(t *testing.T, printed string, argv []string) {
	t.Helper()
	if got := RenderCommand(argv) + "\n"; got != printed {
		t.Fatalf("--print differs from the real invocation:\nprinted: %q\nspawned: %q", printed, got)
	}
}

// assertRunSuccessEnvelope pins the machine contract: one envelope, the verb
// and repo identity, no local PR number, the provenance in data, the handoff.
func assertRunSuccessEnvelope(t *testing.T, out string) {
	t.Helper()
	doc, err := jq.Decode([]byte(out))
	if err != nil {
		t.Fatalf("stdout is not one envelope: %v\n%s", err, out)
	}
	env, _ := jq.AsMap(doc)
	if env["verb"] != "run" || env["repo"] != "acme/app" || env["pr"] != nil {
		t.Errorf("envelope identity = %v/%v/%v", env["verb"], env["repo"], env["pr"])
	}
	if env["error"] != nil {
		t.Errorf("success envelope carries error %v", env["error"])
	}
	data, _ := jq.AsMap(env["data"])
	if data["agent"] != "claude" || data["artifact_fresh"] != true {
		t.Errorf("data = %v", data)
	}
	if !strings.HasPrefix(jq.StrOf(env["next"]), "record the review with verdict record acme/app") {
		t.Errorf("next = %v, want the local-bundle handoff", env["next"])
	}
}

// assertArgvPrefix proves the spawned argv starts with want and carries exactly
// one more element: the directive.
func assertArgvPrefix(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want)+1 {
		t.Fatalf("argv = %q, want %q plus the directive", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
