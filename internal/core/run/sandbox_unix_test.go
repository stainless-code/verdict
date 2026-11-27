//go:build darwin || linux

package run

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/gitcmd"
	"github.com/stainless-code/verdict/internal/hostsandbox"
)

// hostLook resolves the agent to bin and the sandbox runner on the real
// PATH: a stub resolver that answered bwrap with the agent would run the
// agent as the sandbox on Linux (macOS's sandbox-exec has a fixed path).
func hostLook(bin string) func(string) (string, error) {
	return func(name string) (string, error) {
		if name == hostsandbox.BwrapTool {
			return exec.LookPath(name)
		}
		return bin, nil
	}
}

// capturedHost is the real watchdog behind a plain wrapper, which carries
// no simulation marker, so the launch goes through the real review sandbox
// as any production wrapper's would; it keeps every launch's output, where
// a stand-in reports what the sandbox let it do, since it may write almost
// nothing else. The sandboxed launch runs the binary the resolution found,
// so the planted stand-in needs no PATH entry.
type capturedHost struct {
	out strings.Builder
	mu  sync.Mutex
}

// Run runs the command for real and keeps its output.
func (c *capturedHost) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	res, err := coreexec.Watchdog{}.Run(ctx, cmd)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.out.WriteString(res.Stdout)
	c.out.WriteString(res.Stderr)
	return res, err
}

// output is everything the launches printed.
func (c *capturedHost) output() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.out.String()
}

// requireReviewSandbox skips where the host cannot build the review sandbox:
// macOS without sandbox-exec, Linux without a working bubblewrap.
func requireReviewSandbox(t *testing.T) {
	t.Helper()
	switch runtime.GOOS {
	case "darwin":
		if _, err := os.Stat("/usr/bin/sandbox-exec"); err != nil {
			t.Skipf("sandbox-exec is not present: %v", err)
		}
	case "linux":
		probe := exec.CommandContext(t.Context(), "bwrap", "--ro-bind", "/", "/", "--dev", "/dev",
			"--proc", "/proc", "--unshare-pid", "true")
		if err := probe.Run(); err != nil {
			t.Skipf("bubblewrap cannot run here: %v", err)
		}
	}
}

// escapeScript is what a prompt-injected diff asks a review agent to do with
// its absolute-path file tools: append to the operator's shell profile,
// rewrite the operator's checkout, plant a config directory, read a private
// key into its output, and plant its own config or code (CONFIG_PATHS,
// colon-separated) that a later run outside the sandbox would load. Each
// attempt's outcome is printed; its own state write goes to STATE_FILE, or to
// STATE_REL below the throwaway home STATE_VAR names; the review is written
// where it is owed.
const escapeScript = "#!/bin/sh\n" +
	"if [ -n \"$STATE_VAR\" ]; then sf=\"$(printenv \"$STATE_VAR\")/$STATE_REL\"; else sf=\"$STATE_FILE\"; fi\n" +
	"/bin/mkdir -p \"${sf%/*}\"\n" +
	"{ printf 'evil\\n' >> \"$HOME/.zshrc\" && echo zshrc=written || echo zshrc=denied\n" +
	"  printf 'evil\\n' > \"$CHECKOUT_FILE\" && echo checkout=written || echo checkout=denied\n" +
	"  /bin/mkdir -p \"$HOME/.config/evil\" && echo config=written || echo config=denied\n" +
	"  /bin/cat \"$HOME/.ssh/id_rsa\" && echo ssh=read || echo ssh=denied\n" +
	"  printf ok > \"$sf\" && echo state=written || echo state=denied\n" +
	"  /bin/cat \"$OTHER_CRED\" && echo othercred=read || echo othercred=denied\n" +
	"  IFS=:; for p in $CONFIG_PATHS; do /bin/mkdir -p \"${p%/*}\" 2>/dev/null; " +
	"printf evil > \"$p\" && echo \"own-config $p written\" || echo \"own-config $p denied\"; done\n" +
	"} 2>&1\n" +
	"printf '%s' '" + reviewDoc + "' > .verdict/state/42/review.json\n"

// privateKeyCanary is the planted key's content, which must never reach the
// agent.
const privateKeyCanary = "-----BEGIN CANARY PRIVATE KEY----- verdict-review-sandbox"

