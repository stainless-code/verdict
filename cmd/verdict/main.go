// Command verdict is the only front door: it owns every verb, every usage
// error, and the arity matrix. A missing verb fails with the full usage block
// on stderr and exit 2 before any settings, store, or network read. A pair
// verb with a missing repo/pr pair first goes through target resolution
// (internal/target): a pasted URL, the origin remote and a recorded bundle, or
// (for a verb that already talks to the host) the host's own answer fill it
// in; when nothing can, the typed target-unresolved refusal exits 2 with the
// verb's usage line. That refusal reads the settings file only to choose its
// rendering (an unreadable file falls back to defaults), and it never costs a
// host call for a verb with mistyped flags. main is thin: it parses, builds
// the runner, delegates to run(), and exits once — only main calls [os.Exit].
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/stainless-code/verdict/internal/adopt"
	backtestverb "github.com/stainless-code/verdict/internal/backtest"
	"github.com/stainless-code/verdict/internal/calibrate"
	"github.com/stainless-code/verdict/internal/checktree"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/configcmd"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	journalcore "github.com/stainless-code/verdict/internal/core/journal"
	"github.com/stainless-code/verdict/internal/core/upgrade"
	"github.com/stainless-code/verdict/internal/dashboard"
	demo "github.com/stainless-code/verdict/internal/demo"
	"github.com/stainless-code/verdict/internal/doctor"
	drainverb "github.com/stainless-code/verdict/internal/drain"
	"github.com/stainless-code/verdict/internal/fetch"
	"github.com/stainless-code/verdict/internal/fix"
	"github.com/stainless-code/verdict/internal/gate"
	handoffverb "github.com/stainless-code/verdict/internal/handoff"
	"github.com/stainless-code/verdict/internal/index"
	"github.com/stainless-code/verdict/internal/ingest"
	journalverb "github.com/stainless-code/verdict/internal/journal"
	"github.com/stainless-code/verdict/internal/learn"
	"github.com/stainless-code/verdict/internal/loop"
	"github.com/stainless-code/verdict/internal/pack"
	"github.com/stainless-code/verdict/internal/panel"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/prwait"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/ready"
	"github.com/stainless-code/verdict/internal/record"
	"github.com/stainless-code/verdict/internal/report"
	"github.com/stainless-code/verdict/internal/risk"
	runverb "github.com/stainless-code/verdict/internal/run"
	"github.com/stainless-code/verdict/internal/scaffold"
	"github.com/stainless-code/verdict/internal/schemacheck"
	"github.com/stainless-code/verdict/internal/serve"
	"github.com/stainless-code/verdict/internal/target"
	"github.com/stainless-code/verdict/internal/threads"
	"github.com/stainless-code/verdict/internal/trend"
	"github.com/stainless-code/verdict/internal/triage"
	"github.com/stainless-code/verdict/internal/verify"
)

// version is the release version injected at link time by GoReleaser
// (-X main.version={{.Version}}); empty for source builds, which read
// package.json at runtime instead (see versionOf).
var version string

const envHome = "HOME"

const (
	verbSkill  = "skill"
	verbDoctor = "doctor"
	verbACP    = "acp"
	verbLSP    = "lsp"
	// verbDashboard is the terminal dashboard transport.
	verbDashboard = "dashboard"
)

const verbsHint = "demo|fetch|fetch-local|run|loop|panel|record|adopt|ingest|fix|post|triage|report|next|show|" +
	"check-tree|verify|doctor|calibrate|ready|pr-wait|gate|threads|trend|risk|handoff|index|journal|backtest|" +
	"learn|pack|init|skill|upgrade|uninstall|mcp|serve|dashboard|config|drain|acp|lsp"

func main() {
	//nolint:forbidigo // GO-LAYOUT-02: main is the only os.Exit caller by design
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv, os.ReadFile))
}

