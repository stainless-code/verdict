package verifycore_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
)

// sandboxCommander scripts one result per spawn in call order, so the probe
// and the wrapped command stay distinguishable.
type sandboxCommander struct {
	calls   []*core.Command
	results []core.Result
	errs    []error
}

func (c *sandboxCommander) Run(_ context.Context, cmd *core.Command) (core.Result, error) {
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

// sandboxHomeFixture builds every credential path the wrapper hides and
// returns their home.
func sandboxHomeFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, rel := range []string{
		".aws", ".ssh", filepath.Join(".config", "gh"), ".docker", ".kube",
		filepath.Join(".config", "gcloud"), ".azure", ".gnupg",
	} {
		if err := os.MkdirAll(filepath.Join(home, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []string{".netrc", ".git-credentials", ".npmrc", ".pypirc"} {
		if err := os.WriteFile(filepath.Join(home, rel), []byte("sandbox-sentinel\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func sandboxRequest(env []string, cmdr core.Commander) *core.VerifyRequest {
	read := core.ReadRequest{
		Runtime: &core.Runtime{Now: func() time.Time { return fixedNow }, Progress: &collector{}, Commander: cmdr},
		Repo:    "acme/app", Pr: "5201",
	}
	return &core.VerifyRequest{
		ReadRequest: read,
		TimeoutRaw:  "10",
		Sandbox:     true,
		Environ:     env,
		JSONMode:    true,
	}
}

// wantVarRunMount mirrors the host fact the wrapper reads, so a supported
// platform that symlinks /var/run keeps the literal expectation.
func wantVarRunMount() []string {
	if info, err := os.Lstat("/var/run"); err == nil && info.IsDir() {
		return []string{"--tmpfs", "/var/run"}
	}
	return nil
}

// wantProbeArgv is the literal no-op probe argv, mounts included.
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

// TestSandboxWrapsVerifyCommand pins the exact bwrap argv: the probe and the
// command share the mount and namespace flags, and the command still runs as
// `bash -c` inside the wrapper.
func TestSandboxWrapsVerifyCommand(t *testing.T) {
	root, home, resp, cmdr := runSandboxedVerify(t, "printf ok", nil)
	checkSandboxSpawn(t, cmdr, root, home, "printf ok")
	checkSandboxRecord(t, resp)
}

// runSandboxedVerify drives one sandboxed patch run in a fresh bundle.
func runSandboxedVerify(t *testing.T, vcmd string, results []core.Result) (string, string, *core.VerifyResponse, *sandboxCommander) {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	raw, err := json.Marshal(map[string]string{"verifyCmd": vcmd})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, ".verdict/state/5201/patch.json", string(raw))
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
	if got := cmdr.calls[0].Argv; !slices.Equal(got, wantProbeArgv(root, home)) {
		t.Errorf("probe argv = %v, want %v", got, wantProbeArgv(root, home))
	}
	if got := cmdr.calls[1].Argv; !slices.Equal(got, wantRunArgv(root, home, vcmd)) {
		t.Errorf("run argv = %v, want %v", got, wantRunArgv(root, home, vcmd))
	}
	if cmdr.calls[0].Dir != "" || cmdr.calls[1].Dir != "" {
		t.Errorf("patch-mode dirs = %q, %q, want inherited cwd", cmdr.calls[0].Dir, cmdr.calls[1].Dir)
	}
}

// checkSandboxRecord asserts the outcome names the sandbox on both surfaces.
func checkSandboxRecord(t *testing.T, resp *core.VerifyResponse) {
	t.Helper()
	if !resp.Data.Sandboxed {
		t.Error("data.sandboxed = false, want true")
	}
	raw, err := os.ReadFile(".verdict/state/5201/patch.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"sandboxed": true`) {
		t.Errorf("patch.json does not record the sandbox: %s", raw)
	}
	out, err := os.ReadFile(".verdict/state/5201/.verify.out")
	if err != nil || string(out) != "ok" {
		t.Errorf(".verify.out = %q, %v", out, err)
	}
}

// TestSandboxProbeFailureRefusesWithoutRunning proves a host that cannot
// enforce the sandbox runs nothing and leaves the prior proof untouched.
func TestSandboxProbeFailureRefusesWithoutRunning(t *testing.T) {
	before, cmdr, ref := runProbeFailure(t)
	checkProbeRefusal(t, ref)
	if len(cmdr.calls) != 1 || cmdr.calls[0].Argv[len(cmdr.calls[0].Argv)-1] != "true" {
		t.Fatalf("spawns = %d, want only the probe", len(cmdr.calls))
	}
	checkNothingWritten(t, before)
}

// runProbeFailure seeds a sentinel-writing command and a bwrap spawn failure.
func runProbeFailure(t *testing.T) ([]byte, *sandboxCommander, *core.RefusalError) {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	writeFile(t, ".verdict/state/5201/patch.json", `{"verifyCmd":"printf created > sentinel.txt"}`)
	before, err := os.ReadFile(".verdict/state/5201/patch.json")
	if err != nil {
		t.Fatal(err)
	}
	cmdr := &sandboxCommander{errs: []error{errors.New(`exec: "bwrap": executable file not found in $PATH`)}}
	req := sandboxRequest([]string{"PATH=/bin", "HOME=/home/eval"}, cmdr)
	req.RepoRoot = root
	req.Warnings = []string{"prefs warning"}
	_, ref := verifycore.Run(context.Background(), req)
	return before, cmdr, ref
}

// checkProbeRefusal asserts the typed refusal carries the bwrap cause and
// the request warnings.
func checkProbeRefusal(t *testing.T, ref *core.RefusalError) {
	t.Helper()
	if ref == nil || ref.Code != "sandbox-unavailable" {
		t.Fatalf("ref = %v, want sandbox-unavailable", ref)
	}
	if !strings.Contains(ref.Message, "bwrap could not start") {
		t.Errorf("message = %q", ref.Message)
	}
	if len(ref.Warnings) != 1 || ref.Warnings[0] != "prefs warning" {
		t.Errorf("warnings = %v, want prefs warning carried", ref.Warnings)
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

// TestSandboxNamespaceFailureNamesTheCause proves a probe that starts but
// cannot build its namespaces refuses with bwrap's own diagnostic.
func TestSandboxNamespaceFailureNamesTheCause(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeFile(t, ".verdict/state/5201/patch.json", `{"verifyCmd":"printf ok"}`)
	cmdr := &sandboxCommander{results: []core.Result{{
		Stdout:   "bwrap: No permissions to creating new namespace, likely because the kernel does not allow non-privileged user namespaces.",
		ExitCode: 1,
	}}}
	req := sandboxRequest([]string{"PATH=/bin", "HOME=/home/eval"}, cmdr)
	req.RepoRoot = root
	_, ref := verifycore.Run(context.Background(), req)
	if ref == nil || ref.Code != "sandbox-unavailable" {
		t.Fatalf("ref = %v, want sandbox-unavailable", ref)
	}
	if !strings.Contains(ref.Message, "unprivileged user namespaces") {
		t.Errorf("message = %q, want the namespace cause", ref.Message)
	}
	if len(cmdr.calls) != 1 {
		t.Errorf("spawns = %d, want the lone probe", len(cmdr.calls))
	}
}

// TestSandboxWithoutHomeRefuses proves the credential dirs are never left
// exposed: no child HOME means no verified hiding place, so nothing spawns.
func TestSandboxWithoutHomeRefuses(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeFile(t, ".verdict/state/5201/patch.json", `{"verifyCmd":"printf ok"}`)
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
	root, home, resp, cmdr := runSandboxedGroup(t)
	checkGroupCommandArgv(t, root, home, cmdr)
	checkGroupReceipt(t, root, resp)
}

func runSandboxedGroup(t *testing.T) (string, string, *core.VerifyResponse, *groupSandboxCommander) {
	t.Helper()
	root := groupBundle(t)
	home := sandboxHomeFixture(t)
	cmdr := &groupSandboxCommander{}
	req := groupRequest(root, "group-a1", "go test ./...", cmdr)
	req.Sandbox = true
	req.Environ = []string{"PATH=/bin", "HOME=" + home}
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
	if got := cmdr.runs[2].Argv; !slices.Equal(got, wantRunArgv(canonical, home, "go test ./...")) {
		t.Errorf("command argv = %v, want %v", got, wantRunArgv(canonical, home, "go test ./..."))
	}
}

// checkGroupReceipt asserts the response and receipt both carry sandboxed.
func checkGroupReceipt(t *testing.T, root string, resp *core.VerifyResponse) {
	t.Helper()
	if !resp.Data.Sandboxed {
		t.Error("data.sandboxed = false, want true")
	}
	dir := filepath.Join(root, ".verdict", "state", "7")
	receipt, found, ref := verifycore.ReadValidationReceipt(dir, "group-a1")
	if ref != nil || !found {
		t.Fatalf("receipt found=%v ref=%v", found, ref)
	}
	if !receipt.Sandboxed {
		t.Error("receipt.sandboxed = false, want true")
	}
}

// TestUnsandboxedRunClearsAStaleSandboxClaim proves the record describes the
// latest run: a sandboxed run followed by an unsandboxed one cannot leave
// `sandboxed: true` standing.
func TestUnsandboxedRunClearsAStaleSandboxClaim(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeFile(t, ".verdict/state/5201/patch.json",
		`{"verifyCmd":"printf ok","sandboxed":true,"verifyExit":0}`)
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
	if strings.Contains(string(raw), `"sandboxed"`) {
		t.Errorf("unsandboxed run left a stale sandbox claim: %s", raw)
	}
}
