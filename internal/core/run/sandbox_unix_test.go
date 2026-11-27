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
)

// hostStub is the real watchdog behind a plain wrapper, which carries no
// simulation marker, so the launch goes through the real review sandbox as
// any production wrapper's would. The sandboxed launch runs the binary the
// resolution found, so the planted stand-in needs no PATH entry.
type hostStub struct{}

// Run runs the command for real.
func (hostStub) Run(ctx context.Context, cmd *core.Command) (core.Result, error) {
	return coreexec.Watchdog{}.Run(ctx, cmd)
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
// attempt's outcome lands in a log under the agent's own data directory
// (DATA_DIR), which the sandbox keeps writable; the review is written where
// it is owed.
const escapeScript = "#!/bin/sh\n" +
	"log=\"$DATA_DIR/escape.log\"\n" +
	"{ printf 'evil\\n' >> \"$HOME/.zshrc\" && echo zshrc=written || echo zshrc=denied\n" +
	"  printf 'evil\\n' > \"$CHECKOUT_FILE\" && echo checkout=written || echo checkout=denied\n" +
	"  /bin/mkdir -p \"$HOME/.config/evil\" && echo config=written || echo config=denied\n" +
	"  /bin/cat \"$HOME/.ssh/id_rsa\" && echo ssh=read || echo ssh=denied\n" +
	"  printf ok > \"$DATA_DIR/own-state\" && echo state=written || echo state=denied\n" +
	"  /bin/cat \"$OTHER_CRED\" && echo othercred=read || echo othercred=denied\n" +
	"  IFS=:; for p in $CONFIG_PATHS; do /bin/mkdir -p \"${p%/*}\" 2>/dev/null; " +
	"printf evil > \"$p\" && echo \"own-config $p written\" || echo \"own-config $p denied\"; done\n" +
	"} > \"$log\" 2>&1\n" +
	"printf '%s' '" + reviewDoc + "' > .verdict/state/42/review.json\n"

// privateKeyCanary is the planted key's content, which must never reach the
// agent.
const privateKeyCanary = "-----BEGIN CANARY PRIVATE KEY----- verdict-review-sandbox"

// escapeRow is one agent's own layout: the data directory it may write and
// the config and code paths a later run would load, which it may not —
// existing ones planted first, so a denied write leaves them unchanged.
type escapeRow struct {
	agent     string
	data      string
	otherCred string
	config    []string
	planted   []string
}

// escapeRows are the rows with state paths, each with config and plugin
// paths from its read-only list.
var escapeRows = []escapeRow{
	{
		agent: "pi", data: ".pi/agent/sessions", otherCred: ".codex/auth.json",
		config:  []string{".pi/agent/extensions/x.ts", ".pi/agent/settings.json", ".pi/agent/AGENTS.md"},
		planted: []string{".pi/agent/settings.json"},
	},
	{
		agent: "opencode", data: ".local/share/opencode", otherCred: ".pi/agent/auth.json",
		config: []string{
			".config/opencode/plugin/x.js", ".config/opencode/opencode.json",
			".cache/opencode/node_modules/x/index.js", ".local/share/opencode/bin/x",
			".local/share/opencode/node_modules/x/index.js", ".local/share/opencode/shell/x",
		},
		planted: []string{".config/opencode/opencode.json"},
	},
	{
		agent: "claude", data: ".claude/projects", otherCred: ".local/share/opencode/auth.json",
		config: []string{
			".claude/settings.json", ".claude.json", ".claude/hooks/x.sh", ".claude/plugins/x/plugin.json",
			".claude/shell-snapshots/snapshot-zsh.sh", ".claude/session-env/x",
			".claude/projects/p/memory/MEMORY.md",
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
	bin := plantStub(t, row.agent, escapeScript)
	req := request(root, hostStub{})
	req.Agent = row.agent
	req.LookPath = func(string) (string, error) { return bin, nil }
	req.Environ = append(req.Environ, "HOME="+home, "PATH="+os.Getenv("PATH"),
		"CHECKOUT_FILE="+filepath.Join(root, "src", "main.go"), "DATA_DIR="+filepath.Join(home, row.data),
		"CONFIG_PATHS="+strings.Join(homePaths(home, row.config), ":"),
		"OTHER_CRED="+filepath.Join(home, filepath.FromSlash(row.otherCred)))
	resp, ref := Run(t.Context(), req)
	if ref != nil {
		t.Fatalf("review refused: %v", ref)
	}
	assertEscapeLog(t, home, row)
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
// profile, a private key, the row's planted config, and its data directory.
func escapeHome(t *testing.T, row *escapeRow) string {
	t.Helper()
	home := t.TempDir()
	writeUnder(t, home, ".zshrc", operatorProfile)
	writeUnder(t, home, filepath.Join(".ssh", "id_rsa"), privateKeyCanary+"\n")
	writeUnder(t, home, filepath.FromSlash(row.otherCred), privateKeyCanary+"\n")
	for _, rel := range row.planted {
		writeUnder(t, home, filepath.FromSlash(rel), operatorConfig)
	}
	if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(row.data)), 0o700); err != nil {
		t.Fatal(err)
	}
	return home
}

// assertEscapeLog reads what each escape attempt reported: every write and
// the key read denied, every own-config write denied, the agent's own data
// written, and the key never in its output.
func assertEscapeLog(t *testing.T, home string, row *escapeRow) {
	t.Helper()
	log := string(readFile(t, filepath.Join(home, filepath.FromSlash(row.data), "escape.log")))
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
	if got := string(readFile(t, filepath.Join(home, filepath.FromSlash(row.data), "own-state"))); got != "ok" {
		t.Errorf("the agent's own data = %q, want its write", got)
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
	if i := slices.Index(argv, "--"); argv[0] == "bwrap" && i >= 0 {
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
	req.RoundsRaw, req.FindersRaw = "1", "2"
	req.Environ = append(req.Environ, "HOME="+t.TempDir(), "PATH="+os.Getenv("PATH"))
	if _, ref := TwoFinders(t.Context(), req); ref != nil {
		t.Fatalf("two finders refused: %v", ref)
	}
	assertSiblingAttackDenied(t, commander.attack)
	if raw := readPhaseBytes(t, filepath.Join(dir, "x-finder-2", reviewFile)); strings.Contains(string(raw), "forged") {
		t.Error("finder 1's forgery reached finder 2's review")
	}
	assertCheckoutUntouched(t, root)
}

// assertSiblingAttackDenied reads the attack's outcomes: it reached at least
// the shared tree and finder 2's view, wrote nothing, reached finder 2's
// bundle copy on Linux (where it stays readable), and listed nothing on
// macOS.
func assertSiblingAttackDenied(t *testing.T, log string) {
	t.Helper()
	if strings.Count(log, "/forged") < 2 {
		t.Fatalf("the attack reached fewer than two snapshot directories:\n%s", log)
	}
	if strings.Contains(log, "written") || !strings.Contains(log, "denied exact sibling\n") {
		t.Errorf("finder 1 wrote outside its own view, or never tried finder 2's live path:\n%s", log)
	}
	assertSiblingReads(t, log)
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
// HOME and the extra variables in env, and returns the response and the
// refusal.
func sandboxedRun(t *testing.T, root, home, script, rounds string, env ...string) (*core.RunResponse, *core.RefusalError) {
	t.Helper()
	return sandboxedRunAs(t, "pi", root, home, script, rounds, env...)
}

// sandboxedRunAs is sandboxedRun as another row.
func sandboxedRunAs(t *testing.T, agent, root, home, script, rounds string, env ...string,
) (*core.RunResponse, *core.RefusalError) {
	t.Helper()
	bin := plantStub(t, agent, script)
	req := request(root, hostStub{})
	req.Agent, req.RoundsRaw = agent, rounds
	req.LookPath = func(string) (string, error) { return bin, nil }
	req.Environ = append(append(req.Environ, "HOME="+home, "PATH="+os.Getenv("PATH")), env...)
	return Run(t.Context(), req)
}

// swapScript is a prompt-injected pi across two rounds. Round 1 tries to
// swap its own data directory for a link to HOME and owes an invalid review,
// so a repair round follows; round 2 (the repair directive) writes through
// the link it hoped for and straight into HOME, then the review.
const swapScript = "#!/bin/sh\n" +
	"data=\"$HOME/.pi/agent/sessions\"\n" +
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
	if err := os.MkdirAll(filepath.Join(home, ".pi", "agent", "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, ref := sandboxedRun(t, root, home, swapScript, "2")
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
	sessions := filepath.Join(home, ".pi", "agent", "sessions")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	def, _ := lookupAgent("pi")
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
	if err := os.MkdirAll(filepath.Join(home, ".pi"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(home, ".pi", "agent")); err != nil {
		t.Fatal(err)
	}
	def, _ := lookupAgent("pi")
	if _, err := newReviewBox(&def, []string{"HOME=" + home}, nil); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("newReviewBox = %v, want the linked state path refused", err)
	}
}

// socketScript tries to talk to the unix socket SOCK names from inside the
// sandbox and logs the outcome in the agent's data directory.
const socketScript = "#!/bin/sh\n" +
	"{ printf ping | nc -U -w 2 \"$SOCK\" && echo socket=connected || echo socket=denied; } " +
	"> \"$HOME/.pi/agent/sessions/socket.log\" 2>&1\n" +
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
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".pi", "agent", "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, ref := sandboxedRun(t, confinedRoot(t), home, socketScript, "1", "SOCK="+sock); ref != nil {
		t.Fatalf("review refused: %v", ref)
	}
	select {
	case <-accepted:
		t.Error("the agent connected to a unix socket outside its sandbox")
	default:
	}
	if log := string(readFile(t, filepath.Join(home, ".pi", "agent", "sessions", "socket.log"))); !strings.Contains(log, "socket=denied") {
		t.Errorf("socket log = %q, want the connect denied", log)
	}
}

// opencodeStartScript is opencode's first run on a fresh HOME: it makes its
// repos directory and its cache's bin, and its database creates a journal,
// a WAL and a shared-memory file beside opencode.db — while a write into
// the code its data directory holds, or into the cache's bin, is denied.
const opencodeStartScript = "#!/bin/sh\n" +
	"d=\"$HOME/.local/share/opencode\"\n" +
	"{ /bin/mkdir -p \"$d/repos\" && echo repos=made || echo repos=failed\n" +
	"  /bin/mkdir -p \"$HOME/.cache/opencode/bin\" && echo cachebin=made || echo cachebin=failed\n" +
	"  for f in opencode.db opencode.db-journal opencode.db-wal opencode.db-shm; do " +
	"printf x > \"$d/$f\" && echo \"$f=written\" || echo \"$f=failed\"; done\n" +
	"  printf evil > \"$d/bin/x\" && echo bin=written || echo bin=denied\n" +
	"  printf evil > \"$d/node_modules/x\" && echo modules=written || echo modules=denied\n" +
	"  printf evil > \"$HOME/.cache/opencode/bin/x\" && echo cachebinfile=written || echo cachebinfile=denied\n" +
	"} > \"$d/start.log\" 2>&1\n" +
	"printf '%s' '" + reviewDoc + "' > .verdict/state/42/review.json\n"

// TestReviewSandboxStartsOpencodeFresh proves opencode starts on a HOME it
// has never run in: its data directory and the directories it makes at
// startup exist and are writable where they are data, its database can
// create its journal and WAL, and the code inside its data directory and
// its cache stays read-only.
func TestReviewSandboxStartsOpencodeFresh(t *testing.T) {
	t.Parallel()
	requireReviewSandbox(t)
	home := t.TempDir()
	if _, ref := sandboxedRunAs(t, "opencode", confinedRoot(t), home, opencodeStartScript, "1"); ref != nil {
		t.Fatalf("review refused: %v", ref)
	}
	log := string(readFile(t, filepath.Join(home, ".local", "share", "opencode", "start.log")))
	for _, want := range []string{
		"repos=made", "cachebin=made", "opencode.db=written", "opencode.db-journal=written",
		"opencode.db-wal=written", "opencode.db-shm=written", "bin=denied", "modules=denied", "cachebinfile=denied",
	} {
		if !strings.Contains(log, want+"\n") {
			t.Errorf("start log lacks %q:\n%s", want, log)
		}
	}
}