//
//nolint:revive // the hidden transport dispatch adds one branch to the front door
func run(args []string, stdout, stderr *os.File, getenv func(string) string, readFile func(string) ([]byte, error)) int {
	if code, ok := dispatchHidden(args, stderr); ok {
		return code
	}
	provenance.SetBuildVersion(strings.TrimPrefix(version, "v"))
	if len(args) > 0 && args[0] == "mcp" {
		return runMCP(args[1:], stdout, stderr, getenv, readFile)
	}
	if done := handleMetaFlags(args, stdout, readFile); done {
		return cli.ExitOK
	}
	if block, ok := verbHelp(args); ok {
		fmt.Fprint(stdout, block)
		return cli.ExitOK
	}
	root := exeRoot()
	stripped, forces, uerr, ref := cli.StripGlobal(args)
	failed := uerr != nil || ref != nil
	cmd, repo, pr, rest := splitCommand(stripped)
	env := envMap(getenv)
	uverb := usageVerbFor(cmd, repo)
	if failed {
		cmd, repo, pr = envelopeTarget(args)
	}
	r := newRunner(cmd, repo, pr, stdout, stderr, getenv)
	defer r.Flush()
	peekJSONMode(r, args)
	if failed {
		return reportStripFailure(r, env, readFile, uerr, ref)
	}
	if code, bad := checkHostFlags(r, forces, env, readFile, cmd, repo, pr, rest); bad {
		return code
	}
	tgt := resolveTarget(cmd, repo, pr, rest, env, getenv)
	r.Repo, r.Pr = tgt.Repo, tgt.Pr
	if code, done := gateInvocation(r, uverb, &tgt, forces, env, readFile, stderr); done {
		return code
	}
	return dispatch(cmd, r, &tgt, root, env, forces, args, stderr, readFile)
}

// gateInvocation runs the checks that stop an invocation before its verb: the
// target refusal of a pair verb that names no resolvable target (a usage-class
// failure, rendered with the settings applied when they read and the
// defaults when they do not), the arity gate for every other verb, the
// preference read, the global forces (so --no-json and --color shape every
// message below), any other target refusal, and the echo of a derived target.
// verb is the name the usage line is looked up under. done reports that code
// is the exit status.
func gateInvocation(
	r *cli.Runner, verb string, tgt *target.Resolved, forces *cli.GlobalForces, env map[string]string,
	readFile func(string) ([]byte, error), stderr *os.File,
) (code int, done bool) {
	if ref := tgt.Refusal; ref != nil && (ref.Code == core.CodeTargetUnresolved || ref.Code == cli.CodeCanceled) {
		return refuseEarly(r, verb, ref, forces, env, readFile, stderr), true
	}
	if tgt.Refusal == nil && invocationArityUsage(r.Cmd, tgt.Repo, tgt.Pr, tgt.Rest) {
		fmt.Fprint(stderr, usageText())
		return cli.ExitUsage, true
	}
	return settleInvocation(r, tgt, forces, env, readFile, stderr)
}

// settleInvocation reads the preferences, applies the global forces, then
// renders any other target refusal or echoes a derived target.
func settleInvocation(
	r *cli.Runner, tgt *target.Resolved, forces *cli.GlobalForces, env map[string]string,
	readFile func(string) ([]byte, error), stderr *os.File,
) (code int, done bool) {
	if code, done := resolveRunnerPrefs(r, env, readFile); done {
		return code, true
	}
	if err := cli.ApplyGlobalForces(r, forces, noColorForFallback(r, env)); err != nil {
		r.RouteHuman()
		return r.Refuse(err), true
	}
	if tgt.Refusal != nil {
		r.RouteHuman()
		return r.Refuse(tgt.Refusal), true
	}
	if tgt.Note != "" && !r.JSONMode {
		fmt.Fprintln(stderr, tgt.Note)
	}
	return 0, false
}

// refuseEarly renders a target refusal that must not be replaced by a
// settings failure: target-unresolved as the usage-class failure it is (exit
// 2, the typed envelope under --json, the verb's usage line in human mode) and
// a cancel as exit 3. Unreadable settings fall back to the defaults.
func refuseEarly(
	r *cli.Runner, verb string, ref *core.RefusalError, forces *cli.GlobalForces,
	env map[string]string, readFile func(string) ([]byte, error), stderr *os.File,
) int {
	if code, failed := prepareRender(r, forces, env, readFile); failed {
		return code
	}
	r.RouteHuman()
	code := r.RefuseExit(ref, cli.ExitUsage)
	if !r.JSONMode && ref.Code == core.CodeTargetUnresolved {
		fmt.Fprintln(stderr, usageLine(verb))
	}
	return code
}

// usageVerbFor names the usage entry a verb's invocation is documented under:
// the threads subverb has its own line.
func usageVerbFor(cmd, repo string) string {
	if cmd == cmdThreads && repo == subverbResolve {
		return threadsResolve
	}
	return cmd
}

