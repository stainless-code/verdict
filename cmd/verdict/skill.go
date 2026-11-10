package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/skill"
	"github.com/stainless-code/verdict/internal/jq"
)

const keyFiles = "files"

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
	case cmdSkillStatus:
		return runSkillStatus(ctx, r, args, env, root)
	case cmdSkillInstall:
		return runSkillInstall(ctx, r, args, env, root)
	case cmdSkillUninstall:
		return runSkillUninstall(ctx, r, args, env, root)
	case flagShortHelp, flagHelp, "help":
		fmt.Fprint(r.Out, skillUsageText())
		return cli.ExitOK
	default:
		return r.ReportUsage(&cli.UsageError{
			Message: fmt.Sprintf("unknown skill command '%s' (want show, files, status, install, uninstall)", sub),
		})
	}
}

//
//nolint:revive // the show route nests the path, inventory, and SKILL.md arms
func runSkillShow(ctx context.Context, r *cli.Runner, args []string, env map[string]string, root string) int {
	flags, code, done := parseSkillShowFlags(r, args)
	if done {
		return code
	}
	req := &skill.Request{Runtime: skillRuntime(r, env), Version: skillVersion(root)}
	if flags.path != "" {
		body, ref := skill.ShowFile(ctx, req, flags.path)
		if ref != nil {
			return r.Refuse(ref)
		}
		if flags.format == formatJSONName {
			writeSkillEnvelope(r, skillFileData(flags.path, body))
			return cli.ExitOK
		}
		fmt.Fprint(r.Out, string(body))
		return cli.ExitOK
	}
	if flags.format == formatJSONName {
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

type skillShowFlags struct {
	format string
	path   string
}

//
//nolint:cyclop,revive // the flag switch is the parser; splitting it hides the grammar
func parseSkillShowFlags(r *cli.Runner, args []string) (skillShowFlags, int, bool) {
	f := skillShowFlags{format: skillFormat(r)}
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--format":
			if i+1 >= len(args) {
				return f, r.ReportUsage(cli.MissingValue("--format", "md or json")), true
			}
			i++
			f.format = args[i]
		case strings.HasPrefix(arg, "--format="):
			f.format = strings.TrimPrefix(arg, "--format=")
		case arg == flagShortHelp || arg == flagHelp:
			fmt.Fprint(r.Out, skillUsageText())
			return f, cli.ExitOK, true
		case !strings.HasPrefix(arg, "-"):
			if f.path != "" {
				return f, r.ReportUsage(&cli.UsageError{Message: "skill show takes at most one path"}), true
			}
			f.path = arg
		default:
			return f, r.ReportUsage(&cli.UsageError{
				Message: fmt.Sprintf("unknown flag '%s' for skill (want --format md|json, --json, or one path)", arg),
			}), true
		}
	}
	if f.format != "md" && f.format != formatJSONName {
		return f, r.ReportUsage(&cli.UsageError{Message: "bad --format '" + f.format + "' (want md or json)"}), true
	}
	if r.JSONMode && f.format != formatJSONName {
		return f, r.ReportUsage(&cli.UsageError{Message: "--json cannot be combined with --format md"}), true
	}
	return f, 0, false
}

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

type skillInstallFlags struct {
	dir    string
	agents []string
	force  bool
	dry    bool
	detect bool
}

func runSkillInstall(ctx context.Context, r *cli.Runner, args []string, env map[string]string, root string) int {
	flags, code, done := parseSkillInstallFlags(r, args)
	if done {
		return code
	}
	if code, bad := skillScope(r, flags.dir, flags.agents, flags.detect); bad {
		return code
	}
	base := "verdict skill install"
	if flags.detect {
		base += " --detect"
	}
	r.Rentry = base + skillInstallSuffix(r, flags)
	return skillInstallRun(ctx, r, env, root, flags)
}

func skillScope(r *cli.Runner, dir string, agents []string, detect bool) (int, bool) {
	switch {
	case dir != "" && len(agents) > 0:
		return r.ReportUsage(&cli.UsageError{Message: "--dir and --agent are mutually exclusive"}), true
	case detect && (dir != "" || len(agents) > 0):
		return r.ReportUsage(&cli.UsageError{Message: "--detect cannot be combined with --dir or --agent"}), true
	default:
		return 0, false
	}
}

func skillInstallSuffix(r *cli.Runner, flags skillInstallFlags) string {
	suffix := ""
	if flags.dry {
		suffix += entryDryRun
	}
	if r.JSONMode {
		suffix += entryJSON
	}
	return suffix
}

