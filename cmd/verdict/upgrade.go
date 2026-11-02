package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/upgrade"
	"github.com/stainless-code/verdict/internal/jq"
)

// upgradeFlags is the parsed `upgrade`/`uninstall` option set.
type upgradeFlags struct {
	version    string
	dryRun     bool
	removePath bool
}

// runUpgrade dispatches `verdict upgrade`.
func runUpgrade(ctx context.Context, r *cli.Runner, args []string, env map[string]string) int {
	flags, code, done := parseUpgradeFlags(r, "upgrade", args, false)
	if done {
		return code
	}
	return finishUpgrade(ctx, r, env, flags)
}

// runUninstall dispatches `verdict uninstall`.
func runUninstall(ctx context.Context, r *cli.Runner, args []string, env map[string]string) int {
	flags, code, done := parseUpgradeFlags(r, "uninstall", args, true)
	if done {
		return code
	}
	return finishUninstall(ctx, r, env, flags)
}

// parseUpgradeFlags strips both verbs' flags, reporting unknown ones.
//
//nolint:cyclop,revive // the flag switch is the parser; splitting it hides the grammar
func parseUpgradeFlags(r *cli.Runner, verb string, args []string, uninstall bool) (upgradeFlags, int, bool) {
	var f upgradeFlags
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--dry-run":
			f.dryRun = true
		case arg == flagJSON:
		case !uninstall && arg == "--version":
			if i+1 >= len(args) {
				return f, r.ReportUsage(&cli.UsageError{Message: "--version needs a value"}), true
			}
			i++
			f.version = args[i]
		case !uninstall && strings.HasPrefix(arg, "--version="):
			f.version = strings.TrimPrefix(arg, "--version=")
		case uninstall && arg == "--remove-path":
			f.removePath = true
		default:
			want := "--version <v>, --dry-run, --json"
			if uninstall {
				want = "--dry-run, --remove-path, --json"
			}
			return f, r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf("unknown argument '%s' for %s (want %s)", arg, verb, want)}), true
		}
	}
	return f, 0, false
}

// finishUpgrade runs the service and renders its outcome.
func finishUpgrade(ctx context.Context, r *cli.Runner, env map[string]string, f upgradeFlags) int {
	r.Repo, r.Pr = "", ""
	r.Rentry = "verdict upgrade"
	if f.version != "" {
		r.Rentry += " --version " + f.version
	}
	if f.dryRun {
		r.Rentry += entryDryRun
	}
	if r.JSONMode {
		r.Rentry += entryJSON
	}
	req := upgrade.Request{
		Runtime:    &core.Runtime{Env: env, Now: r.Now, Progress: runnerProgress{r}},
		Executable: "",
		PinVersion: f.version,
		DryRun:     f.dryRun,
		Stdout:     r.Diag,
		Stderr:     r.Diag,
		EnvSlice:   os.Environ(),
		LookPath:   exec.LookPath,
	}
	resp, ref := upgrade.Run(ctx, &req)
	if ref != nil {
		return finishUpgradeRefusal(r, ref, resp.ExitCode)
	}
	switch {
	case resp.DryRun:
		printUpgradePlan(r, &resp)
	case resp.Channel == upgrade.ChannelInstaller:
		printSelfReplace(r, &resp)
	default:
		fmt.Fprintln(r.Human, "upgrade: update complete")
	}
	if r.JSONMode {
		writeUpgradeEnvelope(r, "upgrade", upgradeData(&resp))
	}
	return cli.ExitOK
}

// finishUninstall runs the service and renders its outcome.
func finishUninstall(ctx context.Context, r *cli.Runner, env map[string]string, f upgradeFlags) int {
	r.Repo, r.Pr = "", ""
	r.Rentry = "verdict uninstall"
	if f.dryRun {
		r.Rentry += entryDryRun
	}
	if f.removePath {
		r.Rentry += " --remove-path"
	}
	if r.JSONMode {
		r.Rentry += entryJSON
	}
	req := upgrade.Request{
		Runtime:    &core.Runtime{Env: env, Now: r.Now, Progress: runnerProgress{r}},
		DryRun:     f.dryRun,
		RemovePath: f.removePath,
		EnvSlice:   os.Environ(),
	}
	resp, ref := upgrade.Uninstall(ctx, &req)
	if ref != nil {
		return finishUpgradeRefusal(r, ref, 0)
	}
	printUninstall(r, &resp)
	if r.JSONMode {
		writeUpgradeEnvelope(r, "uninstall", uninstallData(&resp))
	}
	return cli.ExitOK
}