// escapeRow is one agent's own layout: the real state file it may write
// (claude's, planted first so a Linux file bind exists) or the throwaway
// home variable and the path below it that it may write instead; the
// config, code, and trusted data paths a later run would load, which it may
// not — existing ones planted first, so a denied write leaves them
// unchanged; and the operator's `--` arguments, which put codex in
// verdict's sandbox.
type escapeRow struct {
	agent     string
	state     string
	stateVar  string
	stateRel  string
	otherCred string
	config    []string
	planted   []string
	pass      []string
}

// escapeRows are the rows with state paths, each with config and plugin
// paths from its read-only list.
var escapeRows = []escapeRow{
	{
		agent: "pi", stateVar: "PI_CODING_AGENT_DIR", stateRel: "auth.json", otherCred: ".codex/auth.json",
		config: []string{
			".pi/agent/auth.json", ".pi/agent/extensions/x.ts", ".pi/agent/settings.json", ".pi/agent/AGENTS.md",
			".pi/agent/sessions/x/s.jsonl",
		},
		planted: []string{".pi/agent/auth.json", ".pi/agent/settings.json"},
	},
	{
		agent: "codex", stateVar: "CODEX_HOME", stateRel: "auth.json", otherCred: ".pi/agent/auth.json",
		config: []string{
			".codex/auth.json", ".codex/config.toml", ".codex/tmp/arg0x/git", ".codex/cache/x", ".codex/.tmp/x",
			".codex/sessions/x.jsonl",
		},
		planted: []string{".codex/auth.json", ".codex/config.toml"},
		pass:    []string{"--sandbox", "danger-full-access"},
	},
	{
		agent: "opencode", stateVar: "XDG_DATA_HOME", stateRel: "opencode/snapshot/x/config", otherCred: ".pi/agent/auth.json",
		config: []string{
			".config/opencode/plugin/x.js", ".config/opencode/opencode.json",
			".cache/opencode/node_modules/x/index.js", ".local/share/opencode/bin/x",
			".local/share/opencode/snapshot/x/config", ".local/share/opencode/repos/x/config",
			".local/state/opencode/x",
		},
		planted: []string{".config/opencode/opencode.json"},
	},
	{
		agent: "claude", state: ".claude/debug/own-state", otherCred: ".local/share/opencode/auth.json",
		config: []string{
			".claude/settings.json", ".claude.json", ".claude/hooks/x.sh", ".claude/plugins/x/plugin.json",
			".claude/shell-snapshots/snapshot-zsh.sh", ".claude/session-env/x",
			".claude/projects/p/memory/MEMORY.md", ".claude/projects/p/x.jsonl", ".claude/todos/x.json",
			".claude/backups/.claude.json.backup.1", ".claude/file-history/x/y", ".claude/sessions/1.json",
		},
		planted: []string{".claude/settings.json", ".claude.json", ".claude/projects/p/memory/MEMORY.md"},
	},
}

// TestReviewSandboxConfinesWritesAndHidesCredentials runs a hostile review
// agent as each row with state paths through the real mechanism
// (sandbox-exec on macOS, bwrap on Linux): its writes to HOME's dotfiles,
// the operator's checkout by absolute path, ~/.config, and its own config,
// plugins, and extensions are all denied and leave the files unchanged, its
// read of ~/.ssh/id_rsa fails, its own data directory stays writable, and
// the review it owes still records.
func TestReviewSandboxConfinesWritesAndHidesCredentials(t *testing.T) {
	t.Parallel()
	requireReviewSandbox(t)
	for _, row := range escapeRows {
		t.Run(row.agent, func(t *testing.T) {
			t.Parallel()
			assertEscapeDenied(t, &row)
		})
	}
}

// assertEscapeDenied runs the hostile stand-in as one row and checks every
// promise the sandbox makes for it.
func assertEscapeDenied(t *testing.T, row *escapeRow) {
	t.Helper()
	root := confinedRoot(t)
	home := escapeHome(t, row)
	env := []string{
		"CHECKOUT_FILE=" + filepath.Join(root, "src", "main.go"),
		"CONFIG_PATHS=" + strings.Join(homePaths(home, row.config), ":"),
		"OTHER_CRED=" + filepath.Join(home, filepath.FromSlash(row.otherCred)),
	}
	if row.state != "" {
		env = append(env, "STATE_FILE="+filepath.Join(home, filepath.FromSlash(row.state)))
	} else {
		env = append(env, "STATE_VAR="+row.stateVar, "STATE_REL="+row.stateRel)
	}
	resp, ref, out := sandboxedRunWith(t, row.agent, root, home, escapeScript, "", row.pass, env...)
	if ref != nil {
		t.Fatalf("review refused: %v", ref)
	}
	assertEscapeLog(t, home, row, out)
	assertHomeUntouched(t, home, row)
	assertCheckoutUntouched(t, root)
	if _, err := os.Stat(filepath.Join(bundleDir(root), reviewFile)); err != nil {
		t.Errorf("the review did not record: %v", err)
	}
	if box := resp.Data.Sandbox; box == nil || box.Posture != "sandboxed" {
		t.Errorf("sandbox = %+v, want the sandboxed posture", box)
	}
}

