// Command verdict is the Go port of scripts/review.sh, grown verb by verb.
// It exits via a single [os.Exit] call in main.
// The shell entry point stays the front door and dispatches ported verbs
// here; this ticket ports `record` only. main is thin: it parses, builds the
// runner, delegates to run(), and exits once — only main calls [os.Exit].
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/stainless-code/verdict/internal/adopt"
	"github.com/stainless-code/verdict/internal/calibrate"
	"github.com/stainless-code/verdict/internal/checktree"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/doctor"
	"github.com/stainless-code/verdict/internal/fix"
	"github.com/stainless-code/verdict/internal/install"
	"github.com/stainless-code/verdict/internal/learn"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/ready"
	"github.com/stainless-code/verdict/internal/record"
	"github.com/stainless-code/verdict/internal/report"
	"github.com/stainless-code/verdict/internal/scaffold"
	"github.com/stainless-code/verdict/internal/trend"
	"github.com/stainless-code/verdict/internal/triage"
	"github.com/stainless-code/verdict/internal/verify"
)

// envHome is the HOME fallback key for hermetic test runs.
const envHome = "HOME"

// verbsHint lists the verbs the shell entry point dispatches.
const verbsHint = "fetch|fetch-local|record|adopt|fix|post|triage|report|next|show|" +
	"check-tree|verify|doctor|calibrate|ready|trend|learn|init|install"

func main() {
	//nolint:forbidigo // GO-LAYOUT-02: main is the only os.Exit caller by design
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv, os.ReadFile))
}

// run executes the CLI with injected I/O and environment for testability,
// returning the process exit code.
func run(args []string, stdout, stderr *os.File, getenv func(string) string, readFile func(string) ([]byte, error)) int {
	if done := handleMetaFlags(args, stdout, readFile); done {
		return cli.ExitOK
	}
	root := exeRoot()
	cmd, repo, pr, args := splitCommand(args)
	r := newRunner(cmd, repo, pr, stdout, stderr, getenv)
	peekJSONMode(r, args)
	env := envMap(getenv)
	if code, done := resolveRunnerPrefs(r, env, readFile); done {
		return code
	}
	rest, forces, code, done := stripRunnerFlags(r, args, cmd)
	if done {
		return code
	}
	if err := cli.ApplyGlobalForces(r, forces, noColorForFallback(r, env)); err != nil {
		routeHuman(r)
		return r.Refuse(err)
	}
	return dispatch(cmd, r, repo, pr, root, env, forces, rest, stderr)
}

// handleMetaFlags handles -h/--help and --version.
func handleMetaFlags(args []string, stdout *os.File, readFile func(string) ([]byte, error)) bool {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(stdout, usage())
		return true
	}
	if len(args) > 0 && args[0] == "--version" {
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
// mode an unset NO_COLOR defaults on, mirroring json_redirect (I11).
func noColorForFallback(r *cli.Runner, env map[string]string) bool {
	if r.EnvNoColor {
		return true
	}
	if _, set := env["NO_COLOR"]; !set && r.JSONMode {
		return true
	}
	return false
}

// dispatch runs the verb or reports it unknown.
func dispatch(cmd string, r *cli.Runner, repo, pr, root string, env map[string]string, forces *cli.GlobalForces, rest []string, stderr *os.File) int {
	routeHuman(r)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	defer stop()
	req := readcommon.Request{Repo: repo, Pr: pr, Root: root, Env: env, Forces: forces}
	switch cmd {
	case "record":
		return record.Run(ctx, r, rest, record.Request{
			Repo: repo, Pr: pr, Root: root, Env: env,
		})
	case "triage":
		return triage.Run(ctx, r, rest, triage.Request{
			Repo: repo, Pr: pr, Root: root, Env: env,
		})
	case "adopt":
		return adopt.Run(ctx, r, rest, adopt.Request{
			Repo: repo, Pr: pr, Root: root, Env: env,
		})
	case "fix":
		return fix.Run(ctx, r, rest, fix.Request{
			Repo: repo, Pr: pr, Root: root, Env: env,
		})
	case "learn":
		return learn.Run(ctx, r, rest, env)
	case "init":
		return scaffold.Run(ctx, r, rest, scaffold.Request{Env: env})
	case "install":
		return install.Run(ctx, r, rest, install.Request{Env: env, Root: root})
	case "verify":
		return verify.Run(ctx, r, rest, verify.Request{
			Repo: repo, Pr: pr, Root: root, Env: env,
		})
	case "report":
		return report.Run(ctx, r, rest, req)
	case "next":
		return report.RunNext(ctx, r, rest, req)
	case "show":
		return report.RunShow(ctx, r, rest, req)
	case "ready":
		return ready.Run(ctx, r, rest, req)
	case "trend":
		return trend.Run(ctx, r, rest, req)
	case "calibrate":
		return calibrate.Run(ctx, r, rest, req)
	case "doctor":
		return doctor.Run(ctx, r, rest, req)
	case "check-tree":
		return checktree.Run(ctx, r, rest, req)
	default:
		fmt.Fprintf(stderr, "unknown cmd '%s' (want %s)\n", cmd, verbsHint)
		return cli.ExitUsage
	}
}

// splitCommand separates the verb and repo/pr positionals from flags.
func splitCommand(args []string) (cmd, repo, pr string, rest []string) {
	if len(args) == 0 {
		return "", "", "", args
	}
	cmd, args = args[0], args[1:]
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
		routeHuman(r)
		return r.Refuse(ref), true
	}
	r.Effective = prefs.Effective
	return 0, false
}

// stripRunnerFlags strips global flags, reporting usage/refusal failures.
func stripRunnerFlags(r *cli.Runner, args []string, cmd string) ([]string, *cli.GlobalForces, int, bool) {
	rest, forces, uerr, ref := cli.StripGlobal(args, cmd)
	if uerr != nil {
		routeHuman(r)
		return nil, nil, r.ReportUsage(uerr), true
	}
	if ref != nil {
		routeHuman(r)
		return nil, nil, r.Refuse(ref), true
	}
	return rest, forces, 0, false
}

// routeHuman sends human progress to stderr in JSON mode (stdout carries
// exactly one envelope) and to stdout otherwise, mirroring json_redirect.
func routeHuman(r *cli.Runner) {
	if r.JSONMode {
		r.Human = r.Diag
	} else {
		r.Human = r.Out
	}
}

// exeRoot resolves the checkout root the way the shell resolves its script
// directory: the directory holding this binary, walking up to the checkout
// carrying scripts/review.sh so a conventional bin/verdict build (see the
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
// scripts/review.sh, falling back to dir when none is found.
func checkoutRoot(dir string) string {
	root := dir
	for range checkoutDepth {
		if st, err := os.Stat(filepath.Join(root, "scripts", "review.sh")); err == nil && !st.IsDir() {
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
		"REVIEW_KB_MIN_JUDGED", "REVIEW_KB_MAX_FP_RATE",
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
		return "0.0.0-unknown"
	}
	var doc struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Version == "" {
		return "0.0.0-unknown"
	}
	return doc.Version
}

// usage prints the verb summary.
func usage() string {
	return "usage: verdict triage|adopt|fix|learn|init|install|verify|record " +
		"<owner/repo> <slug> [flags]\n\nverbs ported to Go: record, triage, " +
		"adopt, fix, learn, init, install, verify " +
		"(the shell entry point dispatches; see scripts/review.sh)"
}
