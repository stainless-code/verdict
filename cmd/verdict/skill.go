package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/skill"
	"github.com/stainless-code/verdict/internal/jq"
)

// runSkill dispatches `verdict skill [show|files|install]`.
func runSkill(ctx context.Context, r *cli.Runner, args []string, env map[string]string, root string) int {
	r.Repo, r.Pr = "", ""
	sub := cmdShow
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case cmdShow:
		return runSkillShow(ctx, r, args, env, root)
	case "files":
		return runSkillFiles(ctx, r, args, env, root)
	case cmdSkillInstall:
		return runSkillInstall(ctx, r, args, env, root)
	case flagShortHelp, flagHelp, "help":
		fmt.Fprint(r.Out, skillUsageText())
		return cli.ExitOK
	default:
		return r.ReportUsage(&cli.UsageError{
			Message: fmt.Sprintf("unknown skill command '%s' (want show, files, install)", sub),
		})
	}
}

// runSkillShow prints SKILL.md, or the JSON inventory envelope.
func runSkillShow(ctx context.Context, r *cli.Runner, args []string, env map[string]string, root string) int {
	format, code, done := parseSkillShowFlags(r, args)
	if done {
		return code
	}
	req := &skill.Request{Runtime: skillRuntime(r, env), Version: skillVersion(root)}
	if format == formatJSONName {
		inv, ref := skill.List(ctx, req)
		if ref != nil {
			return r.Refuse(ref)
		}
		writeSkillEnvelope(r, skillInventoryData(&inv))
		return cli.ExitOK
	}
	body, ref := skill.Show(ctx, req)
	if ref != nil {
		return r.Refuse(ref)
	}
	fmt.Fprint(r.Out, string(body))
	return cli.ExitOK
}

// parseSkillShowFlags parses the show format grammar.
//
//nolint:revive // the flag switch is the parser; splitting it hides the grammar
func parseSkillShowFlags(r *cli.Runner, args []string) (string, int, bool) {
	format := skillFormat(r)
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--format":
			if i+1 >= len(args) {
				return "", r.ReportUsage(cli.MissingValue("--format", "md or json")), true
			}
			i++
			format = args[i]
		case strings.HasPrefix(arg, "--format="):
			format = strings.TrimPrefix(arg, "--format=")
		case arg == flagShortHelp || arg == flagHelp:
			fmt.Fprint(r.Out, skillUsageText())
			return "", cli.ExitOK, true
		default:
			return "", r.ReportUsage(&cli.UsageError{
				Message: fmt.Sprintf("unknown flag '%s' for skill (want --format md|json, --json)", arg),
			}), true
		}
	}
	if format != "md" && format != formatJSONName {
		return "", r.ReportUsage(&cli.UsageError{Message: "bad --format '" + format + "' (want md or json)"}), true
	}
	if r.JSONMode && format != formatJSONName {
		return "", r.ReportUsage(&cli.UsageError{Message: "--json cannot be combined with --format md"}), true
	}
	return format, 0, false
}

// runSkillFiles lists every embedded path (or the JSON inventory envelope).
func runSkillFiles(ctx context.Context, r *cli.Runner, args []string, env map[string]string, root string) int {
	for _, arg := range args {
		switch arg {
		case flagShortHelp, flagHelp:
			fmt.Fprint(r.Out, skillUsageText())
			return cli.ExitOK
		default:
			return r.ReportUsage(&cli.UsageError{
				Message: fmt.Sprintf("unknown flag '%s' for skill files (want --json)", arg),
			})
		}
	}
	inv, ref := skill.List(ctx, &skill.Request{Runtime: skillRuntime(r, env), Version: skillVersion(root)})
	if ref != nil {
		return r.Refuse(ref)
	}
	if r.JSONMode {
		writeSkillEnvelope(r, skillInventoryData(&inv))
		return cli.ExitOK
	}
	for _, file := range inv.Files {
		fmt.Fprintln(r.Out, file)
	}
	return cli.ExitOK
}

