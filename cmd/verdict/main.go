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
	"github.com/stainless-code/verdict/internal/calibrate"
	"github.com/stainless-code/verdict/internal/checktree"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/upgrade"
	"github.com/stainless-code/verdict/internal/doctor"
	"github.com/stainless-code/verdict/internal/fetch"
	"github.com/stainless-code/verdict/internal/fix"
	"github.com/stainless-code/verdict/internal/learn"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/ready"
	"github.com/stainless-code/verdict/internal/record"
	"github.com/stainless-code/verdict/internal/report"
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

// envHome is the HOME fallback key for hermetic test runs.
const envHome = "HOME"

// verbSkill is the skill verb name, shared by the dispatch, arity, and help
// tables.
const verbSkill = "skill"

// verbsHint lists the verbs the CLI owns for the unknown-cmd message. It
// names the same set as the usage synopsis (TestVerbsHintMatchesUsage).
const verbsHint = "fetch|fetch-local|record|adopt|fix|post|triage|report|next|show|" +
	"check-tree|verify|doctor|calibrate|ready|threads|trend|learn|init|skill|upgrade|uninstall|mcp"

func main() {
	//nolint:forbidigo // GO-LAYOUT-02: main is the only os.Exit caller by design
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv, os.ReadFile))
}

// run executes the CLI with injected I/O and environment for testability,
// returning the process exit code.
//
//nolint:revive // the hidden transport dispatch adds one branch to the front door
func run(args []string, stdout, stderr *os.File, getenv func(string) string, readFile func(string) ([]byte, error)) int {
	// Hidden transport verbs run before usage/arity/prefs and never appear
	// in the usage block.
	if code, ok := dispatchHidden(args, stderr); ok {
		return code
	}
	// Installed binaries ship no package.json: the linked release version
	// (if any) owns the footer before any flag handling runs.
	provenance.SetBuildVersion(strings.TrimPrefix(version, "v"))
	// `mcp` keeps its own flag grammar, so it is dispatched before the
	// review arity gate; the top-level usage block documents the
	// experimental entry points without routing through splitCommand.
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
	cmd, repo, pr, args := splitCommand(args)
	// The arity gate runs before any settings, store, or network read:
	// a missing verb or positional is a usage error (exit 2) even when
	// the settings file is corrupt or the network is dead.
	if arityUsage(cmd, repo, pr) {
		fmt.Fprint(stderr, usageText())
		return cli.ExitUsage
	}
	r := newRunner(cmd, repo, pr, stdout, stderr, getenv)
	peekJSONMode(r, args)
	env := envMap(getenv)
	if code, done := resolveRunnerPrefs(r, env, readFile); done {
		return code
	}
	rest, forces, code, done := stripRunnerFlags(r, args)
	if done {
		return code
	}
	if err := cli.ApplyGlobalForces(r, forces, noColorForFallback(r, env)); err != nil {
		r.RouteHuman()
		return r.Refuse(err)
	}
	return dispatch(cmd, r, repo, pr, root, env, forces, rest, stderr)
}

// handleMetaFlags handles -h/--help and --version.
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

// peekJSONMode enables JSON mode when --json precedes any `--`.
func peekJSONMode(r *cli.Runner, args []string) {
	r.JSONPeeked = cli.PeekJSON(args)
	if r.JSONPeeked {
		r.JSONMode = true
	}
}

// noColorForFallback resolves the NO_COLOR convention for the color
// fallback: an explicitly set (even empty) NO_COLOR is respected; in --json
// mode an unset NO_COLOR defaults on (machine output never carries color).
func noColorForFallback(r *cli.Runner, env map[string]string) bool {
	if r.EnvNoColor {
		return true
	}
	if _, set := env["NO_COLOR"]; !set && r.JSONMode {
		return true
	}
	return false
}

// dispatchHidden runs the transport-only verbs: the Windows replacement helper
// and the schema checker behind `schemas/validate.sh`. ok is false for a real
// verb, which then goes through usage, arity, and the normal dispatch.
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

// readHandler is the shared signature of the read-only verbs.
type readHandler func(context.Context, *cli.Runner, []string, readcommon.Request) int

// cmdTrend names the trend verb (revive goconst: no repeated literals).
const cmdTrend = "trend"

// readVerbs maps each read-only verb to its handler.
var readVerbs = map[string]readHandler{
	"report":     report.Run,
	"next":       report.RunNext,
	cmdShow:      report.RunShow,
	"ready":      ready.Run,
	"threads":    threads.Run,
	cmdTrend:     trend.Run,
	"calibrate":  calibrate.Run,
	"doctor":     doctor.Run,
	"check-tree": checktree.Run,
}

// dispatch runs the verb or reports it unknown.
func dispatch(
	cmd string, r *cli.Runner, repo, pr, root string, env map[string]string,
	forces *cli.GlobalForces, rest []string, stderr *os.File,
) int {
	r.RouteHuman()
	// SIGKILL cannot be caught, so SIGTERM (kill, docker stop, systemd) is the
	// second catchable shutdown signal (GO-CLI-05).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if h, ok := readVerbs[cmd]; ok {
		return h(ctx, r, rest, readcommon.Request{
			Runtime: &core.Runtime{Env: env, Now: r.Now},
			Repo:    repo, Pr: pr, Forces: forces, Root: &root,
		})
	}
	if code, ok := dispatchMutating(ctx, cmd, r, repo, pr, root, env, forces, rest); ok {
		return code
	}
	fmt.Fprintf(stderr, "%s\n", unknownCmdMessage(cmd))
	return cli.ExitUsage
}

