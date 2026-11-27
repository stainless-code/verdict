//go:build darwin || linux

package run

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stainless-code/verdict/internal/agentcatalog"
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
	path := filepath.Join(home, filepath.FromSlash(rel))
	if !planted {
		// Absence must be proven: an entry of any kind there, or one that
		// cannot be checked, fails.
		if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("~/%s exists or cannot be checked (%v), want nothing there", rel, err)
		}
		return
	}
	if raw, err := os.ReadFile(path); string(raw) != operatorConfig {
		t.Errorf("~/%s = %q (%v), want the operator's own", rel, raw, err)
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
	if !awaitClose(s.ready) {
		s.inner.t.Error("finder 2's view never became live: the attack did not run")
		return
	}
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

// awaitClose waits for ch to close, false when it gives up after a bound so
// a broken dispatch fails the test instead of hanging it; only a true return
// orders the reads after the close.
func awaitClose(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	case <-time.After(30 * time.Second):
		return false
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
	"  echo round2=ran\n" +
	"  printf evil >> \"$data/.zshrc\" 2>/dev/null; printf evil >> \"$HOME/.zshrc\" 2>/dev/null\n" +
	"  printf '%s' '" + reviewDoc + "' > .verdict/state/42/review.json ;;\n" +
	"*)\n" +
	"  /bin/rm -rf \"$data\" 2>/dev/null; /bin/ln -s \"$HOME\" \"$data\" 2>/dev/null\n" +
	"  echo swap=tried\n" +
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
	_, ref, log := sandboxedRunAs(t, "claude", root, home, swapScript, "2")
	if got := string(readFile(t, filepath.Join(home, ".zshrc"))); got != operatorProfile {
		t.Errorf("~/.zshrc = %q (refusal %v), want it unchanged", got, ref)
	}
	// The attack must have run: round 1 tried the swap, and then either the
	// repair round ran against the pinned directory or the swapped-in link
	// refused it.
	if !strings.Contains(log, "swap=tried") {
		t.Fatalf("round 1 never tried the swap (refusal %v):\n%s", ref, log)
	}
	refusedLink := ref != nil && strings.Contains(ref.Message, "symlink")
	if !refusedLink && (ref != nil || !strings.Contains(log, "round2=ran")) {
		t.Errorf("want the repair round to run or the swapped-in link refused; refusal %v:\n%s", ref, log)
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

// opencodeDBScript reads the logins in opencode's throwaway data home and
// names the database the launch points it at, then reaches for the real
// database, where its logins live.
const opencodeDBScript = "#!/bin/sh\n" +
	"real=\"$HOME/.local/share/opencode/opencode.db\"\n" +
	"{ /bin/cat \"$XDG_DATA_HOME/opencode/auth.json\" 2>/dev/null; echo\n" +
	"  case \"$OPENCODE_DB\" in \"$XDG_DATA_HOME\"/*) echo db=ephemeral ;; *) echo \"db=$OPENCODE_DB\" ;; esac\n" +
	"  /usr/bin/head -c 15 \"$real\" 2>/dev/null | /usr/bin/grep -q SQLite && echo realdb=read || echo realdb=hidden\n" +
	"} 2>&1\n" +
	"printf '%s' '" + reviewDoc + "' > .verdict/state/42/review.json\n"

// TestReviewSandboxSeedsOpencodesDatabaseLogins proves, under the real
// mechanism, that opencode's review launch reads the provider logins its
// database holds from its throwaway home and opens the throwaway database,
// while the real database — opencode's logins — stays hidden from it and
// from every other agent.
func TestReviewSandboxSeedsOpencodesDatabaseLogins(t *testing.T) {
	t.Parallel()
	requireReviewSandbox(t)
	for _, tc := range []struct {
		agent string
		leak  string
		want  []string
	}{
		{agent: "opencode", want: []string{"realdb=hidden", seededLogins, "db=ephemeral"}},
		{agent: "pi", want: []string{"realdb=hidden"}, leak: "an-key"},
	} {
		home := visibleTempDir(t)
		plantOpencodeDB(t, filepath.Join(home, ".local", "share"))
		_, ref, log := sandboxedRunAs(t, tc.agent, confinedRoot(t), home, opencodeDBScript, "1")
		if ref != nil {
			t.Fatalf("%s review refused: %v", tc.agent, ref)
		}
		assertLogHas(t, tc.agent, log, tc.want)
		if tc.leak != "" && strings.Contains(log, tc.leak) {
			t.Errorf("%s read opencode's logins:\n%s", tc.agent, log)
		}
	}
}

// assertLogHas fails for each wanted line an agent's log lacks.
func assertLogHas(t *testing.T, agent, log string, want []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(log, w) {
			t.Errorf("%s log lacks %q:\n%s", agent, w, log)
		}
	}
}

// lateDBScript waits on a FIFO in opencode's real config directory — the
// test opens it once the sandbox is up — then reads a database the test
// created in the real data directory after the launch started.
const lateDBScript = "#!/bin/sh\n" +
	"/bin/cat \"$HOME/.config/opencode/go.fifo\" >/dev/null\n" +
	"{ /usr/bin/head -c 6 \"$HOME/.local/share/opencode/opencode-beta.db\" 2>/dev/null | /usr/bin/grep -q LEAKED " +
	"&& echo late=read || echo late=hidden\n" +
	"} 2>&1\n" +
	"printf '%s' '" + reviewDoc + "' > .verdict/state/42/review.json\n"

// TestReviewSandboxHidesALateOpencodeDatabase proves, under the real
// mechanism, that a database opencode creates in its real data directory
// after a launch started — a channel build's, or the write-ahead log SQLite
// recreates — is unreadable to the launch: the whole directory is hidden,
// not the files that existed when the sandbox was built.
func TestReviewSandboxHidesALateOpencodeDatabase(t *testing.T) {
	t.Parallel()
	requireReviewSandbox(t)
	home := t.TempDir()
	plantOpencodeDB(t, filepath.Join(home, ".local", "share"))
	fifo := filepath.Join(home, ".config", "opencode", "go.fifo")
	if err := os.MkdirAll(filepath.Dir(fifo), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() {
		_, ref, log := sandboxedRunAs(t, "opencode", confinedRoot(t), home, lateDBScript, "1")
		if ref != nil {
			log += "\nrefused: " + ref.Message
		}
		done <- log
	}()
	w := openFIFOWriter(t, fifo, done)
	late := filepath.Join(home, ".local", "share", "opencode", "opencode-beta.db")
	if err := os.WriteFile(late, []byte("LEAKED"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	if log := <-done; !strings.Contains(log, "late=hidden") {
		t.Errorf("the late database was readable, or the launch never reached it:\n%s", log)
	}
}

// openFIFOWriter opens the FIFO for writing once the sandboxed reader has
// opened it, failing if the launch ends first or the wait runs past a
// minute.
func openFIFOWriter(t *testing.T, fifo string, done <-chan string) *os.File {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		f, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			return f
		}
		select {
		case log := <-done:
			t.Fatalf("the launch ended before it opened the FIFO:\n%s", log)
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatal("the launch never opened the FIFO")
	return nil
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

// TestLaunchProjectFailureLeavesNothingBehind proves a launch whose project
// setup fails after making the project directory still hands back what it
// made, so the cleanup registered before the error check removes it.
func TestLaunchProjectFailureLeavesNothingBehind(t *testing.T) {
	t.Parallel()
	box, projects := projectBox(t, "bad\x00name") // a name the filesystem refuses
	launch, err := box.launch(t.TempDir())
	if err == nil {
		t.Fatal("a project setup whose subdirectory cannot be made passed")
	}
	(&state{req: &core.RunRequest{}}).dropLaunch(launch)
	if entries, err := os.ReadDir(projects); err != nil || len(entries) != 0 {
		t.Errorf("projects after the failed setup = %v (%v), want the made directory removed", entries, err)
	}
}

// TestLaunchProjectRefusesAFailedCreation proves a project directory that
// cannot be made for a reason other than already existing refuses the
// launch rather than starting it without its project state.
func TestLaunchProjectRefusesAFailedCreation(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores the read-only bits the creation error needs")
	}
	box, projects := projectBox(t, "memory")
	if err := os.MkdirAll(projects, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(projects, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(projects, 0o700); err != nil {
			t.Log(err)
		}
	})
	if _, err := box.launch(t.TempDir()); err == nil {
		t.Error("a project directory that could not be made passed as granted")
	}
}

// projectBox is a box with claude's project state under a fresh HOME, its
// project directories' read-only subdirectory named readOnly.
func projectBox(t *testing.T, readOnly string) (*reviewBox, string) {
	t.Helper()
	home := t.TempDir()
	projects := filepath.Join(home, ".claude", "projects")
	return &reviewBox{
		home:     home,
		projects: &statePath{path: projects, base: home},
		project:  &agentcatalog.ProjectState{Root: ".claude/projects", ReadOnly: []string{readOnly}, MaxSlug: 200},
	}, projects
}

// TestOpencodeSeedWarnsOnAPermissionDeniedDatabase proves a database the
// operator cannot open (permission denied) warns and keeps the copied
// auth.json, rather than refusing a launch that copy could serve.
func TestOpencodeSeedWarnsOnAPermissionDeniedDatabase(t *testing.T) {
	t.Parallel()
	home, tmp := t.TempDir(), t.TempDir()
	data := filepath.Join(home, ".local", "share")
	plantOpencodeDB(t, data)
	denyRead(t, filepath.Join(data, "opencode", "opencode.db"))
	def, _ := lookupAgent("opencode")
	_, warnings, err := ephemeralEnv(&def, []string{"HOME=" + home}, tmp)
	if err != nil {
		t.Fatalf("a permission-denied database refused the launch: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "could not be read") ||
		!strings.Contains(warnings[0], "only the copy of auth.json") {
		t.Errorf("warnings = %q", warnings)
	}
	if got := readSeeded(t, filepath.Join(tmp, "home-xdg_data_home", "opencode", "auth.json")); string(got) != staleAuth {
		t.Errorf("auth.json = %s, want the copy", got)
	}
}

// linkedDBScript reads the database an OPENCODE_DB link names, from another
// agent's launch.
const linkedDBScript = "#!/bin/sh\n" +
	"{ /usr/bin/head -c 6 \"$REAL_DB\" 2>/dev/null | /usr/bin/grep -q LEAKED && echo target=read " +
	"|| echo target=hidden\n" +
	"} 2>&1\n" +
	"printf '%s' '" + reviewDoc + "' > .verdict/state/42/review.json\n"

// TestReviewSandboxHidesALinkedOpencodeDBTarget proves, under the real
// mechanism, that an OPENCODE_DB which is a link leaves the file it names —
// in a directory of its own, where SQLite also keeps its log — unreadable to
// another agent, not only the link's directory.
func TestReviewSandboxHidesALinkedOpencodeDBTarget(t *testing.T) {
	t.Parallel()
	requireReviewSandbox(t)
	home, store := t.TempDir(), visibleTempDir(t)
	target := filepath.Join(store, "real", "oc.db")
	writeUnder(t, store, filepath.Join("real", "oc.db"), "LEAKED")
	link := filepath.Join(store, "link", "oc.db")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	_, ref, log := sandboxedRunAs(t, "pi", confinedRoot(t), home, linkedDBScript, "1",
		"OPENCODE_DB="+link, "REAL_DB="+target)
	if ref != nil {
		t.Fatalf("review refused: %v", ref)
	}
	if !strings.Contains(log, "target=hidden") {
		t.Errorf("the linked database's target was readable, or the check never ran:\n%s", log)
	}
}

// denyRead makes path unreadable for the test, skipping where the user reads
// a mode-0 file anyway (root).
func denyRead(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(path, 0o600); err != nil {
			t.Error(err)
		}
	})
	if f, err := os.Open(path); err == nil {
		_ = f.Close()
		t.Skip("this user reads a mode-0 file (root); the denial cannot be staged")
	}
}

// firstRunScript waits on a FIFO in pi's own read-only directory — the test
// opens it once the sandbox is up — then reads a sentinel in the home and
// the database a host opencode wrote on its first run, data root and all.
const firstRunScript = "#!/bin/sh\n" +
	"/bin/cat \"$HOME/.pi/go.fifo\" >/dev/null\n" +
	"{ /bin/cat \"$HOME/sentinel\" 2>/dev/null | /usr/bin/grep -q seen && echo home=visible " +
	"|| echo home=hidden\n" +
	"  /usr/bin/head -c 6 \"$HOME/.local/share/opencode/opencode.db\" 2>/dev/null | /usr/bin/grep -q LEAKED " +
	"&& echo firstrun=read || echo firstrun=hidden\n" +
	"} 2>&1\n" +
	"printf '%s' '" + reviewDoc + "' > .verdict/state/42/review.json\n"

// TestReviewSandboxHidesAFirstRunOpencodeDataDir proves, under the real
// mechanism, that opencode's data directory — and its data root
// (~/.local/share), both absent when a launch starts — stays hidden when a
// host opencode makes them and writes its database during the launch: on
// Linux verdict makes the missing chain first so bwrap can hide it. The home
// sits outside the hidden temp roots (visibleTempDir), so a leak would
// show.
func TestReviewSandboxHidesAFirstRunOpencodeDataDir(t *testing.T) {
	t.Parallel()
	requireReviewSandbox(t)
	home := visibleTempDir(t)
	writeUnder(t, home, "sentinel", "seen")
	fifo := filepath.Join(home, ".pi", "go.fifo")
	if err := os.MkdirAll(filepath.Dir(fifo), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan string, 1)
	go func() {
		_, ref, log := sandboxedRunAs(t, "pi", confinedRoot(t), home, firstRunScript, "1")
		if ref != nil {
			log += "\nrefused: " + ref.Message
		}
		done <- log
	}()
	w := openFIFOWriter(t, fifo, done)
	writeUnder(t, home, filepath.Join(".local", "share", "opencode", "opencode.db"), "LEAKED")
	_ = w.Close()
	log := <-done
	if strings.Contains(log, "home=hidden") {
		t.Skipf("the test home sits in a hidden temp root, so no late write could leak:\n%s", log)
	}
	if !strings.Contains(log, "firstrun=hidden") {
		t.Errorf("a first-run opencode database was readable, or the launch never reached it:\n%s", log)
	}
}

// TestReviewSandboxHidesADanglingOpencodeDBTarget proves, under the real
// mechanism, that a database link whose target does not exist when a launch
// starts hides the directory the kernel will create it in — a direct link,
// a chain of links, the default opencode.db as a relative `..` link in a
// linked data directory, and a data directory that is itself a dangling
// link — so the database a host opencode creates there
// during the launch is unreadable to another agent.
func TestReviewSandboxHidesADanglingOpencodeDBTarget(t *testing.T) {
	t.Parallel()
	requireReviewSandbox(t)
	for name, layout := range map[string]func(t *testing.T, home, store string) (env []string, target string){
		"direct": func(t *testing.T, _, store string) ([]string, string) {
			t.Helper()
			target := filepath.Join(store, "real", "oc.db")
			return []string{"OPENCODE_DB=" + danglingLink(t, store, target)}, target
		},
		"chain": func(t *testing.T, _, store string) ([]string, string) {
			t.Helper()
			target := filepath.Join(store, "y", "oc.db")
			b := danglingLink(t, filepath.Join(store, "b"), target)
			return []string{"OPENCODE_DB=" + danglingLink(t, filepath.Join(store, "a"), b)}, target
		},
		"default relative": func(t *testing.T, home, store string) ([]string, string) {
			t.Helper()
			disk := filepath.Join(store, "disk", "oc")
			for _, dir := range []string{
				disk, filepath.Join(store, "disk", "store"),
				filepath.Join(home, ".local", "share"),
			} {
				mustMkdir(t, dir)
			}
			mustSymlink(t, disk, filepath.Join(home, ".local", "share", "opencode"))
			mustSymlink(t, filepath.Join("..", "store", "opencode.db"), filepath.Join(disk, "opencode.db"))
			return nil, filepath.Join(store, "disk", "store", "opencode.db")
		},
		"dangling data dir": func(t *testing.T, home, store string) ([]string, string) {
			t.Helper()
			mustMkdir(t, filepath.Join(home, ".local", "share"))
			elsewhere := filepath.Join(store, "elsewhere", "oc")
			mustSymlink(t, elsewhere, filepath.Join(home, ".local", "share", "opencode"))
			return nil, filepath.Join(elsewhere, "opencode.db")
		},
	} {
		home, store := t.TempDir(), visibleTempDir(t)
		env, target := layout(t, home, store)
		log := lateWriteRun(t, home, target, append(env, "REAL_DB="+target)...)
		if !strings.Contains(log, "target=hidden") {
			t.Errorf("%s: the database created at a dangling link's target was readable:\n%s", name, log)
		}
	}
}

// lateWriteRun launches pi, waits for it to open a FIFO in its own read-only
// directory, writes LEAKED at target, and returns the launch's log, the
// stand-in reading REAL_DB.
func lateWriteRun(t *testing.T, home, target string, env ...string) string {
	t.Helper()
	fifo := filepath.Join(home, ".pi", "go.fifo")
	makeFIFO(t, fifo)
	done := make(chan string, 1)
	go func() {
		_, ref, log := sandboxedRunAs(t, "pi", confinedRoot(t), home, "#!/bin/sh\n/bin/cat \"$HOME/.pi/go.fifo\" "+
			">/dev/null\n"+linkedDBScript[len("#!/bin/sh\n"):], "1", env...)
		if ref != nil {
			log += "\nrefused: " + ref.Message
		}
		done <- log
	}()
	w := openFIFOWriter(t, fifo, done)
	mustMkdir(t, filepath.Dir(target))
	if err := os.WriteFile(target, []byte("LEAKED"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	return <-done
}

// mustMkdir makes dir and its parents.
func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
}

// mustSymlink makes link pointing at target.
func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// visibleTempDir is a test directory the review sandbox does not already
// hide wholesale: t.TempDir(), unless it sits in a hidden temp root (Linux's
// /tmp), then one under the user cache directory, removed after. A test of
// what the sandbox hides would otherwise pass with or without the fix. It
// skips, saying why, when there is no such directory.
func visibleTempDir(t *testing.T) string {
	t.Helper()
	if dir := t.TempDir(); !inHiddenRoot(dir) {
		return dir
	}
	if dir := cacheTempDir(t); dir != "" && !inHiddenRoot(dir) {
		return dir
	}
	t.Skip("no test directory outside the review sandbox's hidden roots, so nothing here could leak")
	return ""
}

// cacheTempDir is a new directory under the user cache directory, removed
// after the test; "" when none can be made.
func cacheTempDir(t *testing.T) string {
	t.Helper()
	base, err := os.UserCacheDir()
	if err != nil || os.MkdirAll(base, 0o700) != nil {
		return ""
	}
	dir, err := os.MkdirTemp(base, "verdict-sandbox-test-") //nolint:usetesting // t.TempDir sits in the hidden root
	if err != nil {
		return ""
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// inHiddenRoot reports whether dir, resolved, sits in a root the review
// sandbox hides whole on this host.
func inHiddenRoot(dir string) bool {
	roots := bwrapHiddenRoots
	if runtime.GOOS == "darwin" {
		roots = seatbeltTempRoots
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		resolved = dir
	}
	return slices.ContainsFunc(hostsandbox.ExpandRealpaths(roots), func(root string) bool {
		return hostsandbox.Within(resolved, root)
	})
}

// danglingLink makes target's directory, empty, and a link to target in a
// directory of its own under base.
func danglingLink(t *testing.T, base, target string) string {
	t.Helper()
	link := filepath.Join(base, "link", "oc.db")
	for _, dir := range []string{filepath.Dir(target), filepath.Dir(link)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	return link
}

// makeFIFO makes a FIFO at path, and its directory.
func makeFIFO(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestMakeHiddenDirRefusesAnUnwritableRoot proves a missing credential
// directory that cannot be made — its data root is read-only — refuses,
// naming the directory and the fix, rather than launching it unhidden.
func TestMakeHiddenDirRefusesAnUnwritableRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(locked, 0o700); err != nil {
			t.Error(err)
		}
	})
	if f, err := os.Create(filepath.Join(locked, "probe")); err == nil {
		_ = f.Close()
		t.Skip("this user writes a read-only directory (root); the refusal cannot be staged")
	}
	dir := filepath.Join(locked, "share", "opencode")
	err := makeHiddenDir(dir)
	if err == nil || !strings.Contains(err.Error(), "must hide "+dir) || !strings.Contains(err.Error(), "create "+dir) {
		t.Fatalf("an unwritable root = %v, want a refusal naming %s", err, dir)
	}
}

// TestReviewSandboxPolicyRefusalNeverRunsUnsandboxed proves, under the real
// mechanism on Linux, that a credential directory the sandbox cannot make —
// opencode's data root a file, or read-only (refused only once the launch
// tries to make it), the directory missing — refuses claude, a
// row that confines its own writes, typed review-sandbox-unavailable before
// anything spawns, never running it unsandboxed with a warning; and codex
// when its `--` arguments give it verdict's sandbox.
func TestReviewSandboxPolicyRefusalNeverRunsUnsandboxed(t *testing.T) {
	t.Parallel()
	requireReviewSandbox(t)
	if runtime.GOOS != "linux" {
		t.Skip("a credential directory is made only before a bwrap launch")
	}
	for _, tc := range []struct {
		agent, layout string
		pass          []string
	}{
		{agent: "claude", layout: "file"},
		{agent: "claude", layout: "read-only"},
		{agent: "codex", layout: "file", pass: []string{"--sandbox", "danger-full-access"}},
		{agent: "codex", layout: "read-only", pass: []string{"--sandbox", "danger-full-access"}},
	} {
		t.Run(tc.agent+"/"+tc.layout, func(t *testing.T) { assertPolicyRefused(t, tc.agent, tc.layout, tc.pass) })
	}
}

// assertPolicyRefused runs agent over an unmakeable data directory and
// checks the launch refuses on the policy and spawns nothing.
func assertPolicyRefused(t *testing.T, agent, layout string, pass []string) {
	t.Helper()
	home := t.TempDir()
	stageUnmakeableDataDir(t, home, layout)
	resp, ref, log := sandboxedRunWith(t, agent, confinedRoot(t), home, "#!/bin/sh\necho spawned=yes\n", "1", pass)
	if ref == nil || ref.Code != codeReviewSandboxUnavailable || !strings.Contains(ref.Message, "policy refused") {
		t.Errorf("refusal = %v (posture %q), want the policy refused", ref, postureOf(resp))
	}
	if strings.Contains(log, "spawned=yes") {
		t.Errorf("spawned before the refusal:\n%s", log)
	}
}

// stageUnmakeableDataDir leaves opencode's data directory missing below a
// data root the sandbox cannot make it in: a file, or a read-only
// directory (skipped for a user who writes one anyway, root).
func stageUnmakeableDataDir(t *testing.T, home, layout string) {
	t.Helper()
	share := filepath.Join(home, ".local", "share")
	if layout == "file" {
		writeUnder(t, home, filepath.Join(".local", "share"), "a file where the data root goes")
		return
	}
	mustMkdir(t, share)
	if err := os.Chmod(share, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(share, 0o700); err != nil {
			t.Error(err)
		}
	})
	if f, err := os.Create(filepath.Join(share, "probe")); err == nil {
		_ = f.Close()
		t.Skip("this user writes a read-only directory (root); the refusal cannot be staged")
	}
}

// TestReviewSandboxLinkedDataRootStaysSandboxed proves, under the real
// mechanism on Linux, that a data root that is a link — ~/.local/share on
// another disk, opencode's directory missing — keeps claude sandboxed: the
// directory is made at the link's target and hidden, never a refusal the
// probe turns into an unsandboxed run.
func TestReviewSandboxLinkedDataRootStaysSandboxed(t *testing.T) {
	t.Parallel()
	requireReviewSandbox(t)
	if runtime.GOOS != "linux" {
		t.Skip("a credential directory is made only before a bwrap launch")
	}
	home, disk := t.TempDir(), t.TempDir()
	mustMkdir(t, filepath.Join(home, ".local"))
	mustSymlink(t, disk, filepath.Join(home, ".local", "share"))
	resp, ref, _ := sandboxedRunAs(t, "claude", confinedRoot(t), home, "#!/bin/sh\nprintf '%s' '"+reviewDoc+
		"' > .verdict/state/42/review.json\n", "1")
	if ref != nil || postureOf(resp) != postureSandboxed {
		t.Fatalf("refusal = %v, posture %q; want a sandboxed review", ref, postureOf(resp))
	}
	if info, err := os.Lstat(filepath.Join(disk, "opencode")); err != nil || !info.IsDir() {
		t.Errorf("opencode's directory was not made at the link's target (%v)", err)
	}
}

// postureOf is a response's sandbox posture, "" for none.
func postureOf(resp *core.RunResponse) string {
	if resp == nil || resp.Data == nil || resp.Data.Sandbox == nil {
		return ""
	}
	return resp.Data.Sandbox.Posture
}