// usageLine is the first line of the verb's usage entry that takes the target
// (a verb may document a flag-only form first, as report --html does), as one
// `usage:` line.
func usageLine(verb string) string {
	for line := range strings.SplitSeq(verbUsage(verb), "\n") {
		if strings.Contains(line, "<owner/repo>") {
			return "usage: verdict " + strings.Join(strings.Fields(line), " ")
		}
	}
	return "usage: verdict " + verb + " <owner/repo> <pr> [opts]"
}

func handleMetaFlags(args []string, stdout *os.File, readFile func(string) ([]byte, error)) bool {
	if len(args) > 0 && (args[0] == flagShortHelp || args[0] == flagHelp) {
		fmt.Fprint(stdout, usageText())
		return true
	}
	if len(args) > 0 && args[0] == "--version" {
		// --version prefers the linked release version over package.json at runtime (see versionOf).
		if v := strings.TrimPrefix(version, "v"); v != "" {
			fmt.Fprintln(stdout, v)
			return true
		}
		fmt.Fprintln(stdout, versionOf(exeRoot(), readFile))
		return true
	}
	return false
}

func peekJSONMode(r *cli.Runner, args []string) {
	r.JSONPeeked = cli.PeekJSON(args)
	if r.JSONPeeked {
		r.JSONMode = true
	}
}

func noColorForFallback(r *cli.Runner, env map[string]string) bool {
	if r.EnvNoColor {
		return true
	}
	if _, set := env["NO_COLOR"]; !set && r.JSONMode {
		return true
	}
	return false
}

func dispatchHidden(args []string, stderr *os.File) (code int, ok bool) {
	if len(args) == 0 {
		return 0, false
	}
	switch args[0] {
	case upgrade.HelperCommand:
		return upgrade.RunHelper(context.Background(), args[1:], stderr), true
	case schemacheck.Command:
		return schemacheck.Run(args[1:], stderr), true
	default:
		return 0, false
	}
}

type readHandler func(context.Context, *cli.Runner, []string, readcommon.Request) int

const cmdTrend = "trend"

const cmdFetchLocal = "fetch-local"

// The host-talking verbs, named once for the tables and the target gate.
const (
	cmdFetch  = "fetch"
	cmdReady  = "ready"
	cmdPRWait = "pr-wait"
	cmdDrain  = "drain"
)

const (
	cmdThreads     = "threads"
	subverbResolve = "resolve"
	// threadsResolve names the subverb the way hosts tables and usage do.
	threadsResolve = cmdThreads + " " + subverbResolve
)

// flagPrefix starts every flag token; a positional never does.
const flagPrefix = "-"

const cmdBacktest = "backtest"

const cmdDemo = "demo"

const cmdRisk = "risk"

const cmdLoop = "loop"

var readVerbs = map[string]readHandler{
	"config":     configcmd.Run,
	"panel":      panel.Run,
	"report":     report.Run,
	"next":       report.RunNext,
	cmdShow:      report.RunShow,
	cmdReady:     ready.Run,
	cmdPRWait:    prwait.Run,
	"gate":       gate.Run,
	"threads":    threads.Run,
	cmdDrain:     drainverb.Run,
	cmdTrend:     trend.Run,
	cmdRisk:      risk.Run,
	"calibrate":  calibrate.Run,
	verbDoctor:   doctor.Run,
	"check-tree": checktree.Run,
	"index":      index.Run,
	"journal":    journalverb.Run,
	"pack":       pack.Run,
	"handoff":    handoffverb.Run,
}

// repoFreeVerbs run without a resolvable repository root: doctor exists to
// report why the probe cannot answer (a missing git is a warning row), skill
// only writes the embedded payload into agent directories, and demo reads a
// public pull request by URL with no checkout at all (its local form refuses
// not-a-repo itself, naming both invocation shapes). They keep a cwd-relative
// posture instead of refusing not-a-repo.
var repoFreeVerbs = map[string]bool{verbDoctor: true, verbSkill: true, cmdDemo: true}