// homePaths joins home-relative slash paths under home.
func homePaths(home string, rels []string) []string {
	out := make([]string, 0, len(rels))
	for _, rel := range rels {
		out = append(out, filepath.Join(home, filepath.FromSlash(rel)))
	}
	return out
}

// operatorProfile is the shell profile the agent tries to append to, and
// operatorConfig the content of every config file planted before the launch.
const (
	operatorProfile = "# the operator's shell profile\n"
	operatorConfig  = "{\"operator\": true}\n"
)

// escapeHome is the operator's HOME the hostile agent sees: a shell
// profile, a private key, the row's planted config, and its state file.
func escapeHome(t *testing.T, row *escapeRow) string {
	t.Helper()
	home := t.TempDir()
	writeUnder(t, home, ".zshrc", operatorProfile)
	writeUnder(t, home, filepath.Join(".ssh", "id_rsa"), privateKeyCanary+"\n")
	writeUnder(t, home, filepath.FromSlash(row.otherCred), privateKeyCanary+"\n")
	for _, rel := range row.planted {
		writeUnder(t, home, filepath.FromSlash(rel), operatorConfig)
	}
	if row.state != "" {
		writeUnder(t, home, filepath.FromSlash(row.state), "{}")
	}
	return home
}

// assertEscapeLog reads what each escape attempt reported: every write and
// the key read denied, every own-config write denied, the agent's own data
// written, and the key never in its output.
func assertEscapeLog(t *testing.T, home string, row *escapeRow, log string) {
	t.Helper()
	want := []string{
		"zshrc=denied", "checkout=denied", "config=denied", "ssh=denied", "state=written", "othercred=denied",
	}
	for _, p := range homePaths(home, row.config) {
		want = append(want, "own-config "+p+" denied")
	}
	for _, line := range want {
		if !strings.Contains(log, line+"\n") {
			t.Errorf("want %q; escape log:\n%s", line, log)
		}
	}
	if strings.Contains(log, privateKeyCanary) {
		t.Errorf("the agent read the private key:\n%s", log)
	}
}

// assertConfigKept proves one config or plugin path is still the operator's
// own (planted) or still absent.
func assertConfigKept(t *testing.T, home, rel string, planted bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(rel)))
	if planted && string(raw) != operatorConfig || !planted && err == nil {
		t.Errorf("~/%s = %q (%v), want the operator's own or nothing", rel, raw, err)
	}
}

// assertHomeUntouched proves the operator's profile and planted config are
// unchanged, no new config or plugin file exists, ~/.config/evil was never
// made, and the agent's own data write landed.
func assertHomeUntouched(t *testing.T, home string, row *escapeRow) {
	t.Helper()
	if got := string(readFile(t, filepath.Join(home, ".zshrc"))); got != operatorProfile {
		t.Errorf("~/.zshrc = %q, want it unchanged", got)
	}
	for _, rel := range row.config {
		assertConfigKept(t, home, rel, slices.Contains(row.planted, rel))
	}
	if _, err := os.Lstat(filepath.Join(home, ".config", "evil")); !os.IsNotExist(err) {
		t.Errorf("~/.config/evil exists (err = %v), want the write denied", err)
	}
	if row.state == "" {
		return
	}
	if got := string(readFile(t, filepath.Join(home, filepath.FromSlash(row.state)))); got != "ok" {
		t.Errorf("the agent's own state = %q, want its write", got)
	}
}