func skillInstallRun(ctx context.Context, r *cli.Runner, env map[string]string, root string, flags skillInstallFlags) int {
	req := &skill.Request{
		Runtime: skillRuntime(r, env),
		Version: skillVersion(root),
		Dir:     flags.dir,
		Force:   flags.force,
		DryRun:  flags.dry,
	}
	agents := dedupeStrings(flags.agents)
	switch {
	case flags.detect:
		return runSkillInstallDetect(ctx, r, req, flags)
	case len(agents) > 1:
		return runSkillInstallAgents(ctx, r, req, flags)
	default:
		if len(agents) == 1 {
			req.Agent = agents[0]
		}
		plan, ref := skill.Install(ctx, req)
		if ref != nil {
			return r.Refuse(ref)
		}
		printSkillPlan(r, &plan, flags.dry)
		if r.JSONMode {
			writeSkillEnvelope(r, skillInstallData(&plan, flags.dry, flags.force))
		}
		return cli.ExitOK
	}
}

func runSkillInstallAgents(ctx context.Context, r *cli.Runner, req *skill.Request, flags skillInstallFlags) int {
	det, ref := skill.InstallAgents(ctx, req, flags.agents)
	if ref != nil {
		return r.Refuse(ref)
	}
	printSkillSet(r, "skill install", &det, flags.dry)
	if r.JSONMode {
		writeSkillEnvelope(r, skillSetData(&det, flags.dry, flags.force))
	}
	return cli.ExitOK
}

func runSkillInstallDetect(ctx context.Context, r *cli.Runner, req *skill.Request, flags skillInstallFlags) int {
	det, ref := skill.InstallDetected(ctx, req)
	if ref != nil {
		return r.Refuse(ref)
	}
	printSkillSet(r, "skill install --detect", &det, flags.dry)
	if r.JSONMode {
		writeSkillEnvelope(r, skillSetData(&det, flags.dry, flags.force))
	}
	return cli.ExitOK
}

//
//nolint:cyclop,revive // the flag switch is the parser; splitting it hides the grammar
func parseSkillInstallFlags(r *cli.Runner, args []string) (skillInstallFlags, int, bool) {
	var f skillInstallFlags
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == flagDir || arg == flagAgent:
			if i+1 >= len(args) {
				return f, r.ReportUsage(cli.MissingValue(arg, "")), true
			}
			i++
			if arg == flagDir {
				f.dir = args[i]
			} else {
				f.agents = append(f.agents, args[i])
			}
		case strings.HasPrefix(arg, "--dir="):
			f.dir = strings.TrimPrefix(arg, "--dir=")
		case strings.HasPrefix(arg, "--agent="):
			f.agents = append(f.agents, strings.TrimPrefix(arg, "--agent="))
		case arg == "--force":
			f.force = true
		case arg == "--detect":
			f.detect = true
		case arg == flagDryRun:
			f.dry = true
		case arg == flagShortHelp || arg == flagHelp:
			fmt.Fprint(r.Out, skillUsageText())
			return f, cli.ExitOK, true
		default:
			return f, r.ReportUsage(&cli.UsageError{
				Message: fmt.Sprintf("unknown flag '%s' for skill install (want --dir, --agent, --detect, --force, --dry-run, --json)", arg),
			}), true
		}
	}
	return f, 0, false
}

func printSkillPlan(r *cli.Runner, plan *skill.Plan, dry bool) {
	fmt.Fprintf(r.Human, "skill install: %s v%s -> %s\n", plan.Name, plan.Version, plan.Target)
	fmt.Fprintf(r.Human, "--- %s (%s) ---\n", plan.Target, plan.Status)
	printSkillTarget(r, plan)
	if dry {
		fmt.Fprintln(r.Human, "dry-run: nothing changed")
	}
}

func printSkillSet(r *cli.Runner, label string, det *skill.Detection, dry bool) {
	fmt.Fprintf(r.Human, "%s: %s v%s -> %d agent(s): %s\n",
		label, det.Name, det.Version, len(det.Plans), strings.Join(det.Agents, ", "))
	for i := range det.Plans {
		fmt.Fprintf(r.Human, "--- %s: %s (%s) ---\n", det.Plans[i].Agent, det.Plans[i].Target, det.Plans[i].Status)
		printSkillTarget(r, &det.Plans[i])
	}
	if dry {
		fmt.Fprintln(r.Human, "dry-run: nothing changed")
	}
}

func printSkillTarget(r *cli.Runner, plan *skill.Plan) {
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
}

func printSkillKeep(r *cli.Runner, plan *skill.Plan) {
	if plan.ReceiptUpdated {
		fmt.Fprintf(r.Human, "install: keep (receipt refreshed to v%s)\n", plan.Version)
		return
	}
	fmt.Fprintln(r.Human, "install: keep (already up to date)")
}

func printSkillBlocked(r *cli.Runner, plan *skill.Plan) {
	if plan.TargetFile {
		fmt.Fprintf(r.Human, "install: blocked (%s is not a directory — left untouched)\n", plan.Target)
		return
	}
	fmt.Fprintf(r.Human,
		"install: blocked (no %s receipt — foreign, left untouched; --force writes only verdict's payload files)\n",
		skill.ReceiptName)
}