// skillInstallFlags is the parsed `skill install` option set.
type skillInstallFlags struct {
	dir   string
	agent string
	force bool
	dry   bool
}

// runSkillInstall previews or applies the embedded payload.
func runSkillInstall(ctx context.Context, r *cli.Runner, args []string, env map[string]string, root string) int {
	flags, code, done := parseSkillInstallFlags(r, args)
	if done {
		return code
	}
	if flags.dir != "" && flags.agent != "" {
		return r.ReportUsage(&cli.UsageError{Message: "--dir and --agent are mutually exclusive"})
	}
	r.Rentry = "verdict skill install"
	if flags.dry {
		r.Rentry += entryDryRun
	}
	if r.JSONMode {
		r.Rentry += entryJSON
	}
	plan, ref := skill.Install(ctx, &skill.Request{
		Runtime: skillRuntime(r, env),
		Version: skillVersion(root),
		Dir:     flags.dir,
		Agent:   flags.agent,
		Force:   flags.force,
		DryRun:  flags.dry,
	})
	if ref != nil {
		return r.Refuse(ref)
	}
	printSkillPlan(r, &plan, flags.dry)
	if r.JSONMode {
		writeSkillEnvelope(r, skillInstallData(&plan, flags.dry, flags.force))
	}
	return cli.ExitOK
}

// parseSkillInstallFlags strips install's flags, reporting unknown ones.
//
//nolint:cyclop,revive // the flag switch is the parser; splitting it hides the grammar
func parseSkillInstallFlags(r *cli.Runner, args []string) (skillInstallFlags, int, bool) {
	var f skillInstallFlags
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--dir" || arg == "--agent":
			if i+1 >= len(args) {
				return f, r.ReportUsage(cli.MissingValue(arg, "")), true
			}
			i++
			if arg == "--dir" {
				f.dir = args[i]
			} else {
				f.agent = args[i]
			}
		case strings.HasPrefix(arg, "--dir="):
			f.dir = strings.TrimPrefix(arg, "--dir=")
		case strings.HasPrefix(arg, "--agent="):
			f.agent = strings.TrimPrefix(arg, "--agent=")
		case arg == "--force":
			f.force = true
		case arg == flagDryRun:
			f.dry = true
		case arg == flagShortHelp || arg == flagHelp:
			fmt.Fprint(r.Out, skillUsageText())
			return f, cli.ExitOK, true
		default:
			return f, r.ReportUsage(&cli.UsageError{
				Message: fmt.Sprintf("unknown flag '%s' for skill install (want --dir, --agent, --force, --dry-run, --json)", arg),
			}), true
		}
	}
	return f, 0, false
}

// printSkillPlan renders the install preview/outcome in human mode.
func printSkillPlan(r *cli.Runner, plan *skill.Plan, dry bool) {
	fmt.Fprintf(r.Human, "skill install: %s v%s -> %s\n", plan.Name, plan.Version, plan.Target)
	fmt.Fprintf(r.Human, "--- %s (%s) ---\n", plan.Target, plan.Status)
	for _, rel := range plan.Changed {
		fmt.Fprintf(r.Human, "  + %s\n", rel)
	}
	switch plan.Status {
	case skill.StatusKeep:
		printSkillKeep(r, plan)
	case skill.StatusBlocked:
		printSkillBlocked(r, plan)
	default:
		printSkillWrote(r, plan)
	}
	if dry {
		fmt.Fprintln(r.Human, "dry-run: nothing changed")
	}
}

// printSkillKeep renders a keep outcome (and a receipt refresh note).
func printSkillKeep(r *cli.Runner, plan *skill.Plan) {
	if plan.ReceiptUpdated {
		fmt.Fprintf(r.Human, "install: keep (receipt refreshed to v%s)\n", plan.Version)
		return
	}
	fmt.Fprintln(r.Human, "install: keep (already up to date)")
}

