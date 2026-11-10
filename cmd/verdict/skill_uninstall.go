package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/skill"
	"github.com/stainless-code/verdict/internal/jq"
)

type skillUninstallFlags struct {
	dir    string
	agents []string
	force  bool
	dry    bool
}

//
//nolint:revive // the uninstall route nests the single and argv arms
func runSkillUninstall(ctx context.Context, r *cli.Runner, args []string, env map[string]string, root string) int {
	flags, code, done := parseSkillUninstallFlags(r, args)
	if done {
		return code
	}
	if code, bad := skillScope(r, flags.dir, flags.agents, false); bad {
		return code
	}
	r.Rentry = "verdict skill uninstall" + skillUninstallSuffix(r, flags)
	agents := dedupeStrings(flags.agents)
	req := &skill.Request{
		Runtime: skillRuntime(r, env),
		Version: skillVersion(root),
		Dir:     flags.dir,
		Force:   flags.force,
		DryRun:  flags.dry,
	}
	if len(agents) > 1 {
		set, ref := skill.UninstallAgents(ctx, req, agents)
		if ref != nil {
			return r.Refuse(ref)
		}
		printSkillRemovalSet(r, &set)
		if r.JSONMode {
			writeSkillEnvelope(r, skillRemovalSetData(&set))
		}
		return cli.ExitOK
	}
	if len(agents) == 1 {
		req.Agent = agents[0]
	}
	rm, ref := skill.Uninstall(ctx, req)
	if ref != nil {
		return r.Refuse(ref)
	}
	printSkillRemoval(r, &rm)
	if r.JSONMode {
		writeSkillEnvelope(r, skillRemovalData(&rm))
	}
	return cli.ExitOK
}

//
//nolint:cyclop,revive // the flag switch is the parser; splitting it hides the grammar
func parseSkillUninstallFlags(r *cli.Runner, args []string) (skillUninstallFlags, int, bool) {
	var f skillUninstallFlags
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
		case arg == flagDryRun:
			f.dry = true
		case arg == flagShortHelp || arg == flagHelp:
			fmt.Fprint(r.Out, skillUsageText())
			return f, cli.ExitOK, true
		default:
			return f, r.ReportUsage(&cli.UsageError{
				Message: fmt.Sprintf("unknown flag '%s' for skill uninstall (want --dir, --agent, --force, --dry-run, --json)", arg),
			}), true
		}
	}
	return f, 0, false
}

func skillUninstallSuffix(r *cli.Runner, flags skillUninstallFlags) string {
	suffix := ""
	if flags.dry {
		suffix += entryDryRun
	}
	if r.JSONMode {
		suffix += entryJSON
	}
	return suffix
}

func printSkillRemoval(r *cli.Runner, rm *skill.Removal) {
	fmt.Fprintf(r.Human, "skill uninstall: %s v%s -> %s\n", rm.Name, rm.Version, rm.Target)
	printSkillRemovalOutcome(r, rm)
}

func printSkillRemovalSet(r *cli.Runner, set *skill.RemovalSet) {
	fmt.Fprintf(r.Human, "skill uninstall: %s v%s -> %d agent(s): %s\n",
		set.Name, set.Version, len(set.Targets), strings.Join(set.Agents, ", "))
	for i := range set.Targets {
		fmt.Fprintf(r.Human, "--- %s: %s (%s) ---\n",
			set.Targets[i].Agent, set.Targets[i].Target, set.Targets[i].Status)
		printSkillRemovalOutcome(r, &set.Targets[i])
	}
}

func printSkillRemovalOutcome(r *cli.Runner, rm *skill.Removal) {
	if rm.Status == skill.StatusBlocked {
		fmt.Fprintf(r.Human, "uninstall: blocked (no %s receipt; --force removes only verdict's payload files)\n", skill.ReceiptName)
	}
	if rm.DryRun {
		fmt.Fprintf(r.Human, "dry-run: would remove %d file(s); nothing changed\n", len(rm.Would))
	} else {
		fmt.Fprintf(r.Human, "uninstall: removed %d payload file(s)\n", len(rm.Removed))
	}
	if rm.ReceiptRemoved {
		fmt.Fprintln(r.Human, "uninstall: receipt removed")
	}
	if len(rm.Kept) > 0 {
		fmt.Fprintf(r.Human, "uninstall: kept %d foreign file(s)\n", len(rm.Kept))
	}
}

func skillRemovalData(rm *skill.Removal) *jq.Obj {
	return jq.NewObj(
		"name", rm.Name,
		"version", rm.Version,
		"target", rm.Target,
		"status", rm.Status,
		"dry_run", rm.DryRun,
		"removed", core.StringValues(rm.Removed),
		"would_remove", core.StringValues(rm.Would),
		"kept", core.StringValues(rm.Kept),
		"receipt_removed", rm.ReceiptRemoved,
	)
}

func skillRemovalSetData(set *skill.RemovalSet) *jq.Obj {
	targets := make([]any, 0, len(set.Targets))
	for i := range set.Targets {
		targets = append(targets, skillRemovalTargetData(&set.Targets[i]))
	}
	return jq.NewObj(
		"name", set.Name,
		"version", set.Version,
		"agents", core.StringValues(set.Agents),
		"targets", targets,
		"dry_run", len(set.Targets) > 0 && set.Targets[0].DryRun,
	)
}

func skillRemovalTargetData(rm *skill.Removal) *jq.Obj {
	return jq.NewObj(
		"agent", rm.Agent,
		"target", rm.Target,
		"status", rm.Status,
		"removed", core.StringValues(rm.Removed),
		"would_remove", core.StringValues(rm.Would),
		"kept", core.StringValues(rm.Kept),
		"receipt_removed", rm.ReceiptRemoved,
	)
}