// finishUpgradeRefusal renders a typed refusal, propagating a manager exit
// code when the failure came from the owning package manager.
func finishUpgradeRefusal(r *cli.Runner, ref *cli.RefusalError, exit int) int {
	if exit <= 0 {
		exit = cli.ExitRefused
	}
	return r.RefuseExit(ref, exit)
}

// printUpgradePlan renders the dry-run plan; it touches nothing.
func printUpgradePlan(r *cli.Runner, resp *upgrade.Response) {
	fmt.Fprintf(r.Human, "upgrade: channel: %s (%s)\n", resp.Channel, resp.Target)
	switch {
	case resp.Action == upgrade.ActionRefuse:
		fmt.Fprintf(r.Human, "upgrade: action: refuse — %s\n", resp.Reason)
	case len(resp.Command) > 0:
		fmt.Fprintf(r.Human, "upgrade: action: %s\n", resp.Action)
		fmt.Fprintf(r.Human, "upgrade: would run: %s\n", strings.Join(resp.Command, " "))
	default:
		fmt.Fprintf(r.Human, "upgrade: action: %s %s\n", resp.Action, resp.Version)
	}
	fmt.Fprintln(r.Human, "upgrade: dry-run: nothing changed")
}

// printSelfReplace renders the synchronous or Windows-handoff outcome.
func printSelfReplace(r *cli.Runner, resp *upgrade.Response) {
	if resp.Handoff {
		fmt.Fprintf(r.Human, "upgrade: verdict %s staged; the replacement helper finishes after this process exits\n", resp.Version)
		fmt.Fprintf(r.Human, "upgrade: result %s (log %s)\n", resp.HelperResult, resp.HelperLog)
		return
	}
	fmt.Fprintf(r.Human, "upgrade: installed verdict %s to %s (SHA-256 verified)\n", resp.Version, resp.Target)
}

// printUninstall renders the removed paths.
func printUninstall(r *cli.Runner, resp *upgrade.Response) {
	if resp.DryRun {
		fmt.Fprintf(r.Human, "uninstall: would remove: %s\n", strings.Join(resp.Removed, ", "))
		fmt.Fprintln(r.Human, "uninstall: dry-run: nothing changed")
		return
	}
	fmt.Fprintf(r.Human, "uninstall: removed %s\n", strings.Join(resp.Removed, ", "))
}

// upgradeData builds the ordered upgrade --json data object.
func upgradeData(resp *upgrade.Response) *jq.Obj {
	data := jq.NewObj(
		"channel", resp.Channel,
		"action", resp.Action,
		"version", resp.Version,
		"target", resp.Target,
		"dry_run", resp.DryRun,
		"handoff", resp.Handoff,
	)
	if resp.SHA256 != "" {
		data.Set("sha256", resp.SHA256)
	}
	if resp.Reason != "" {
		data.Set("reason", resp.Reason)
	}
	if len(resp.Command) > 0 {
		data.Set("command", stringSlice(resp.Command))
	}
	if resp.HelperResult != "" {
		data.Set("helper_result", resp.HelperResult)
		data.Set("helper_log", resp.HelperLog)
	}
	return data
}

// uninstallData builds the ordered uninstall --json data object.
func uninstallData(resp *upgrade.Response) *jq.Obj {
	return jq.NewObj(
		"channel", resp.Channel,
		"action", resp.Action,
		"target", resp.Target,
		"removed", stringSlice(resp.Removed),
		"path_line_removed", resp.PathLineRemoved,
		"dry_run", resp.DryRun,
	)
}

// writeUpgradeEnvelope prints exactly one envelope for a successful call.
func writeUpgradeEnvelope(r *cli.Runner, verb string, data *jq.Obj) {
	env := jq.NewObj(
		"schema_version", "1",
		"verb", verb,
		"repo", "",
		"pr", nil,
		"at", cli.NowUTC(r.Now()),
		"next", "",
		"warnings", cliWarnings(r.Warnings),
		"data", data,
		"error", nil,
	)
	fmt.Fprintln(r.Out, env.MarshalPretty())
}

// cliWarnings renders the accumulated warnings as JSON strings.
func cliWarnings(warnings []string) []any {
	out := make([]any, 0, len(warnings))
	for _, w := range warnings {
		out = append(out, w)
	}
	return out
}

// stringSlice renders a Go string slice as a JSON array.
func stringSlice(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

// runnerProgress adapts cli.Runner to core.Progress.
type runnerProgress struct {
	r *cli.Runner
}

// Human implements core.Progress.
func (p runnerProgress) Human(line string) { fmt.Fprintln(p.r.Human, line) }

// Warn implements core.Progress.
func (p runnerProgress) Warn(msg string) { p.r.Warn(msg) }

// Diag implements core.Progress.
func (p runnerProgress) Diag(line string) { fmt.Fprintln(p.r.Diag, line) }
