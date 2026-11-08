// Package configcmd is the config CLI adapter: argv parsing (same usage
// text and exit 2), one call into the config service, envelope rendering,
// and refusal-vs-cancel exit mapping. The service performs no live calls and
// no writes; nothing here touches the bundle beyond reading it. Resolving the
// root runs one `git rev-parse` probe outside the injected commander.
package configcmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request carries the resolved config invocation.
type Request = readcommon.Request

// Run executes `config <owner/repo> <pr> (--explain | --preview
// [--file <path>]) [--json]`, returning the process exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	flags, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if code, done := r.Canceled(ctx, "config"); done {
		return code
	}
	settings := cli.SettingsPath(reqEnv(req, "HOME"), reqEnv(req, "XDG_CONFIG_HOME"))
	resp, ref := config.Run(ctx, &core.ConfigRequest{
		ReadRequest:  req,
		Flags:        flags,
		SettingsPath: settings,
		Warnings:     append([]string{}, r.Warnings...),
	})
	if ref != nil {
		return r.RefuseTyped(ref)
	}
	r.Repo, r.Pr = resp.Repo, resp.Pr
	return renderConfig(r, resp)
}

func reqEnv(req Request, key string) string {
	if req.Runtime == nil || req.Env == nil {
		return ""
	}
	return req.Env[key]
}

// renderConfig renders a config success: JSON mode prints exactly one
// envelope to stdout, human mode prints the policy lines.
func renderConfig(r *cli.Runner, resp *core.ConfigResponse) int {
	r.Warnings = resp.Warnings
	if r.JSONMode {
		env := core.RenderEnvelope("config", resp.Repo, resp.Pr, resp.At, resp.Next,
			resp.Warnings, resp.Data.Wire(), nil)
		fmt.Fprint(r.Out, jq.PrettyOrdered(env))
		return cli.ExitOK
	}
	for _, line := range humanLines(resp.Data) {
		fmt.Fprintln(r.Out, line)
	}
	return cli.ExitOK
}

// humanLines renders the policy in the fixed order: trust and source,
// values with origins, requirements, plan, spend, privacy, and the
// observed checks on explain.
func humanLines(data *core.ConfigData) []string {
	lines := []string{fmt.Sprintf("config %s (trusted=%v): %s", data.Mode, data.Trusted, data.Source)}
	for _, value := range data.Values {
		lines = append(lines, fmt.Sprintf("  %s = %s (from %s)", value.Key, jq.Compact(value.Value), value.Source))
	}
	for _, req := range data.Requirements {
		lines = append(lines, fmt.Sprintf("  require %s [%s] via %s", req.Key, req.Status, strings.Join(req.Sources, ", ")))
	}
	for i := range data.Plan {
		group := &data.Plan[i]
		lines = append(lines, fmt.Sprintf("  plan: id=%s profile=%s depth=%s checks=[%s] files=%d",
			orDash(group.ID), orDash(group.Profile), orDash(group.Depth),
			strings.Join(group.Checks, ","), len(group.Files)))
	}
	lines = append(lines,
		fmt.Sprintf("  spend: guard=%s limit=%s", usd(data.Spend.GuardUSD), usd(data.Spend.PersonalLimit)),
		"  privacy: "+exclusions(data.Privacy),
	)
	for _, check := range data.Checks {
		lines = append(lines, fmt.Sprintf("  observed %s/%s: %v (%s)", check.Check, check.Group, check.Passed, check.At))
	}
	return lines
}

// exclusions renders the personal privacy exclusions, or the explicit
// none marker so an empty set never reads as unreported.
func exclusions(list []string) string {
	if len(list) == 0 {
		return "(none)"
	}
	return strings.Join(list, ", ")
}

// orDash renders an empty profile or depth as the unset marker.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func usd(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("$%v", *v)
}

// parseFlags strips config's flags: exactly one of --explain or
// --preview, with --file only on preview. Anything else is a usage
// error.
func parseFlags(r *cli.Runner, args []string) (core.ConfigFlags, int, bool) {
	flags := core.ConfigFlags{}
	for i := 0; i < len(args); i++ {
		next, ref := parseFlag(&flags, args, i)
		if ref != nil {
			return flags, r.ReportUsage(ref), true
		}
		i = next
	}
	if ref := validateModes(flags); ref != nil {
		return flags, r.ReportUsage(ref), true
	}
	return flags, 0, false
}

// parseFlag reads args[i], returning the index of its last consumed
// argument and the usage error for anything config does not accept.
func parseFlag(flags *core.ConfigFlags, args []string, i int) (int, *cli.UsageError) {
	switch arg := args[i]; arg {
	case "--explain":
		flags.Explain = true
		return i, nil
	case "--preview":
		flags.Preview = true
		return i, nil
	case "--file":
		value, next, ref := cli.FlagValue(args, i, arg, "<path>")
		if ref != nil {
			return i, ref
		}
		flags.File = value
		return next, nil
	default:
		return i, &cli.UsageError{Message: unknownFlagMessage(arg)}
	}
}

// unknownFlagMessage names the rejected flag next to the accepted set.
func unknownFlagMessage(arg string) string {
	return fmt.Sprintf("unknown flag '%s' for config (want --explain | --preview [--file <path>], --json)", arg)
}

// validateModes enforces config's mode gate once the flags are read:
// exactly one of --explain or --preview, with --file only on preview.
func validateModes(flags core.ConfigFlags) *cli.UsageError {
	if flags.Explain == flags.Preview {
		return &cli.UsageError{Message: "config wants exactly one of --explain or --preview"}
	}
	if flags.File != "" && !flags.Preview {
		return &cli.UsageError{Message: "config --file needs --preview"}
	}
	return nil
}