// printSkillBlocked renders the foreign-target preview/refusal context.
func printSkillBlocked(r *cli.Runner, plan *skill.Plan) {
	if plan.TargetFile {
		fmt.Fprintf(r.Human, "install: blocked (%s is not a directory — left untouched)\n", plan.Target)
		return
	}
	fmt.Fprintf(r.Human,
		"install: blocked (no %s receipt — foreign, left untouched; --force writes only verdict's payload files)\n",
		skill.ReceiptName)
}

// printSkillWrote renders a create/update result.
func printSkillWrote(r *cli.Runner, plan *skill.Plan) {
	if plan.Written > 0 {
		fmt.Fprintf(r.Human, "install: %d file(s) written (receipt v%s)\n", plan.Written, plan.ReceiptVersion)
	}
}

// skillFormat is the JSON-forced default shared by skill read commands.
func skillFormat(r *cli.Runner) string {
	if r.JSONMode {
		return formatJSONName
	}
	return "md"
}

// skillVersion is the served skill version: the linked binary version, else
// package.json beside the checkout, matching `verdict --version`.
func skillVersion(root string) string {
	if v := strings.TrimPrefix(version, "v"); v != "" {
		return v
	}
	return versionOf(root, os.ReadFile)
}

// skillRuntime builds the injected service runtime.
func skillRuntime(r *cli.Runner, env map[string]string) *core.Runtime {
	return &core.Runtime{Env: env, Now: r.Now, Progress: runnerProgress{r}}
}

// skillInventoryData builds the name/version/files envelope data.
func skillInventoryData(inv *skill.Inventory) *jq.Obj {
	return jq.NewObj(
		"name", inv.Name,
		"version", inv.Version,
		"files", core.StringValues(inv.Files),
	)
}

// skillInstallData builds the install envelope data.
func skillInstallData(plan *skill.Plan, dry, force bool) *jq.Obj {
	data := jq.NewObj(
		"name", plan.Name,
		"version", plan.Version,
		"target", plan.Target,
		"status", plan.Status,
		"dry_run", dry,
		"force", force,
		"written", plan.Written,
		"files", core.StringValues(plan.Files),
		"changed", core.StringValues(plan.Changed),
	)
	if plan.ReceiptVersion != "" {
		data.Set("receipt_version", plan.ReceiptVersion)
	}
	return data
}

// writeSkillEnvelope prints exactly one standard envelope for a skill call.
func writeSkillEnvelope(r *cli.Runner, data *jq.Obj) {
	env := core.RenderEnvelope("skill", "", "", cli.NowUTC(r.Now()), "",
		r.Warnings, data, nil)
	fmt.Fprintln(r.Out, env.MarshalPretty())
}

// skillUsageText is the `verdict skill` help block.
func skillUsageText() string {
	return "usage: verdict skill [show|files|install] [opts]\n" +
		"  skill [show]              print SKILL.md (default --format md)\n" +
		"  skill --format json       one envelope: {name, version, files}\n" +
		"  skill files               list every embedded payload path\n" +
		"  skill install [--dir <skills-dir>|--agent <name>] [--force] [--dry-run] [--json]\n" +
		"      atomically write SKILL.md, prompts/, schemas/, and scripts/verdict plus\n" +
		"      a .verdict-managed receipt. Previews create/update/keep/blocked;\n" +
		"      only --json is guaranteed non-interactive (this command never prompts).\n" +
		"      A target without a verdict receipt refuses typed unless --force;\n" +
		"      foreign files are never touched.\n" +
		"  agents: agents, claude, codex, cursor, opencode, copilot, gemini, goose, qwen, windsurf\n" +
		"  The skill is embedded in this binary, so the served copy can never skew.\n" +
		"  In a skill copy, scripts/verdict resolves VERDICT_BIN, then <skill>/bin/verdict,\n" +
		"  then verdict on PATH; otherwise run <skill>/scripts/verdict bootstrap once\n" +
		"  (verified; never a review-time fetch).\n"
}
