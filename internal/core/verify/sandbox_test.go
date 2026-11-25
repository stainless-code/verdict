package verifycore_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stainless-code/verdict/internal/core"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
	"github.com/stainless-code/verdict/internal/testkit"
)

// wantProbeScript is the macOS probe body, a literal expectation: the probe
// must exercise bash, mktemp, the private TMPDIR, and a read of the working
// directory before the real command runs.
const wantProbeScript = `p=$(mktemp "$TMPDIR/verdict-probe.XXXXXX") && printf ok > "$p" && test -s "$p" && rm -f "$p" && test -r .`

// sandboxCommander scripts one result per spawn in call order, so the probe
// and the wrapped command stay distinguishable.
type sandboxCommander struct {
	calls   []*core.Command
	results []core.Result
	errs    []error
}

func (c *sandboxCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	if isHeadProbe(cmd) {
		return core.Result{Stdout: patchHead + "\n"}, nil
	}
	if isStatusProbe(cmd) {
		return core.Result{}, nil
	}
	c.calls = append(c.calls, cmd)
	i := len(c.calls) - 1
	if i < len(c.errs) && c.errs[i] != nil {
		return core.Result{}, c.errs[i]
	}
	if i < len(c.results) {
		return c.results[i], nil
	}
	return core.Result{}, nil
}