// readFile reads one fixture file.
func readFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// siblingAttack is what a prompt-injected finder 1 runs from its working
// directory: for every snapshot directory beside its own wrapper — the
// execution's shared tree and finder 2's live view among them — it tries to
// plant a file, append to the shared tree's src/main.go, forge finder 2's
// review in the sibling's bundle copy, and list the directory; then it
// forges finder 2's review at the exact live path finder 2 started in
// (SIBLING_VIEW, `../../<its wrapper>/<its view>`). Each outcome is one line
// on stdout.
const siblingAttack = `own=$(basename "$(cd .. && pwd)")
for d in ../../verdict-snapshot-*; do
  n=$(basename "$d"); [ "$n" = "$own" ] && continue
  (printf forged > "$d/forged") 2>/dev/null && echo "written $n/forged" || echo "denied $n/forged"
  (printf forged >> "$d/src/main.go") 2>/dev/null && echo "written $n/src/main.go" || echo "denied $n/src/main.go"
  for v in "$d"/*/.verdict/state/42/x-finder-2; do
    (printf '{"forged":true}' > "$v/review.json") 2>/dev/null && echo "written sibling $v" || echo "denied sibling $v"
  done
  ls "$d" >/dev/null 2>&1 && echo "read $n" || echo "noread $n"
done
(printf '{"forged":true}' > "$SIBLING_VIEW/.verdict/state/42/x-finder-2/review.json") 2>/dev/null &&
  echo "written exact sibling" || echo "denied exact sibling"
`

// siblingPhases runs a two-finder execution through the real review
// sandbox: finder 1's wrapper argv runs siblingAttack for real while finder
// 2 waits in its live view, then each phase writes its artifacts as the
// scripted phaseCommander does. A git call and the mechanism's probe run
// for real.
type siblingPhases struct {
	inner   *phaseCommander
	ready   chan struct{}
	live    string
	done    chan struct{}
	attack  string
	once    sync.Once
	readyMu sync.Once
}

// Run routes one command: git and the probe for real, a finder's attack
// through its own wrapper, then the phase's artifacts.
func (s *siblingPhases) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	if _, _, git := gitcmd.Split(cmd.Argv); git || isProbe(cmd.Argv) {
		return coreexec.Runner{}.Run(ctx, cmd)
	}
	at := agentAt(cmd.Argv)
	switch phase, _ := phaseOf(cmd); phase {
	case "x-finder-1":
		s.finderOne(ctx, cmd, at)
	case "x-finder-2":
		s.readyMu.Do(func() {
			s.live = cmd.Dir
			close(s.ready)
		})
		awaitClose(s.done)
	}
	inner := *cmd
	inner.Argv = cmd.Argv[at:]
	return s.inner.Run(ctx, &inner)
}

// finderOne waits for finder 2's view, then runs the attack through finder
// 1's own wrapper.
func (s *siblingPhases) finderOne(ctx context.Context, cmd *core.Command, at int) {
	defer s.once.Do(func() { close(s.done) })
	awaitClose(s.ready)
	attack := *cmd
	attack.Argv = append(slices.Clone(cmd.Argv[:at]), "/bin/sh", "-c", siblingAttack)
	attack.Env = append(slices.Clone(cmd.Env), "SIBLING_VIEW="+filepath.Join("..", "..",
		filepath.Base(filepath.Dir(s.live)), filepath.Base(s.live)))
	res, err := coreexec.Runner{}.Run(ctx, &attack)
	if err != nil {
		s.inner.t.Errorf("the attack did not run: %v", err)
	}
	s.attack = res.Stdout + res.Stderr
}

// awaitClose waits for ch to close, or gives up after a bound so a broken
// dispatch fails the test instead of hanging it.
func awaitClose(ch chan struct{}) {
	select {
	case <-ch:
	case <-time.After(30 * time.Second):
	}
}

// agentAt is the index of the agent's own argv inside a sandboxed launch:
// after sandbox-exec's profile, or after bwrap's `--`.
func agentAt(argv []string) int {
	if len(argv) > 2 && argv[0] == "/usr/bin/sandbox-exec" {
		return 3
	}
	if i := slices.Index(argv, "--"); filepath.Base(argv[0]) == "bwrap" && i >= 0 {
		return i + 1
	}
	return 0
}