func printSkillWrote(r *cli.Runner, plan *skill.Plan) {
	if plan.Written > 0 {
		fmt.Fprintf(r.Human, "install: %d file(s) written (receipt v%s)\n", plan.Written, plan.ReceiptVersion)
	}
}

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

func skillRuntime(r *cli.Runner, env map[string]string) *core.Runtime {
	return &core.Runtime{Env: env, Now: r.Now, Progress: cli.Progress{R: r}}
}

func dedupeStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func skillInventoryData(inv *skill.Inventory) *jq.Obj {
	return jq.NewObj(
		"name", inv.Name,
		"version", inv.Version,
		keyFiles, core.StringValues(inv.Files),
	)
}

func skillFileData(path string, body []byte) *jq.Obj {
	return jq.NewObj("path", path, "content", string(body))
}

func skillInstallData(plan *skill.Plan, dry, force bool) *jq.Obj {
	data := jq.NewObj(
		"name", plan.Name,
		"version", plan.Version,
		"target", plan.Target,
		"status", plan.Status,
		"dry_run", dry,
		"force", force,
		"written", plan.Written,
		keyFiles, core.StringValues(plan.Files),
		"changed", core.StringValues(plan.Changed),
	)
	if plan.ReceiptVersion != "" {
		data.Set("receipt_version", plan.ReceiptVersion)
	}
	return data
}

func skillSetData(det *skill.Detection, dry, force bool) *jq.Obj {
	targets := make([]any, 0, len(det.Plans))
	for i := range det.Plans {
		targets = append(targets, skillTargetData(&det.Plans[i]))
	}
	return jq.NewObj(
		"name", det.Name,
		"version", det.Version,
		"dry_run", dry,
		"force", force,
		"agents", core.StringValues(det.Agents),
		"targets", targets,
	)
}

func skillTargetData(plan *skill.Plan) *jq.Obj {
	data := jq.NewObj(
		"agent", plan.Agent,
		"target", plan.Target,
		"status", plan.Status,
		"written", plan.Written,
		keyFiles, core.StringValues(plan.Files),
		"changed", core.StringValues(plan.Changed),
	)
	if plan.ReceiptVersion != "" {
		data.Set("receipt_version", plan.ReceiptVersion)
	}
	return data
}

func writeSkillEnvelope(r *cli.Runner, data *jq.Obj) {
	env := core.RenderEnvelope("skill", "", "", cli.NowUTC(r.Now()), "",
		r.Warnings, data, nil)
	fmt.Fprintln(r.Out, env.MarshalPretty())
}

func skillUsageText() string {
	return "usage: verdict skill [show [<path>]|files|status|install|uninstall] [opts]\n" +
		"  skill [show]              print SKILL.md (default --format md)\n" +
		"  skill show <path>         print one embedded payload file byte-identical\n" +
		"                            (paths come from `skill files`)\n" +
		"  skill --format json       one envelope: {name, version, files}\n" +
		"  skill files               list every embedded payload path\n" +
		"  skill status [--dir <skills-dir>|--agent <name>...|--detect] [--diff] [--json]\n" +
		"      read-only drift report per target: create/update/keep/blocked, the\n" +
		"      receipt versus embedded version, and the changed files. Exit 0 when\n" +
		"      every target is in sync, 1 when drifted. --diff adds one capped\n" +
		"      unified diff per changed file (a truncated diff says so).\n" +
		"  skill install [--dir <skills-dir>|--agent <name>...|--detect] [--force] [--dry-run] [--json]\n" +
		"      atomically write SKILL.md, prompts/, schemas/, and scripts/verdict plus\n" +
		"      a .verdict-managed receipt. Previews create/update/keep/blocked;\n" +
		"      --agent repeats and installs each agent in argv order (deduped);\n" +
		"      --detect targets every agent below whose skills dir already exists and\n" +
		"      is non-empty, and lists each target's result (nothing detected refuses\n" +
		"      typed: name one with --agent). --detect cannot be combined with\n" +
		"      --dir or --agent.\n" +
		"      only --json is guaranteed non-interactive (this command never prompts).\n" +
		"      A target without a verdict receipt refuses typed unless --force;\n" +
		"      foreign files are never touched.\n" +
		"  skill uninstall [--dir <skills-dir>|--agent <name>...] [--force] [--dry-run] [--json]\n" +
		"      remove only the payload paths the .verdict-managed receipt owns, then\n" +
		"      the receipt; foreign files always survive. A target without a receipt\n" +
		"      refuses unless --force, and even then only inventory paths go;\n" +
		"      --dry-run lists the plan and deletes nothing.\n" +
		"  agents: agents, claude, codex, cursor, opencode, copilot, gemini, goose, qwen, windsurf\n" +
		"  The skill is embedded in this binary, so the served copy can never skew.\n" +
		"  In a skill copy, scripts/verdict resolves VERDICT_BIN, then <skill>/bin/verdict,\n" +
		"  then verdict on PATH; otherwise run <skill>/scripts/verdict bootstrap once\n" +
		"  (verified; never a review-time fetch).\n"
}