// shutdownSignalContext is the root context of every long-running entry point:
// SIGINT and SIGTERM cancel it (GO-CLI-05). SIGKILL cannot be caught, so
// SIGTERM (kill, docker stop, systemd) is the second catchable shutdown signal.
func shutdownSignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// dispatch runs the verb against the resolved target. The target's host rides
// on the context, so every gh and glab call the verb makes names it
// (core.PinHostArgv) and the environment's default host cannot redirect one.
func dispatch(
	cmd string, r *cli.Runner, tgt *target.Resolved, root string, env map[string]string,
	forces *cli.GlobalForces, args []string, stderr *os.File,
	readFile func(string) ([]byte, error),
) int {
	repo, pr, rest := tgt.Repo, tgt.Pr, tgt.Rest
	r.RouteHuman()
	ctx, stop := shutdownSignalContext()
	defer stop()
	ctx = core.WithHost(ctx, tgt.Host)
	if htmlInvocation(cmd, rest) {
		return serve.RunHTML(ctx, r, rest, cliVersion(root, readFile))
	}
	switch cmd {
	case "upgrade":
		return runUpgrade(ctx, r, rest, env)
	case "uninstall":
		return runUninstall(ctx, r, rest, env)
	case "serve":
		return serve.Run(ctx, r, rest, serve.Request{Version: cliVersion(root, readFile), Env: env, InstallRoot: root})
	case verbDashboard:
		return dashboard.Run(ctx, r, rest, dashboard.Request{
			In: os.Stdin, Term: os.Getenv("TERM"), Version: cliVersion(root, readFile),
		})
	case verbACP:
		return runACP(ctx, r, rest, cliVersion(root, readFile))
	case verbLSP:
		return runLSP(ctx, r, rest, cliVersion(root, readFile))
	}
	repoRoot, code, ok := resolveRepoRoot(ctx, r, cmd)
	if !ok {
		return code
	}
	scan, restore := cli.InstallNextScanner(r)
	defer restore()
	return journalcore.Guard(&journalcore.Invocation{
		Verb:     cmd,
		Channel:  journalcore.ChannelCLI,
		RepoRoot: repoRoot,
		Pr:       pr,
		Env:      env,
		Argv:     args,
		Environ:  os.Environ(),
		Version:  cliVersion(root, readFile),
		Now:      r.Now,
		Warn:     r.Warn,
		Refusal:  r.RefusalCode,
		Warnings: func() int { return len(r.Warnings) },
		Next:     func() string { return scan.Next(r.JSONMode) },
	}, func() int {
		return dispatchVerb(ctx, cmd, r, repo, pr, root, repoRoot, env, forces, rest, stderr)
	})
}

func dispatchVerb(
	ctx context.Context, cmd string, r *cli.Runner, repo, pr, root, repoRoot string,
	env map[string]string, forces *cli.GlobalForces, rest []string, stderr *os.File,
) int {
	if h, ok := readVerbs[cmd]; ok {
		return h(ctx, r, rest, readcommon.Request{
			Runtime: &core.Runtime{Env: env, Now: r.Now},
			Repo:    repo, Pr: pr, Forces: forces, Root: &root,
			RepoRoot: repoRoot,
		})
	}
	if code, ok := dispatchMutating(ctx, cmd, r, repo, pr, root, repoRoot, env, forces, rest); ok {
		return code
	}
	fmt.Fprintf(stderr, "%s\n", unknownCmdMessage(cmd))
	return cli.ExitUsage
}

// resolveRepoRoot resolves the reviewed repository root for one verb. A probe
// failure refuses not-a-repo, except for the repo-free verbs, which keep the
// cwd-relative posture so they can still report or install.
func resolveRepoRoot(ctx context.Context, r *cli.Runner, cmd string) (root string, code int, ok bool) {
	root, err := readcommon.RepoRoot(ctx)
	if err == nil {
		return root, 0, true
	}
	if exit, done := r.Canceled(ctx, cmd); done {
		return "", exit, false
	}
	if !repoFreeVerbs[cmd] {
		return "", r.Refuse(&cli.RefusalError{Code: "not-a-repo", Message: err.Error()}), false
	}
	return ".", 0, true
}

func dispatchMutating(
	ctx context.Context, cmd string, r *cli.Runner, repo, pr, root, repoRoot string,
	env map[string]string, forces *cli.GlobalForces, rest []string,
) (code int, ok bool) {
	if code, ok := dispatchFetch(ctx, cmd, r, repo, pr, root, repoRoot, env, rest); ok {
		return code, true
	}
	if code, ok := dispatchLocal(ctx, cmd, r, env, root, repoRoot, rest); ok {
		return code, true
	}
	switch cmd {
	case "run":
		// The verb name matches main's own run() entry point, so the adapter import
		// is aliased rather than renaming either one.
		return runverb.Run(ctx, r, rest, runverb.Request{
			Env: env, Repo: repo, Pr: pr, Root: root, RepoRoot: repoRoot,
		}), true
	case cmdLoop:
		return loop.Run(ctx, r, rest, loop.Request{
			Env: env, Forces: forces, Repo: repo, Root: root, RepoRoot: repoRoot,
		}), true
	case "record":
		return record.Run(ctx, r, rest, &record.Request{Repo: repo, Pr: pr, Root: root, RepoRoot: repoRoot, Env: env, Forces: forces}), true
	case "post":
		return record.RunPost(ctx, r, rest, &record.Request{
			Repo: repo, Pr: pr, Root: root, RepoRoot: repoRoot, Env: env, Forces: forces,
		}), true
	default:
		return dispatchStoreVerbs(ctx, cmd, r, repo, pr, root, repoRoot, env, rest)
	}
}