// TestReviewSandboxKeepsFindersOutOfEachOther runs two finders through the
// real mechanism while finder 1 reaches through `../..` for the shared
// snapshot tree and finder 2's live view: every write is denied, macOS
// denies even listing them, and nothing forged comes back to the bundle.
func TestReviewSandboxKeepsFindersOutOfEachOther(t *testing.T) {
	t.Parallel()
	requireReviewSandbox(t)
	root := confinedRoot(t)
	dir := bundleDir(root)
	writeCapture(t, dir, `{"schema_version":"1"}`)
	writeUnder(t, dir, "PR.json", `{"headRefOid":"head"}`)
	writeUnder(t, dir, "procedure.md", "authoritative procedure")
	commander := &siblingPhases{
		inner: &phaseCommander{t: t, dir: dir}, ready: make(chan struct{}), done: make(chan struct{}),
	}
	req := request(root, commander)
	req.LookPath = hostLook("/opt/agents/claude")
	req.RoundsRaw, req.FindersRaw = "1", "2"
	req.Environ = append(req.Environ, "HOME="+t.TempDir(), "PATH="+os.Getenv("PATH"))
	if _, ref := TwoFinders(t.Context(), req); ref != nil {
		t.Fatalf("two finders refused: %v", ref)
	}
	assertSiblingAttackDenied(t, commander.attack, hiddenBase(commander.live))
	if raw := readPhaseBytes(t, filepath.Join(dir, "x-finder-2", reviewFile)); strings.Contains(string(raw), "forged") {
		t.Error("finder 1's forgery reached finder 2's review")
	}
	assertCheckoutUntouched(t, root)
}

// assertSiblingAttackDenied reads the attack's outcomes: it reached at least
// the shared tree and finder 2's view, wrote nothing, reached finder 2's
// bundle copy on Linux (where it stays readable), and listed nothing on
// macOS.
func assertSiblingAttackDenied(t *testing.T, log string, hidden bool) {
	t.Helper()
	if strings.Contains(log, "written") || !strings.Contains(log, "denied exact sibling\n") {
		t.Errorf("finder 1 wrote outside its own view, or never tried finder 2's live path:\n%s", log)
	}
	if hidden {
		// bwrap's tmpfs hides the whole snapshot directory but the launch's
		// own wrapper: the attack sees no sibling to try.
		if strings.Contains(log, "/forged") {
			t.Errorf("finder 1 saw another snapshot directory in a hidden temp root:\n%s", log)
		}
		return
	}
	if strings.Count(log, "/forged") < 2 {
		t.Fatalf("the attack reached fewer than two snapshot directories:\n%s", log)
	}
	assertSiblingReads(t, log)
}

// hiddenBase reports whether the snapshot directory a live view sits in lies
// in a temp root bwrap hides on Linux.
func hiddenBase(live string) bool {
	return runtime.GOOS == "linux" && inside(filepath.Dir(filepath.Dir(live)), hiddenRoots(lstatMode))
}

// assertSiblingReads checks what each platform lets the attack read: macOS
// lists no other snapshot directory, and Linux, where they stay readable,
// reached finder 2's bundle copy, so the forgery there was really tried.
func assertSiblingReads(t *testing.T, log string) {
	t.Helper()
	listed := strings.HasPrefix(log, "read ") || strings.Contains(log, "\nread ")
	if runtime.GOOS == "darwin" && listed {
		t.Errorf("finder 1 listed another snapshot directory:\n%s", log)
	}
	if runtime.GOOS == "linux" && !strings.Contains(log, "/.verdict/state/42/x-finder-2\n") {
		t.Errorf("finder 1 never reached finder 2's bundle copy, so the forgery was not tried:\n%s", log)
	}
}

// sandboxedRun launches script as pi through the real review sandbox, with
// HOME and the extra variables in env, and returns the response, the
// refusal, and what the launches printed.
func sandboxedRun(t *testing.T, root, home, script string, env ...string,
) (*core.RunResponse, *core.RefusalError, string) {
	t.Helper()
	return sandboxedRunAs(t, "pi", root, home, script, "1", env...)
}

// sandboxedRunAs is sandboxedRun as another row.
func sandboxedRunAs(t *testing.T, agent, root, home, script, rounds string, env ...string,
) (*core.RunResponse, *core.RefusalError, string) {
	t.Helper()
	return sandboxedRunWith(t, agent, root, home, script, rounds, nil, env...)
}

// sandboxedRunWith is sandboxedRunAs with the operator's `--` arguments.
func sandboxedRunWith(t *testing.T, agent, root, home, script, rounds string, pass []string, env ...string,
) (*core.RunResponse, *core.RefusalError, string) {
	t.Helper()
	bin := plantStub(t, agent, script)
	host := &capturedHost{}
	req := request(root, host)
	req.Agent, req.RoundsRaw, req.PassThrough = agent, rounds, pass
	req.LookPath = hostLook(bin)
	req.Environ = append(append(req.Environ, "HOME="+home, "PATH="+os.Getenv("PATH")), env...)
	resp, ref := Run(t.Context(), req)
	return resp, ref, host.output()
}

