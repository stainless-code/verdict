// Command verdict is the only front door: it owns every verb, every usage
// error, and the arity matrix. A missing verb or
// positional fails with the full usage block on stderr and exit 2 before
// any settings, store, or network read. main is thin: it parses, builds
// the runner, delegates to run(), and exits once — only main calls [os.Exit].
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/stainless-code/verdict/internal/adopt"
	backtestverb "github.com/stainless-code/verdict/internal/backtest"
	"github.com/stainless-code/verdict/internal/calibrate"
	"github.com/stainless-code/verdict/internal/checktree"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/configcmd"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/upgrade"
	"github.com/stainless-code/verdict/internal/doctor"
	"github.com/stainless-code/verdict/internal/fetch"
	"github.com/stainless-code/verdict/internal/fix"
	"github.com/stainless-code/verdict/internal/gate"
	"github.com/stainless-code/verdict/internal/index"
	"github.com/stainless-code/verdict/internal/learn"
	"github.com/stainless-code/verdict/internal/pack"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/ready"
	"github.com/stainless-code/verdict/internal/record"
	"github.com/stainless-code/verdict/internal/report"
	runverb "github.com/stainless-code/verdict/internal/run"
	"github.com/stainless-code/verdict/internal/scaffold"
	"github.com/stainless-code/verdict/internal/schemacheck"
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
)

const verbsHint = "fetch|fetch-local|run|record|adopt|fix|post|triage|report|next|show|" +
	"check-tree|verify|doctor|calibrate|ready|gate|threads|trend|index|backtest|learn|pack|init|skill|upgrade|uninstall|mcp|config"

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
	if failed {
		cmd, repo, pr = envelopeTarget(args)
	}
	r := newRunner(cmd, repo, pr, stdout, stderr, getenv)
	peekJSONMode(r, args)
	env := envMap(getenv)
	if failed {
		return reportStripFailure(r, env, readFile, uerr, ref)
	}
	if arityUsage(cmd, repo, pr) {
		fmt.Fprint(stderr, usageText())
		return cli.ExitUsage
	}
	if code, done := resolveRunnerPrefs(r, env, readFile); done {
		return code
	}
	if err := cli.ApplyGlobalForces(r, forces, noColorForFallback(r, env)); err != nil {
		r.RouteHuman()
		return r.Refuse(err)
	}
	return dispatch(cmd, r, repo, pr, root, env, forces, rest, stderr)
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

const cmdBacktest = "backtest"

var readVerbs = map[string]readHandler{
	"config":     configcmd.Run,
	"report":     report.Run,
	"next":       report.RunNext,
	cmdShow:      report.RunShow,
	"ready":      ready.Run,
	"gate":       gate.Run,
	"threads":    threads.Run,
	cmdTrend:     trend.Run,
	"calibrate":  calibrate.Run,
	verbDoctor:   doctor.Run,
	"check-tree": checktree.Run,
	"index":      index.Run,
	"pack":       pack.Run,
}

// repoFreeVerbs run without a resolvable repository root: doctor exists to
// report why the probe cannot answer (a missing git is a warning row), and
// skill only writes the embedded payload into agent directories. They keep a
// cwd-relative posture instead of refusing not-a-repo.
var repoFreeVerbs = map[string]bool{verbDoctor: true, verbSkill: true}

// shutdownSignalContext is the root context of every long-running entry point:
// SIGINT and SIGTERM cancel it (GO-CLI-05). SIGKILL cannot be caught, so
// SIGTERM (kill, docker stop, systemd) is the second catchable shutdown signal.
func shutdownSignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func dispatch(
	cmd string, r *cli.Runner, repo, pr, root string, env map[string]string,
	forces *cli.GlobalForces, rest []string, stderr *os.File,
) int {
	r.RouteHuman()
	ctx, stop := shutdownSignalContext()
	defer stop()
	switch cmd {
	case "upgrade":
		return runUpgrade(ctx, r, rest, env)
	case "uninstall":
		return runUninstall(ctx, r, rest, env)
	}
	repoRoot, code, ok := resolveRepoRoot(ctx, r, cmd)
	if !ok {
		return code
	}
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
	case "record":
		return record.Run(ctx, r, rest, &record.Request{Repo: repo, Pr: pr, Root: root, RepoRoot: repoRoot, Env: env, Forces: forces}), true
	case "post":
		return record.RunPost(ctx, r, rest, &record.Request{
			Repo: repo, Pr: pr, Root: root, RepoRoot: repoRoot, Env: env, Forces: forces,
		}), true
	case "triage":
		return triage.Run(ctx, r, rest, triage.Request{Repo: repo, Pr: pr, Root: root, RepoRoot: repoRoot, Env: env}), true
	case "adopt":
		return adopt.Run(ctx, r, rest, adopt.Request{Repo: repo, Pr: pr, Root: root, RepoRoot: repoRoot, Env: env}), true
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
	case "fetch":
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
	if bareVerbs[cmd] {
		return cmd, "", "", args
	}
	if cmd == cmdTrend {
		if len(args) > 0 {
			repo, rest = args[0], args[1:]
		}
		return cmd, repo, "", rest
	}
	if cmd == cmdBacktest {
		return cmd, "", "", args
	}
	repo, pr, rest = splitPositionals(args)
	return cmd, repo, pr, rest
}

func splitPositionals(args []string) (repo, pr string, rest []string) {
	for i, a := range args {
		if a == flagSeparator {
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
	return &cli.Runner{
		Cmd: cmd, Repo: repo, Pr: pr,
		Out: stdout, Diag: stderr, Human: stdout,
		Now:                time.Now,
		EnvNoColor:         getenv("NO_COLOR") != "",
		EnvReviewColorMode: getenv("REVIEW_COLOR_MODE"),
	}
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
	if strings.HasPrefix(cmd, "-") {
		return "", "", ""
	}
	if strings.HasPrefix(repo, "-") {
		return cmd, "", ""
	}
	if strings.HasPrefix(pr, "-") {
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
		"HOME", "XDG_CONFIG_HOME", "NO_COLOR",
		"REVIEW_OUTPUT_FORMAT", "REVIEW_LAYOUT_COMPACT", "REVIEW_COLOR_MODE",
		"REVIEW_PUBLISH_DIAGRAMS_STRUCTURAL", "REVIEW_PUBLISH_DIAGRAMS_BEHAVIORAL",
		"REVIEW_PUBLISH_DIAGRAMS_EXPANDED", "REVIEW_PUBLISH_FOOTER",
		"REVIEW_PUBLISH_ATTRIBUTION", "REVIEW_PUBLISH_ATTRIBUTION_TEXT",
		"REVIEW_PUBLISH_COPYRIGHT", "REVIEW_PUBLISH_LOGO", "REVIEW_PUBLISH_LOGO_URL",
		"REVIEW_PUBLISH_OVERVIEW", "REVIEW_MODEL",
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
		if v, ok := os.LookupEnv(k); ok {
			env[k] = v
		} else if v := getenv(k); v != "" {
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