func dispatchStoreVerbs(
	ctx context.Context, cmd string, r *cli.Runner, repo, pr, root, repoRoot string,
	env map[string]string, rest []string,
) (int, bool) {
	switch cmd {
	case "triage":
		return triage.Run(ctx, r, rest, triage.Request{Repo: repo, Pr: pr, Root: root, RepoRoot: repoRoot, Env: env}), true
	case "adopt":
		return adopt.Run(ctx, r, rest, adopt.Request{Repo: repo, Pr: pr, Root: root, RepoRoot: repoRoot, Env: env}), true
	case "ingest":
		return ingest.Run(ctx, r, rest, &ingest.Request{Repo: repo, Pr: pr, Root: root, RepoRoot: repoRoot, Env: env}), true
	case "fix":
		return fix.Run(ctx, r, rest, fix.Request{Repo: repo, Pr: pr, Root: root, RepoRoot: repoRoot, Env: env}), true
	case "learn":
		return learn.Run(ctx, r, rest, repoRoot), true
	case "verify":
		return verify.Run(ctx, r, rest, verify.Request{Repo: repo, Pr: pr, Root: root, RepoRoot: repoRoot, Env: env}), true
	default:
		return 0, false
	}
}

// dispatchLocal serves the verbs that need no repository or pull request:
// the scaffold writer, the local backtest miner, and the skill distributor.
// They share dispatchMutating's "handled" contract so one switch can stay
// small enough to read.
func dispatchLocal(
	ctx context.Context, cmd string, r *cli.Runner, env map[string]string, root, repoRoot string, rest []string,
) (int, bool) {
	switch cmd {
	case "init":
		return scaffold.Run(ctx, r, rest, scaffold.Request{Env: env, RepoRoot: repoRoot}), true
	case cmdBacktest:
		return backtestverb.Run(ctx, r, rest, backtestverb.Request{
			Env: env, Root: root, RepoRoot: repoRoot,
		}), true
	case cmdDemo:
		// demo owns its own positionals (an optional public PR URL), so it
		// reads neither the repo nor the pr split above.
		return demo.Run(ctx, r, rest, demo.Request{Env: env, Root: root}), true
	case verbSkill:
		return runSkill(ctx, r, rest, env, root), true
	default:
		return 0, false
	}
}

func dispatchFetch(
	ctx context.Context, cmd string, r *cli.Runner, repo, pr, root, repoRoot string,
	env map[string]string, rest []string,
) (code int, ok bool) {
	req := fetch.Request{Repo: repo, Pr: pr, Root: root, RepoRoot: repoRoot, Env: env}
	switch cmd {
	case cmdFetch:
		return fetch.Run(ctx, r, rest, req), true
	case "fetch-local":
		return fetch.RunLocal(ctx, r, rest, req), true
	default:
		return 0, false
	}
}

func splitCommand(args []string) (cmd, repo, pr string, rest []string) {
	if len(args) == 0 {
		return "", "", "", args
	}
	cmd, args = args[0], args[1:]
	if noPositionalPair(cmd, args) {
		return cmd, "", "", args
	}
	if repoOnlyVerbs[cmd] {
		// A flag first means the repo is absent, exactly as for the pair verbs.
		if len(args) > 0 && !strings.HasPrefix(args[0], flagPrefix) {
			repo, args = args[0], args[1:]
		}
		return cmd, repo, "", args
	}
	repo, pr, rest = splitPositionals(args)
	return cmd, repo, pr, rest
}

// noPositionalPair reports the invocations whose arguments are never a repo/pr
// pair: the HTML surface, the bare verbs, the verbs with one optional
// positional, and the local backtest miner.
func noPositionalPair(cmd string, args []string) bool {
	return htmlInvocation(cmd, args) || bareVerbs[cmd] || optionals[cmd] || cmd == cmdBacktest
}