// swapScript is a prompt-injected claude across two rounds. Round 1 tries to
// swap its own data directory for a link to HOME and owes an invalid review,
// so a repair round follows; round 2 (the repair directive) writes through
// the link it hoped for and straight into HOME, then the review.
const swapScript = "#!/bin/sh\n" +
	"data=\"$HOME/.claude/debug\"\n" +
	"case \"$*\" in\n" +
	"*'Round 2/2:'*)\n" +
	"  printf evil >> \"$data/.zshrc\" 2>/dev/null; printf evil >> \"$HOME/.zshrc\" 2>/dev/null\n" +
	"  printf '%s' '" + reviewDoc + "' > .verdict/state/42/review.json ;;\n" +
	"*)\n" +
	"  /bin/rm -rf \"$data\" 2>/dev/null; /bin/ln -s \"$HOME\" \"$data\" 2>/dev/null\n" +
	"  printf '{}' > .verdict/state/42/review.json ;;\n" +
	"esac\n"

// TestReviewSandboxSurvivesAStateDirSwap proves a round-1 agent cannot turn
// its own data directory into a link to HOME for round 2: the swap is
// refused where it happens (the directory's own entry is pinned on macOS,
// a mount point on Linux), and a link that got there anyway is refused
// before the next launch. Either way HOME's profile is unchanged.
func TestReviewSandboxSurvivesAStateDirSwap(t *testing.T) {
	t.Parallel()
	requireReviewSandbox(t)
	root := confinedRoot(t)
	home := t.TempDir()
	writeUnder(t, home, ".zshrc", operatorProfile)
	if err := os.MkdirAll(filepath.Join(home, ".claude", "debug"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, ref, _ := sandboxedRunAs(t, "claude", root, home, swapScript, "2")
	if got := string(readFile(t, filepath.Join(home, ".zshrc"))); got != operatorProfile {
		t.Errorf("~/.zshrc = %q (refusal %v), want it unchanged", got, ref)
	}
}

// TestReviewSandboxStateLinkRefusesTheNextLaunch proves the per-launch check
// on its own: a data directory replaced by a link to HOME after the box was
// settled refuses the next wrap.
func TestReviewSandboxStateLinkRefusesTheNextLaunch(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	sessions := filepath.Join(home, ".claude", "debug")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	def, _ := lookupAgent("claude")
	box, err := newReviewBox(&def, []string{"HOME=" + home}, nil)
	if err != nil {
		t.Fatal(err)
	}
	box.mechanism, box.runner = "bwrap", "bwrap"
	if err := os.Remove(sessions); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(home, sessions); err != nil {
		t.Fatal(err)
	}
	if _, err := box.wrap(t.TempDir(), "", []string{"pi"}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("wrap = %v, want the swapped-in link refused", err)
	}
}

// TestReviewSandboxRefusesALinkedStatePath proves a state path that is
// already a link — a dotfile manager's — refuses when the box is settled,
// since the sandbox would otherwise open whatever it points at.
func TestReviewSandboxRefusesALinkedStatePath(t *testing.T) {
	t.Parallel()
	home, elsewhere := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(home, ".claude", "debug")); err != nil {
		t.Fatal(err)
	}
	def, _ := lookupAgent("claude")
	if _, err := newReviewBox(&def, []string{"HOME=" + home}, nil); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("newReviewBox = %v, want the linked state path refused", err)
	}
}

// socketScript tries to talk to the unix socket SOCK names from inside the
// sandbox and prints the outcome.
const socketScript = "#!/bin/sh\n" +
	"{ printf ping | nc -U -w 2 \"$SOCK\" && echo socket=connected || echo socket=denied; } 2>&1\n" +
	"printf '%s' '" + reviewDoc + "' > .verdict/state/42/review.json\n"

// listenUnix listens on a fresh unix socket outside any wrapper and reports
// each connection it accepts.
func listenUnix(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	// A short path: unix socket names are capped near 100 bytes, which a
	// t.TempDir path can pass.
	dir, err := os.MkdirTemp("", "vs") //nolint:usetesting // see above
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan struct{}, 1)
	go func() {
		if conn, err := listener.Accept(); err == nil {
			accepted <- struct{}{}
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() { _ = listener.Close() })
	return sock, accepted
}