// sandboxHomeFixture builds credential paths of both kinds the wrapper hides
// and returns their home.
func sandboxHomeFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, rel := range []string{
		".aws", ".ssh", filepath.Join(".config", "gh"), ".docker", ".kube",
		filepath.Join(".config", "gcloud"), ".azure", ".gnupg", ".terraform.d", ".cargo",
	} {
		if err := os.MkdirAll(filepath.Join(home, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []string{
		".netrc", ".git-credentials", ".npmrc", ".pypirc", filepath.Join(".cargo", "credentials.toml"), ".vault-token",
	} {
		if err := os.WriteFile(filepath.Join(home, rel), []byte("sandbox-sentinel\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func sandboxRequest(env []string, cmdr core.Commander) *verifycore.Request {
	read := core.ReadRequest{
		Runtime: &core.Runtime{Now: testkit.FixedNow, Progress: &testkit.Collector{}, Commander: cmdr},
		Repo:    "acme/app", Pr: "5201",
	}
	// The account home is pinned to the fake HOME so the credential mounts
	// stay a literal expectation on every host.
	req := &verifycore.Request{
		ReadRequest: read,
		TimeoutRaw:  "10",
		Sandbox:     true,
		Environ:     env,
		JSONMode:    true,
	}
	verifycore.SetAccountHomeForTest(req, envValue(env, "HOME"))
	return req
}

// wantVarRunMount mirrors the host fact the wrapper reads, so a supported
// platform that symlinks /var/run keeps the literal expectation.
func wantVarRunMount() []string {
	if info, err := os.Lstat("/var/run"); err == nil && info.IsDir() {
		return []string{"--tmpfs", "/var/run"}
	}
	return nil
}

func hostSandboxTool() string {
	switch runtime.GOOS {
	case "linux":
		return "bwrap"
	case "darwin":
		return "sandbox-exec"
	}
	return ""
}

func requireSandboxHost(t *testing.T) {
	t.Helper()
	if hostSandboxTool() == "" {
		t.Skip("no enforced sandbox mechanism on this host")
	}
}

// wantProbeArgv is the exact Linux bwrap probe; Darwin builds its profile from
// the host paths and is checked structurally instead.
func wantProbeArgv(root, home string) []string {
	argv := []string{
		"bwrap",
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--unshare-pid",
		"--tmpfs", "/tmp",
		"--tmpfs", "/run",
	}
	argv = append(argv, wantVarRunMount()...)
	return append(argv,
		"--bind", root, root,
		"--ro-bind", filepath.Join(root, ".git"), filepath.Join(root, ".git"),
		"--tmpfs", filepath.Join(home, ".aws"),
		"--tmpfs", filepath.Join(home, ".ssh"),
		"--tmpfs", filepath.Join(home, ".config", "gh"),
		"--tmpfs", filepath.Join(home, ".docker"),
		"--tmpfs", filepath.Join(home, ".kube"),
		"--tmpfs", filepath.Join(home, ".config", "gcloud"),
		"--tmpfs", filepath.Join(home, ".azure"),
		"--tmpfs", filepath.Join(home, ".gnupg"),
		"--ro-bind", "/dev/null", filepath.Join(home, ".netrc"),
		"--ro-bind", "/dev/null", filepath.Join(home, ".git-credentials"),
		"--ro-bind", "/dev/null", filepath.Join(home, ".npmrc"),
		"--ro-bind", "/dev/null", filepath.Join(home, ".pypirc"),
		"--tmpfs", filepath.Join(home, ".terraform.d"),
		"--ro-bind", "/dev/null", filepath.Join(home, ".cargo", "credentials.toml"),
		"--ro-bind", "/dev/null", filepath.Join(home, ".vault-token"),
		"--unshare-net",
		"--new-session",
		"--die-with-parent",
		"--", "true",
	)
}

// wantRunArgv is the same flags around `bash -c`, a literal expectation.
func wantRunArgv(root, home, vcmd string) []string {
	argv := wantProbeArgv(root, home)
	return append(argv[:len(argv)-1], "bash", "-c", vcmd)
}

func envValue(env []string, name string) string {
	for _, kv := range env {
		if key, value, ok := strings.Cut(kv, "="); ok && key == name {
			return value
		}
	}
	return ""
}

// checkSeatbeltProfile pins the generated macOS rules against literal inputs:
// network, every Mach service but one, and every write denied up front, the
// shared host temp roots denied, the reviewed root and the private TMPDIR
// allowed, and every credential path under HOME denied.
func checkSeatbeltProfile(t *testing.T, profile, root, home string) {
	t.Helper()
	const header = `(version 1)(allow default)(deny network*)` +
		`(deny mach-lookup)(allow mach-lookup (global-name "com.apple.system.opendirectoryd.libinfo"))` +
		`(deny lsopen)(deny appleevent-send)(deny job-creation)(deny distributed-notification-post)` +
		`(deny signal)(allow signal (target same-sandbox))` +
		`(deny file-write* (subpath "/"))`
	if !strings.HasPrefix(profile, header) {
		t.Errorf("profile does not open with %q:\n%s", header, profile)
	}
	for _, want := range []string{
		`(deny file-read* file-write* (subpath "` + home + `/.vault-token"))`,
		`(deny file-read* file-write* (subpath "` + home + `/.ssh"))`,
		`(deny file-read* file-write* (subpath "` + home + `/.aws"))`,
		`(deny file-read* file-write* (subpath "` + home + `/.netrc"))`,
		`(deny file-read* file-write* (subpath "/tmp"))`,
		`(deny file-read* file-write* (subpath "/private/tmp"))`,
		`(allow file-read* file-write* (subpath "` + root + `"))`,
	} {
		if !strings.Contains(profile, want) {
			t.Errorf("profile misses %q:\n%s", want, profile)
		}
	}
}

// checkSeatbeltPair validates one probe/command pair that shares a profile and
// a private TMPDIR.
func checkSeatbeltPair(t *testing.T, probe, run *core.Command, root, home, vcmd string) {
	t.Helper()
	profile := requireSeatbeltProbe(t, probe, root, home)
	requireSeatbeltRun(t, run, profile, vcmd)
	requireSeatbeltPrivateEnv(t, probe, run, profile)
}

// requireSeatbeltProbe pins the probe argv and returns its profile.
func requireSeatbeltProbe(t *testing.T, probe *core.Command, root, home string) string {
	t.Helper()
	if len(probe.Argv) != 6 || probe.Argv[0] != "/usr/bin/sandbox-exec" || probe.Argv[1] != "-p" {
		t.Fatalf("probe argv = %v, want sandbox-exec -p <profile> bash -c <script>", probe.Argv)
	}
	profile := probe.Argv[2]
	checkSeatbeltProfile(t, profile, root, home)
	if got := probe.Argv[3:]; !slices.Equal(got, []string{"bash", "-c", wantProbeScript}) {
		t.Errorf("probe tail = %v, want bash -c %q", got, wantProbeScript)
	}
	return profile
}

// requireSeatbeltRun pins the wrapped command to the probe's profile.
func requireSeatbeltRun(t *testing.T, run *core.Command, profile, vcmd string) {
	t.Helper()
	if len(run.Argv) != 6 || run.Argv[2] != profile {
		t.Fatalf("run argv = %v, want the probe's profile", run.Argv)
	}
	if got := run.Argv[3:]; !slices.Equal(got, []string{"bash", "-c", vcmd}) {
		t.Errorf("run tail = %v, want bash -c %q", got, vcmd)
	}
}

// requireSeatbeltPrivateEnv pins one private TMPDIR across TMPDIR/TMP/TEMP and
// proves the profile allows it.
func requireSeatbeltPrivateEnv(t *testing.T, probe, run *core.Command, profile string) {
	t.Helper()
	private := envValue(probe.Env, "TMPDIR")
	if private == "" || private == "/tmp" || private == os.TempDir() {
		t.Fatalf("probe TMPDIR = %q, want a per-run private directory", private)
	}
	for _, name := range []string{"TMPDIR", "TMP", "TEMP", "GOTMPDIR"} {
		if got := envValue(run.Env, name); got != private {
			t.Errorf("run %s = %q, want %q", name, got, private)
		}
	}
	if got := envValue(run.Env, "GOCACHE"); got != private+"/go-build" {
		t.Errorf("run GOCACHE = %q, want %q", got, private+"/go-build")
	}
	if !strings.Contains(profile, `(allow file-read* file-write* (subpath "`+private+`"))`) {
		t.Errorf("profile does not allow the private TMPDIR %q:\n%s", private, profile)
	}
}

// assertPrivateGone proves the per-run temp directory is cleaned up when the
// verify call returns.
func assertPrivateGone(t *testing.T, cmdr *sandboxCommander) {
	t.Helper()
	if len(cmdr.calls) == 0 {
		return
	}
	private := envValue(cmdr.calls[0].Env, "TMPDIR")
	if private == "" || private == os.TempDir() {
		return
	}
	if _, err := os.Stat(private); !os.IsNotExist(err) {
		t.Errorf("private TMPDIR %q survived the run (%v)", private, err)
	}
}

// TestSandboxWrapsVerifyCommand pins the exact bwrap argv on Linux and the
// generated Seatbelt profile on macOS: the probe and the command share the
// mechanism, and the command still runs as `bash -c` inside the wrapper.
func TestSandboxWrapsVerifyCommand(t *testing.T) {
	requireSandboxHost(t)
	root, home, resp, cmdr := runSandboxedVerify(t, "printf ok", nil)
	checkSandboxSpawn(t, cmdr, root, home, "printf ok")
	checkSandboxRecord(t, resp, hostSandboxTool())
	assertPrivateGone(t, cmdr)
}

// runSandboxedVerify drives one sandboxed patch run in a fresh bundle.
func runSandboxedVerify(t *testing.T, vcmd string, results []core.Result) (string, string, *verifycore.Response, *sandboxCommander) {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	testkit.WriteFile(t, ".git/HEAD", "ref: refs/heads/main\n")
	raw, err := json.Marshal(map[string]string{"verifyCmd": vcmd})
	if err != nil {
		t.Fatal(err)
	}
	testkit.WriteFile(t, ".verdict/state/5201/patch.json", string(raw))
	home := sandboxHomeFixture(t)
	if results == nil {
		results = []core.Result{{}, {Stdout: "ok"}}
	}
	cmdr := &sandboxCommander{results: results}
	req := sandboxRequest([]string{"PATH=/bin", "HOME=" + home}, cmdr)
	req.RepoRoot = root
	resp, ref := verifycore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	return root, home, resp, cmdr
}

// checkSandboxSpawn compares both spawns with the literal argv shapes.
func checkSandboxSpawn(t *testing.T, cmdr *sandboxCommander, root, home, vcmd string) {
	t.Helper()
	if len(cmdr.calls) != 2 {
		t.Fatalf("spawns = %d, want probe + command", len(cmdr.calls))
	}
	if runtime.GOOS == "darwin" {
		checkSeatbeltPair(t, cmdr.calls[0], cmdr.calls[1], root, home, vcmd)
	} else {
		checkBwrapSpawn(t, cmdr, root, home, vcmd)
	}
	// The probe and the command run in the repo root the tree probes read,
	// as the deep-group path does, never the caller's inherited cwd.
	if cmdr.calls[0].Dir != root || cmdr.calls[1].Dir != root {
		t.Errorf("patch-mode dirs = %q, %q, want the repo root %q", cmdr.calls[0].Dir, cmdr.calls[1].Dir, root)
	}
}

// checkBwrapSpawn pins the exact Linux bwrap probe and command argv.
func checkBwrapSpawn(t *testing.T, cmdr *sandboxCommander, root, home, vcmd string) {
	t.Helper()
	if got := cmdr.calls[0].Argv; !slices.Equal(got, wantProbeArgv(root, home)) {
		t.Errorf("probe argv = %v, want %v", got, wantProbeArgv(root, home))
	}
	if got := cmdr.calls[1].Argv; !slices.Equal(got, wantRunArgv(root, home, vcmd)) {
		t.Errorf("run argv = %v, want %v", got, wantRunArgv(root, home, vcmd))
	}
}

// checkSandboxRecord asserts the outcome names the sandbox and its mechanism
// on both surfaces.
func checkSandboxRecord(t *testing.T, resp *verifycore.Response, mechanism string) {
	t.Helper()
	if !resp.Data.Sandboxed {
		t.Error("data.sandboxed = false, want true")
	}
	if resp.Data.SandboxMechanism != mechanism {
		t.Errorf("data.sandboxMechanism = %q, want %q", resp.Data.SandboxMechanism, mechanism)
	}
	raw, err := os.ReadFile(".verdict/state/5201/patch.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"sandboxed": true`) {
		t.Errorf("patch.json does not record the sandbox: %s", raw)
	}
	if !strings.Contains(string(raw), `"sandboxMechanism": "`+mechanism+`"`) {
		t.Errorf("patch.json does not record mechanism %q: %s", mechanism, raw)
	}
	out, err := os.ReadFile(".verdict/state/5201/.verify.out")
	if err != nil || string(out) != "ok" {
		t.Errorf(".verify.out = %q, %v", out, err)
	}
}

// TestSandboxProbeFailureRefusesWithoutRunning proves a host that cannot
// enforce the sandbox runs nothing and leaves the prior proof untouched.
func TestSandboxProbeFailureRefusesWithoutRunning(t *testing.T) {
	requireSandboxHost(t)
	before, cmdr, ref := runProbeFailure(t)
	checkProbeRefusal(t, ref)
	checkProbeOnlySpawn(t, cmdr)
	checkNothingWritten(t, before)
	assertPrivateGone(t, cmdr)
}

// runProbeFailure seeds a sentinel-writing command and a mechanism spawn
// failure on the probe.
func runProbeFailure(t *testing.T) ([]byte, *sandboxCommander, *core.RefusalError) {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	testkit.WriteFile(t, ".verdict/state/5201/patch.json", `{"verifyCmd":"printf created > sentinel.txt"}`)
	testkit.WriteFile(t, ".git/HEAD", "ref: refs/heads/main\n")
	before, err := os.ReadFile(".verdict/state/5201/patch.json")
	if err != nil {
		t.Fatal(err)
	}
	cmdr := &sandboxCommander{errs: []error{errors.New(`exec: "` + hostSandboxTool() + `": executable file not found in $PATH`)}}
	req := sandboxRequest([]string{"PATH=/bin", "HOME=/home/eval"}, cmdr)
	req.RepoRoot = root
	req.Warnings = []string{"prefs warning"}
	_, ref := verifycore.Run(context.Background(), req)
	return before, cmdr, ref
}

// checkProbeRefusal asserts the typed refusal carries the mechanism cause and
// the request warnings.
func checkProbeRefusal(t *testing.T, ref *core.RefusalError) {
	t.Helper()
	if ref == nil || ref.Code != "sandbox-unavailable" {
		t.Fatalf("ref = %v, want sandbox-unavailable", ref)
	}
	if !strings.Contains(ref.Message, hostSandboxTool()+" could not start") {
		t.Errorf("message = %q", ref.Message)
	}
	if len(ref.Warnings) != 1 || ref.Warnings[0] != "prefs warning" {
		t.Errorf("warnings = %v, want prefs warning carried", ref.Warnings)
	}
}

// checkProbeOnlySpawn proves the probe ran alone, before any command spawn.
func checkProbeOnlySpawn(t *testing.T, cmdr *sandboxCommander) {
	t.Helper()
	if len(cmdr.calls) != 1 {
		t.Fatalf("spawns = %d, want only the probe", len(cmdr.calls))
	}
	argv := cmdr.calls[0].Argv
	last := argv[len(argv)-1]
	if runtime.GOOS == "darwin" {
		if last != wantProbeScript {
			t.Errorf("probe argv = %v, want the macOS smoke script", argv)
		}
	} else if last != "true" {
		t.Errorf("probe argv = %v, want a true probe", argv)
	}
}

// checkNothingWritten asserts the refusal left the proof and the sentinel
// untouched.
func checkNothingWritten(t *testing.T, before []byte) {
	t.Helper()
	after, err := os.ReadFile(".verdict/state/5201/patch.json")
	if err != nil || !slices.Equal(after, before) {
		t.Errorf("patch.json changed on refusal: %q -> %q (%v)", before, after, err)
	}
	if _, err := os.Stat(".verdict/state/5201/.verify.out"); !os.IsNotExist(err) {
		t.Error("refusal wrote .verify.out")
	}
	if _, err := os.Stat("sentinel.txt"); !os.IsNotExist(err) {
		t.Error("refusal executed the command")
	}
}

// TestSandboxWithoutHomeRefuses proves the credential paths are never left
// exposed: no child HOME means no verified hiding place, so nothing spawns.
func TestSandboxWithoutHomeRefuses(t *testing.T) {
	requireSandboxHost(t)
	root := t.TempDir()
	t.Chdir(root)
	testkit.WriteFile(t, ".verdict/state/5201/patch.json", `{"verifyCmd":"printf ok"}`)
	cmdr := &sandboxCommander{}
	req := sandboxRequest([]string{"PATH=/bin"}, cmdr)
	req.RepoRoot = root
	_, ref := verifycore.Run(context.Background(), req)
	if ref == nil || ref.Code != "sandbox-unavailable" {
		t.Fatalf("ref = %v, want sandbox-unavailable", ref)
	}
	if !strings.Contains(ref.Message, "HOME") {
		t.Errorf("message = %q, want the HOME cause", ref.Message)
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("spawns = %d, want none", len(cmdr.calls))
	}
}

// TestSandboxFlagsAreMutuallyExclusive proves the caller must state one
// boundary: both flags refuse bad-flags before anything spawns or is written.
func TestSandboxFlagsAreMutuallyExclusive(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	testkit.WriteFile(t, ".verdict/state/5201/patch.json", `{"verifyCmd":"printf ok"}`)
	before, err := os.ReadFile(".verdict/state/5201/patch.json")
	if err != nil {
		t.Fatal(err)
	}
	cmdr := &sandboxCommander{}
	req := sandboxRequest([]string{"PATH=/bin", "HOME=" + t.TempDir()}, cmdr)
	req.SandboxNetworkOnly = true
	req.RepoRoot = root
	_, ref := verifycore.Run(context.Background(), req)
	if ref == nil || ref.Code != "bad-flags" {
		t.Fatalf("ref = %v, want bad-flags", ref)
	}
	for _, want := range []string{"--sandbox", "--sandbox-network-only", "mutually exclusive"} {
		if !strings.Contains(ref.Message, want) {
			t.Errorf("message = %q, want %q named", ref.Message, want)
		}
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("spawns = %d, want none", len(cmdr.calls))
	}
	checkNothingWritten(t, before)
}

// TestSandboxNetworkOnlyOnLinux wraps with unshare and labels the run, while
// every other host refuses the Linux-only opt-in without spawning.
func TestSandboxNetworkOnlyOnLinux(t *testing.T) {
	root, cmdr, resp, ref := runNetworkOnlyRequest(t)
	if runtime.GOOS != "linux" {
		requireNetworkOnlyRefusal(t, ref, cmdr)
		return
	}
	if ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	_ = root
	checkNetworkOnlyRun(t, cmdr, resp)
}

// runNetworkOnlyRequest drives one patch run with only the network-only
// opt-in set.
func runNetworkOnlyRequest(t *testing.T) (string, *sandboxCommander, *verifycore.Response, *core.RefusalError) {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	testkit.WriteFile(t, ".verdict/state/5201/patch.json", `{"verifyCmd":"printf ok"}`)
	cmdr := &sandboxCommander{results: []core.Result{{}, {Stdout: "ok"}}}
	req := sandboxRequest([]string{"PATH=/bin", "HOME=" + t.TempDir()}, cmdr)
	req.Sandbox = false
	req.SandboxNetworkOnly = true
	req.RepoRoot = root
	resp, ref := verifycore.Run(context.Background(), req)
	return root, cmdr, resp, ref
}

// requireNetworkOnlyRefusal proves a non-Linux host refuses the opt-in with
// the Linux mechanism named and spawns nothing.
func requireNetworkOnlyRefusal(t *testing.T, ref *core.RefusalError, cmdr *sandboxCommander) {
	t.Helper()
	if ref == nil || ref.Code != "sandbox-unavailable" {
		t.Fatalf("ref = %v, want sandbox-unavailable on %s", ref, runtime.GOOS)
	}
	if !strings.Contains(ref.Message, "Linux") || !strings.Contains(ref.Message, "unshare") {
		t.Errorf("message = %q, want the Linux opt-in named", ref.Message)
	}
	if len(cmdr.calls) != 0 {
		t.Errorf("spawns = %d, want none", len(cmdr.calls))
	}
}

// checkNetworkOnlyRun pins the unshare argv and the metadata that keeps the
// weaker boundary visible.
func checkNetworkOnlyRun(t *testing.T, cmdr *sandboxCommander, resp *verifycore.Response) {
	t.Helper()
	requireUnshareArgv(t, cmdr)
	requireNetworkOnlyRecord(t, resp)
	requireNetworkOnlyPatch(t)
}

// requireUnshareArgv pins the exact network-only probe and command argv.
func requireUnshareArgv(t *testing.T, cmdr *sandboxCommander) {
	t.Helper()
	prefix := []string{"unshare", "--user", "--map-root-user", "--net", "--"}
	if len(cmdr.calls) != 2 || !slices.Equal(cmdr.calls[0].Argv, append(slices.Clone(prefix), "true")) {
		t.Fatalf("probe argv = %v, want %v true", cmdr.calls[0].Argv, prefix)
	}
	if got, want := cmdr.calls[1].Argv, append(slices.Clone(prefix), "bash", "-c", "printf ok"); !slices.Equal(got, want) {
		t.Errorf("run argv = %v, want %v", got, want)
	}
}

// requireNetworkOnlyRecord pins the data fields and the warning a reader needs
// to tell the network-only run from the full shape: the mechanism is named,
// and `sandboxed` stays false because it means the full shape.
func requireNetworkOnlyRecord(t *testing.T, resp *verifycore.Response) {
	t.Helper()
	if resp.Data.Sandboxed || resp.Data.SandboxMechanism != "unshare-net" {
		t.Errorf("data sandboxed/mechanism = %v/%q, want false/unshare-net", resp.Data.Sandboxed, resp.Data.SandboxMechanism)
	}
	if !hasNetworkOnlyWarning(resp.Warnings) {
		t.Errorf("warnings = %v, want the no-mount network-only warning", resp.Warnings)
	}
}

// requireNetworkOnlyPatch pins the mechanism in the stored patch record.
func requireNetworkOnlyPatch(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile(".verdict/state/5201/patch.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"sandboxMechanism": "unshare-net"`) || strings.Contains(string(raw), `"sandboxed"`) {
		t.Errorf("patch.json = %s, want unshare-net recorded without sandboxed", raw)
	}
}

func hasNetworkOnlyWarning(warnings []string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, "network-only") && strings.Contains(warning, "no mount, filesystem, or PID isolation") {
			return true
		}
	}
	return false
}

// groupSandboxCommander answers the git head probes and records every spawn,
// so the probe's place between the head read and the wrapped command shows.
type groupSandboxCommander struct {
	runs []*core.Command
}

func (c *groupSandboxCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
	c.runs = append(c.runs, cmd)
	if len(cmd.Argv) > 3 && cmd.Argv[0] == "git" {
		return core.Result{Stdout: groupHead + "\n"}, nil
	}
	return core.Result{Stdout: "ok\n"}, nil
}

// TestSandboxGroupValidationWrapsTheOperatorCommand proves the deep-group
// path uses the same wrapper and records sandboxed on the receipt.
func TestSandboxGroupValidationWrapsTheOperatorCommand(t *testing.T) {
	requireSandboxHost(t)
	root, home, resp, cmdr := runSandboxedGroup(t)
	checkGroupCommandArgv(t, root, home, cmdr)
	checkGroupReceipt(t, root, resp, hostSandboxTool())
}

func runSandboxedGroup(t *testing.T) (string, string, *verifycore.Response, *groupSandboxCommander) {
	t.Helper()
	root := groupBundle(t)
	testkit.WriteFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	home := sandboxHomeFixture(t)
	cmdr := &groupSandboxCommander{}
	req := groupRequest(root, "group-a1", "go test ./...", cmdr)
	req.Sandbox = true
	req.Environ = []string{"PATH=/bin", "HOME=" + home}
	verifycore.SetAccountHomeForTest(req, home)
	resp, ref := verifycore.Run(context.Background(), req)
	if ref != nil {
		t.Fatalf("Run: %v", ref)
	}
	return root, home, resp, cmdr
}

// checkGroupCommandArgv asserts the third spawn is the wrapped operator
// command between the two git head probes.
func checkGroupCommandArgv(t *testing.T, root, home string, cmdr *groupSandboxCommander) {
	t.Helper()
	if len(cmdr.runs) != 4 {
		t.Fatalf("spawns = %d, want head, probe, command, head", len(cmdr.runs))
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "darwin" {
		checkSeatbeltPair(t, cmdr.runs[1], cmdr.runs[2], canonical, home, "go test ./...")
		return
	}
	if got := cmdr.runs[2].Argv; !slices.Equal(got, wantRunArgv(canonical, home, "go test ./...")) {
		t.Errorf("command argv = %v, want %v", got, wantRunArgv(canonical, home, "go test ./..."))
	}
}

// checkGroupReceipt asserts the response and receipt both carry sandboxed and
// the mechanism.
func checkGroupReceipt(t *testing.T, root string, resp *verifycore.Response, mechanism string) {
	t.Helper()
	if !resp.Data.Sandboxed || resp.Data.SandboxMechanism != mechanism {
		t.Errorf("data sandboxed/mechanism = %v/%q, want true/%q", resp.Data.Sandboxed, resp.Data.SandboxMechanism, mechanism)
	}
	dir := filepath.Join(root, ".verdict", "state", "7")
	receipt, found, ref := verifycore.ReadValidationReceipt(dir, "group-a1")
	if ref != nil || !found {
		t.Fatalf("receipt found=%v ref=%v", found, ref)
	}
	if !receipt.Sandboxed || receipt.SandboxMechanism != mechanism {
		t.Errorf("receipt sandboxed/mechanism = %v/%q, want true/%q", receipt.Sandboxed, receipt.SandboxMechanism, mechanism)
	}
}

// TestUnsandboxedRunClearsAStaleSandboxClaim proves the record describes the
// latest run: a sandboxed run followed by an unsandboxed one cannot leave
// `sandboxed: true` standing, nor a stale mechanism.
func TestUnsandboxedRunClearsAStaleSandboxClaim(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	testkit.WriteFile(t, ".verdict/state/5201/patch.json",
		`{"verifyCmd":"printf ok","sandboxed":true,"sandboxMechanism":"bwrap","verifyExit":0}`)
	cmdr := &sandboxCommander{results: []core.Result{{Stdout: "ok"}}}
	req := sandboxRequest([]string{"PATH=/bin", "HOME=" + sandboxHomeFixture(t)}, cmdr)
	req.Sandbox = false
	req.RepoRoot = root
	if _, ref := verifycore.Run(context.Background(), req); ref != nil {
		t.Fatalf("Run refusal: %v", ref)
	}
	raw, err := os.ReadFile(".verdict/state/5201/patch.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"sandboxed"`) || strings.Contains(string(raw), `"sandboxMechanism"`) {
		t.Errorf("unsandboxed run left a stale sandbox claim: %s", raw)
	}
}