// splitPositionals reads the repo/pr pair from the leading non-flag tokens: a
// flag before the pair ends it (the pair is then absent, and target inference
// may supply it), and "--" ends everything.
func splitPositionals(args []string) (repo, pr string, rest []string) {
	for i, a := range args {
		if a == flagSeparator || (i < 2 && strings.HasPrefix(a, flagPrefix)) {
			return repo, pr, append(rest, args[i:]...)
		}
		switch i {
		case 0:
			repo = a
		case 1:
			pr = a
		default:
			rest = append(rest, a)
		}
	}
	return repo, pr, rest
}

func newRunner(cmd, repo, pr string, stdout, stderr *os.File, getenv func(string) string) *cli.Runner {
	return cli.NewRunner(cmd, repo, pr, stdout, stderr, getenv)
}

func resolveRunnerPrefs(r *cli.Runner, env map[string]string, readFile func(string) ([]byte, error)) (int, bool) {
	prefs, ref := cli.ResolvePrefs(r, env, readFile)
	if ref != nil {
		r.Effective = cli.Defaults()
		r.RouteHuman()
		return r.Refuse(ref), true
	}
	r.Effective = prefs.Effective
	return 0, false
}

func reportStripFailure(
	r *cli.Runner, env map[string]string, readFile func(string) ([]byte, error),
	uerr *cli.UsageError, ref error,
) int {
	if code, done := resolveRunnerPrefs(r, env, readFile); done {
		return code
	}
	r.RouteHuman()
	if uerr != nil {
		return r.ReportUsage(uerr)
	}
	return r.Refuse(ref)
}

func envelopeTarget(args []string) (cmd, repo, pr string) {
	cmd, repo, pr, _ = splitCommand(args)
	if strings.HasPrefix(cmd, flagPrefix) {
		return "", "", ""
	}
	if strings.HasPrefix(repo, flagPrefix) {
		return cmd, "", ""
	}
	if strings.HasPrefix(pr, flagPrefix) {
		return cmd, repo, ""
	}
	return cmd, repo, pr
}

func exeRoot() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return checkoutRoot(filepath.Dir(exe))
}

const checkoutDepth = 5

func checkoutRoot(dir string) string {
	root := dir
	for range checkoutDepth {
		if st, err := os.Stat(filepath.Join(root, "package.json")); err == nil && !st.IsDir() {
			return root
		}
		parent := filepath.Dir(root)
		if parent == root {
			break
		}
		root = parent
	}
	return dir
}

func envMap(getenv func(string) string) map[string]string {
	keys := []string{
		"HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "NO_COLOR",
		"REVIEW_OUTPUT_FORMAT", "REVIEW_LAYOUT_COMPACT", "REVIEW_COLOR_MODE",
		"REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL", "REVIEW_PUBLISH_DIAGRAMS_ANCHORED",
		"REVIEW_PUBLISH_FOOTER",
		"REVIEW_PUBLISH_ATTRIBUTION", "REVIEW_PUBLISH_ATTRIBUTION_TEXT",
		"REVIEW_PUBLISH_COPYRIGHT", "REVIEW_PUBLISH_LOGO", "REVIEW_PUBLISH_LOGO_URL",
		"REVIEW_PUBLISH_OVERVIEW", "REVIEW_PUBLISH_FIX_PROMPT", "REVIEW_MODEL",
		"REVIEW_MAX_BYTES", "REVIEW_BUDGET_USD", "REVIEW_PANEL",
		"REVIEW_STRICT_INPUT",
		"FORGEJO_HOST", "FORGEJO_TOKEN",
		"BITBUCKET_TOKEN", "BITBUCKET_USERNAME", "BITBUCKET_PASSWORD",
		"AZURE_DEVOPS_ORG", "AZURE_DEVOPS_EXT_PAT",
		"REVIEW_KB_MIN_JUDGED", "REVIEW_KB_MAX_FP_RATE",
		"VERDICT_INSTALL", "VERDICT_INSTALL_METHOD", "VERDICT_NPM_MANAGED",
		"VERDICT_MANAGED", "VERDICT_DOWNLOAD_URL", "VERDICT_COSIGN",
		"VERDICT_REQUIRE_SIGNATURE", "VERDICT_USAGE", "SHELL", "ZDOTDIR", "PATH",
		"APPDATA", "LOCALAPPDATA", "PNPM_HOME", "BUN_INSTALL",
		"CLAUDE_CONFIG_DIR", "CODEX_HOME",
		"NPM_CONFIG_PREFIX", "HOMEBREW_PREFIX",
	}
	env := make(map[string]string, len(keys))
	for _, k := range keys {
		if v := getenv(k); v != "" {
			env[k] = v
		}
	}
	if env[envHome] == "" {
		if wd, err := os.Getwd(); err == nil {
			env[envHome] = wd
		} else {
			env[envHome] = "."
		}
	}
	return env
}