// dispatchMutating runs a store-writing verb; ok is false for unknown verbs.
//
//nolint:cyclop // the verb table grows one branch per verb by design
func dispatchMutating(
	ctx context.Context, cmd string, r *cli.Runner, repo, pr, root string,
	env map[string]string, forces *cli.GlobalForces, rest []string,
) (code int, ok bool) {
	if code, ok := dispatchFetch(ctx, cmd, r, repo, pr, root, env, rest); ok {
		return code, true
	}
	switch cmd {
	case "record":
		return record.Run(ctx, r, rest, record.Request{Repo: repo, Pr: pr, Root: root, Env: env, Forces: forces}), true
	case "post":
		return record.RunPost(ctx, r, rest, record.Request{Repo: repo, Pr: pr, Root: root, Env: env, Forces: forces}), true
	case "triage":
		return triage.Run(ctx, r, rest, triage.Request{Repo: repo, Pr: pr, Root: root, Env: env}), true
	case "adopt":
		return adopt.Run(ctx, r, rest, adopt.Request{Repo: repo, Pr: pr, Root: root, Env: env}), true
	case "fix":
		return fix.Run(ctx, r, rest, fix.Request{Repo: repo, Pr: pr, Root: root, Env: env}), true
	case "learn":
		return learn.Run(ctx, r, rest, env), true
	case "init":
		return scaffold.Run(ctx, r, rest, scaffold.Request{Env: env}), true
	case verbSkill:
		return runSkill(ctx, r, rest, env, root), true
	case "upgrade":
		return runUpgrade(ctx, r, rest, env), true
	case "uninstall":
		return runUninstall(ctx, r, rest, env), true
	case "verify":
		return verify.Run(ctx, r, rest, verify.Request{Repo: repo, Pr: pr, Root: root, Env: env}), true
	default:
		return 0, false
	}
}

// dispatchFetch runs the bundle-builder verbs; ok is false for other verbs.
func dispatchFetch(
	ctx context.Context, cmd string, r *cli.Runner, repo, pr, root string,
	env map[string]string, rest []string,
) (code int, ok bool) {
	req := fetch.Request{Repo: repo, Pr: pr, Root: root, Env: env}
	switch cmd {
	case "fetch":
		return fetch.Run(ctx, r, rest, req), true
	case "fetch-local":
		return fetch.RunLocal(ctx, r, rest, req), true
	default:
		return 0, false
	}
}

// splitCommand separates the verb and repo/pr positionals from flags.
// Bare verbs (check-tree, doctor, learn, init, install) take no
// positionals, so every arg after the verb is flags. trend takes only a
// repo. Every other verb takes repo plus pr.
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
	repo, pr, rest = splitPositionals(args)
	return cmd, repo, pr, rest
}

// splitPositionals separates repo/pr positionals from flags.
func splitPositionals(args []string) (repo, pr string, rest []string) {
	if len(args) > 0 {
		repo = args[0]
	}
	if len(args) > 1 {
		pr = args[1]
	}
	if len(args) >= 2 {
		return repo, pr, args[2:]
	}
	return repo, pr, nil
}

// newRunner builds the runner with environment-derived fields.
func newRunner(cmd, repo, pr string, stdout, stderr *os.File, getenv func(string) string) *cli.Runner {
	return &cli.Runner{
		Cmd: cmd, Repo: repo, Pr: pr,
		Out: stdout, Diag: stderr, Human: stdout,
		Now:                time.Now,
		EnvNoColor:         getenv("NO_COLOR") != "",
		EnvReviewColorMode: getenv("REVIEW_COLOR_MODE"),
	}
}

// resolveRunnerPrefs resolves prefs, refusing with defaults on failure.
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

// stripRunnerFlags strips global flags, reporting usage/refusal failures.
func stripRunnerFlags(r *cli.Runner, args []string) ([]string, *cli.GlobalForces, int, bool) {
	rest, forces, uerr, ref := cli.StripGlobal(args)
	if uerr != nil {
		r.RouteHuman()
		return nil, nil, r.ReportUsage(uerr), true
	}
	if ref != nil {
		r.RouteHuman()
		return nil, nil, r.Refuse(ref), true
	}
	return rest, forces, 0, false
}

// exeRoot resolves the checkout root from the directory holding this binary,
// walking up to the checkout
// carrying package.json so a conventional bin/verdict build (see the
// Makefile) still finds package.json and prompts/ at the checkout root. A
// relocated binary with no checkout above it keeps the binary directory, so
// hermetic harnesses can plant the provenance inputs beside it.
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

// checkoutDepth caps the walk-up from the binary directory to the checkout.
const checkoutDepth = 5

// checkoutRoot walks up from dir to the checkout carrying
// package.json, falling back to dir when none is found.
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

// envMap snapshots the process environment names the CLI reads. Presence
// tracks LookupEnv so `export X=` reads the same as unset everywhere the
// shell tests `-n` (all knobs), while HOME falls back to the cwd like
// `${HOME:-$PWD}`.
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

// versionOf reads the tool version beside the binary root.
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