// TestReviewSandboxDeniesUnixSockets proves a review agent cannot connect to
// a unix socket outside its wrapper — an ssh agent, docker, or a systemd bus
// stand-in the test listens on — and the listener sees no connection.
func TestReviewSandboxDeniesUnixSockets(t *testing.T) {
	t.Parallel()
	requireReviewSandbox(t)
	if _, err := exec.LookPath("nc"); err != nil {
		t.Skipf("nc is not installed: %v", err)
	}
	sock, accepted := listenUnix(t)
	_, ref, log := sandboxedRun(t, confinedRoot(t), t.TempDir(), socketScript, "SOCK="+sock)
	if ref != nil {
		t.Fatalf("review refused: %v", ref)
	}
	select {
	case <-accepted:
		t.Error("the agent connected to a unix socket outside its sandbox")
	default:
	}
	if !strings.Contains(log, "socket=denied") {
		t.Errorf("socket log = %q, want the connect denied", log)
	}
}

// opencodeStartScript is opencode's first run: in its ephemeral data home
// it makes repos, a snapshot repository's config, and its database with
// journal, WAL, and shared-memory files, and reads the seeded copy of its
// login; it makes its cache's bin; and a write into its real data or state
// directory — the shadow repositories a later session's git trusts — is
// denied, as is a read of its real login.
const opencodeStartScript = "#!/bin/sh\n" +
	"d=\"$XDG_DATA_HOME/opencode\"; real=\"$HOME/.local/share/opencode\"\n" +
	"{ case \"$d\" in \"$HOME\"/*) echo data=real ;; *) echo data=ephemeral ;; esac\n" +
	"  /bin/mkdir -p \"$d/repos\" \"$d/snapshot/x\" && printf x > \"$d/snapshot/x/config\" && echo repos=made " +
	"|| echo repos=failed\n" +
	"  /bin/mkdir -p \"$HOME/.cache/opencode/bin\" && echo cachebin=made || echo cachebin=failed\n" +
	"  for f in opencode.db opencode.db-journal opencode.db-wal opencode.db-shm; do " +
	"printf x > \"$d/$f\" && echo \"$f=written\" || echo \"$f=failed\"; done\n" +
	"  /bin/cat \"$d/auth.json\" && echo; echo seeded\n" +
	"  /bin/cat \"$real/auth.json\" 2>/dev/null | /usr/bin/grep -q opencode-login && echo realauth=read " +
	"|| echo realauth=hidden\n" +
	"  /bin/mkdir -p \"$real/snapshot/x\" 2>/dev/null; printf evil > \"$real/snapshot/x/config\" && echo realsnap=written " +
	"|| echo realsnap=denied\n" +
	"  printf evil > \"$XDG_STATE_HOME/x\" && echo state=written || echo state=failed\n" +
	"  printf evil > \"$HOME/.local/state/opencode/x\" && echo realstate=written || echo realstate=denied\n" +
	"  printf evil > \"$HOME/.cache/opencode/bin/x\" && echo cachebinfile=written || echo cachebinfile=denied\n" +
	"} 2>&1\n" +
	"printf '%s' '" + reviewDoc + "' > .verdict/state/42/review.json\n"

// opencodeLogin is the planted login the ephemeral home is seeded from.
const opencodeLogin = `{"opencode-login":"seed"}`

// TestReviewSandboxStartsOpencodeEphemeral proves opencode runs in an
// ephemeral data and state home in its wrapper, seeded with a copy of its
// login: its database, journal, WAL, repos, and snapshot repositories land
// there; its real data and state directories, which a later session's git
// trusts, stay unwritable and empty of them; its real login stays hidden;
// and the cache's bin it makes at startup exists.
func TestReviewSandboxStartsOpencodeEphemeral(t *testing.T) {
	t.Parallel()
	requireReviewSandbox(t)
	home := t.TempDir()
	writeUnder(t, home, filepath.Join(".local", "share", "opencode", "auth.json"), opencodeLogin)
	_, ref, log := sandboxedRunAs(t, "opencode", confinedRoot(t), home, opencodeStartScript, "1")
	if ref != nil {
		t.Fatalf("review refused: %v", ref)
	}
	for _, want := range []string{
		"data=ephemeral", "repos=made", "cachebin=made", "opencode.db=written", "opencode.db-journal=written",
		"opencode.db-wal=written", "opencode.db-shm=written", opencodeLogin, "realauth=hidden", "realsnap=denied",
		"state=written", "realstate=denied", "cachebinfile=denied",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("start log lacks %q:\n%s", want, log)
		}
	}
	if _, err := os.Lstat(filepath.Join(home, ".local", "share", "opencode", "snapshot")); !os.IsNotExist(err) {
		t.Errorf("the real data directory holds a snapshot (err = %v)", err)
	}
}