// cliVersion prefers the linked release version over package.json at runtime,
// the same rule --version follows.
func cliVersion(root string, readFile func(string) ([]byte, error)) string {
	if v := strings.TrimPrefix(version, "v"); v != "" {
		return v
	}
	return versionOf(root, readFile)
}

func versionOf(root string, readFile func(string) ([]byte, error)) string {
	raw, err := readFile(filepath.Join(root, "package.json"))
	if err != nil {
		return core.VersionUnknown
	}
	var doc struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Version == "" {
		return core.VersionUnknown
	}
	return doc.Version
}

func htmlInvocation(cmd string, args []string) bool {
	return cmd == "report" && len(args) > 0 && args[0] == "--html"
}

func invocationArityUsage(cmd, repo, pr string, args []string) bool {
	return arityUsage(cmd, repo, pr) && !htmlInvocation(cmd, args)
}

// resolveTarget turns the split positionals into the verb's target: a pasted
// pull request URL, or the pair inferred from the origin remote and a recorded
// bundle. It runs only for the verbs that take a repo/pr pair and never for
// fetch-local, whose second positional is a bundle name.
func resolveTarget(cmd, repo, pr string, rest []string, env map[string]string, getenv func(string) string) target.Resolved {
	if !takesTarget(cmd, rest) {
		return target.Resolved{Repo: repo, Pr: pr, Rest: rest}
	}
	if cmd == cmdThreads && repo == subverbResolve {
		return resolveSubverb(pr, rest, env, getenv)
	}
	return runResolver(cmd, repo, pr, rest, env, getenv)
}

// resolveSubverb resolves `threads resolve <owner/repo> <pr>`: the subverb sits
// where the repo would, so the pair behind it is resolved and the subverb is
// put back in the shifted shape the verb reads (subverb, repo, then the pr
// first in rest). Threads resolve takes no --provider, so the verb name carries
// the subverb to keep the flag from being injected.
func resolveSubverb(first string, rest []string, env map[string]string, getenv func(string) string) target.Resolved {
	repo, pr, tail := splitSubverb(first, rest)
	got := runResolver(threadsResolve, repo, pr, tail, env, getenv)
	if got.Refusal != nil {
		return got
	}
	got.Repo, got.Pr, got.Rest = subverbResolve, got.Repo, append([]string{got.Pr}, got.Rest...)
	return got
}

// runResolver reads GITLAB_HOST beside the environment map (envMap does not
// carry it; it only maps a self-hosted URL or remote to GitLab) and runs the
// shared resolver.
func runResolver(verb, repo, pr string, rest []string, env map[string]string, getenv func(string) string) target.Resolved {
	targetEnv := maps.Clone(env)
	if h := getenv("GITLAB_HOST"); h != "" {
		targetEnv["GITLAB_HOST"] = h
	}
	ctx, stop := shutdownSignalContext()
	defer stop()
	return target.Resolve(ctx, target.Deps{Commander: coreexec.Runner{}, RepoRoot: readcommon.RepoRoot}, &target.Request{
		Verb: verb, Repo: repo, Pr: pr, Rest: rest, RepoOnly: repoOnlyVerbs[verb], Env: targetEnv,
		HostLookup: talksToHost(verb, rest), PrArg: prArgFor(verb),
	})
}

// prArgFor is how the verb's own usage line spells its number argument
// (`<pr>`, `<pr|local-name>`, `<local-slug|pr>`), so a refusal names the
// arguments the way the usage line does.
func prArgFor(verb string) string {
	fields := strings.Fields(usageLine(verb))
	if at := slices.Index(fields, "<owner/repo>"); at >= 0 && at+1 < len(fields) && strings.HasPrefix(fields[at+1], "<") {
		return fields[at+1]
	}
	return "<pr>"
}

// splitSubverb reads the pair and flags behind the threads subverb, which the
// front door's own split saw as (subverb, first, rest).
func splitSubverb(first string, rest []string) (repo, pr string, tail []string) {
	return splitPositionals(append(nonEmpty(first), rest...))
}

// checkHostFlags validates the flags of a verb that may ask the host for its
// target, before any git or host call, so a mistyped flag is a plain usage
// error and never a lookup followed by one. The check first runs silently; only
// a failure renders, through the same output mode the verb would have used (the
// readable settings, else the defaults, then the global flags), and reports
// the verb's own error exactly as the verb would.
func checkHostFlags(
	r *cli.Runner, forces *cli.GlobalForces, env map[string]string, readFile func(string) ([]byte, error),
	cmd, repo, pr string, rest []string,
) (code int, bad bool) {
	check, args, ok := hostFlagCheck(cmd, repo, pr, rest)
	if !ok {
		return 0, false
	}
	silent := &cli.Runner{Out: io.Discard, Human: io.Discard, Diag: io.Discard, Now: r.Now}
	if _, bad := check(silent, args); !bad {
		return 0, false
	}
	if code, failed := prepareRender(r, forces, env, readFile); failed {
		return code, true
	}
	return check(r, args)
}

// hostFlagCheck picks the flag checker and the flag arguments of a verb that
// may ask the host; ok is false for any other invocation. A URL invocation's
// "--provider=x" is spaced first, because the URL's provider is resolved only
// after the check.
func hostFlagCheck(cmd, repo, pr string, rest []string) (check func(*cli.Runner, []string) (int, bool), args []string, ok bool) {
	if !takesTarget(cmd, rest) {
		return nil, nil, false
	}
	verb, args := cmd, rest
	if cmd == cmdThreads && repo == subverbResolve {
		_, _, args = splitSubverb(pr, rest)
		verb = threadsResolve
	}
	if core.IsTargetURL(repo) {
		args = target.SpaceProviderFlag(args)
	}
	check, ok = hostFlagCheckers[verb]
	return check, args, ok && talksToHost(verb, args)
}

// prepareRender settles how an early refusal renders: the readable settings,
// else the defaults (a corrupt file never replaces the refusal), then the
// global flags, so the last of --json and --no-json decides the output mode.
// failed reports a global flag that itself refused, with its exit code.
func prepareRender(
	r *cli.Runner, forces *cli.GlobalForces, env map[string]string, readFile func(string) ([]byte, error),
) (code int, failed bool) {
	r.Effective = cli.Defaults()
	if prefs, perr := cli.ResolvePrefs(r, env, readFile); perr == nil {
		r.Effective = prefs.Effective
	}
	if err := cli.ApplyGlobalForces(r, forces, noColorForFallback(r, env)); err != nil {
		r.RouteHuman()
		return r.Refuse(err), true
	}
	return 0, false
}

// hostFlagCheckers is each host-talking verb's own flag validation. A test
// pins it to hostVerbs and drain, so a verb cannot start asking the host
// without one.
var hostFlagCheckers = map[string]func(*cli.Runner, []string) (int, bool){
	cmdFetch:       fetch.CheckFlags,
	cmdPRWait:      prwait.CheckFlags,
	cmdReady:       ready.CheckFlags,
	cmdThreads:     threads.CheckFlags,
	threadsResolve: threads.CheckResolveFlags,
	cmdDrain:       drainverb.CheckFlags,
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

// hostVerbs always read the host as part of their work, so asking it which
// pull request a branch heads costs them nothing new. Every other pair verb
// is offline, and drain only reaches the host with --post.
var hostVerbs = map[string]bool{
	cmdFetch: true, cmdPRWait: true, cmdReady: true, cmdThreads: true, threadsResolve: true,
}

// talksToHost reports whether verb, with these flags, reads the host anyway.
func talksToHost(verb string, rest []string) bool {
	if verb == cmdDrain {
		at := slices.Index(rest, flagSeparator)
		if at < 0 {
			at = len(rest)
		}
		return slices.Contains(rest[:at], "--post")
	}
	return hostVerbs[verb]
}

// takesTarget reports the known verbs whose positionals are `<owner/repo> <pr>`
// (or `<owner/repo>` alone for the repo-only verbs).
func takesTarget(cmd string, rest []string) bool {
	if cmd == "" || cmd == cmdFetchLocal || noPositionalPair(cmd, rest) {
		return false
	}
	return slices.Contains(strings.Split(verbsHint, "|"), cmd)
}