// claudeProjectScript is a prompt-injected claude reaching for project
// state: it writes a transcript into its own project directory (allowed),
// then tries its own memory, another project's memory, a staging directory
// beside them, and renaming its own project directory away to swap one in.
const claudeProjectScript = "#!/bin/sh\n" +
	"p=\"$HOME/.claude/projects\"; own=\"$p/$(pwd -P | /usr/bin/sed 's/[^A-Za-z0-9]/-/g')\"\n" +
	"{ printf t > \"$own/t.jsonl\" && echo transcript=written || echo transcript=failed\n" +
	"  printf evil > \"$own/memory/MEMORY.md\" && echo ownmemory=written || echo ownmemory=denied\n" +
	"  printf evil > \"$p/other/memory/MEMORY.md\" && echo othermemory=written || echo othermemory=denied\n" +
	"  /bin/mkdir \"$p/stage\" && echo stage=made || echo stage=denied\n" +
	"  /bin/mv \"$own\" \"$p/moved\" && echo rename=done || echo rename=denied\n" +
	"  /bin/mv \"$own/memory\" \"$own/m2\" && echo memoryrename=done || echo memoryrename=denied\n" +
	"} 2>&1\n" +
	"printf '%s' '" + reviewDoc + "' > .verdict/state/42/review.json\n"

// TestReviewSandboxScopesClaudesProjectDir proves claude writes only its own
// launch's project directory — made before the launch and removed after —
// and never its memory, another project's, or ~/.claude/projects itself:
// renaming its directory away to swap one in is denied.
func TestReviewSandboxScopesClaudesProjectDir(t *testing.T) {
	t.Parallel()
	requireReviewSandbox(t)
	home := t.TempDir()
	other := filepath.Join(".claude", "projects", "other", "memory", "MEMORY.md")
	writeUnder(t, home, other, operatorConfig)
	_, ref, log := sandboxedRunAs(t, "claude", confinedRoot(t), home, claudeProjectScript, "1")
	if ref != nil {
		t.Fatalf("review refused: %v", ref)
	}
	for _, want := range []string{
		"transcript=written", "ownmemory=denied", "othermemory=denied", "stage=denied", "rename=denied",
		"memoryrename=denied",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("project log lacks %q:\n%s", want, log)
		}
	}
	if got := string(readFile(t, filepath.Join(home, other))); got != operatorConfig {
		t.Errorf("another project's memory = %q, want it unchanged", got)
	}
	entries, err := os.ReadDir(filepath.Join(home, ".claude", "projects"))
	if err != nil || len(entries) != 1 || entries[0].Name() != "other" {
		t.Errorf("projects after the run = %v (%v), want the launch's own removed", entries, err)
	}
}

// codexMCPScript stands in for codex starting the operator's MCP servers:
// the real CLI runs every [mcp_servers] command its CODEX_HOME config names,
// outside every sandbox, so the stand-in runs the one it finds there.
const codexMCPScript = "#!/bin/sh\n" +
	"cfg=\"$CODEX_HOME/config.toml\"; /bin/cat \"$cfg\"\n" +
	"if /usr/bin/grep -q '^\\[mcp_servers' \"$cfg\" 2>/dev/null; then /usr/bin/touch \"$MCP_MARKER\"; fi\n" +
	"printf '%s' '" + reviewDoc + "' > .verdict/state/42/review.json\n"

// TestCodexStartsNoOperatorMCPServer proves a codex review launch starts
// none of the operator's MCP servers: its throwaway CODEX_HOME holds the
// operator's model and provider settings and nothing else.
func TestCodexStartsNoOperatorMCPServer(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeUnder(t, home, filepath.Join(".codex", "config.toml"),
		"model = \"m\"\n\n[mcp_servers.probe]\ncommand = \"/bin/sh\"\nargs = [\"-c\", \"evil\"]\n")
	marker := filepath.Join(t.TempDir(), "mcp-started")
	_, ref, log := sandboxedRunAs(t, "codex", confinedRoot(t), home, codexMCPScript, "1", "MCP_MARKER="+marker)
	if ref != nil {
		t.Fatalf("review refused: %v", ref)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("the operator's MCP server started (err = %v):\n%s", err, log)
	}
	if !strings.Contains(log, `model = "m"`) {
		t.Errorf("the operator's model did not reach codex:\n%s", log)
	}
}
