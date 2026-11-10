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

type skillStatusFlags struct {
	dir    string
	agents []string
	detect bool
	diff   bool
}

//
//nolint:revive // the status route nests the single, argv, and detect arms
func runSkillStatus(ctx context.Context, r *cli.Runner, args []string, env map[string]string, root string) int {
	flags, code, done := parseSkillStatusFlags(r, args)
	if done {
		return code
	}
	if code, bad := skillScope(r, flags.dir, flags.agents, flags.detect); bad {
		return code
	}
	r.Rentry = "verdict skill status" + skillStatusSuffix(r, flags)
	agents := dedupeStrings(flags.agents)
	req := &skill.Request{Runtime: skillRuntime(r, env), Version: skillVersion(root), Dir: flags.dir}
	switch {
	case flags.detect:
		set, ref := skill.StatusDetected(ctx, req, flags.diff)
		if ref != nil {
			return r.Refuse(ref)
		}
		return finishSkillStatusSet(r, &set, flags.diff)
	case len(agents) > 1:
		set, ref := skill.StatusAgents(ctx, req, agents, flags.diff)
		if ref != nil {
			return r.Refuse(ref)
		}
		return finishSkillStatusSet(r, &set, flags.diff)
	default:
		if len(agents) == 1 {
			req.Agent = agents[0]
		}
		st, ref := skill.Status(ctx, req, flags.diff)
		if ref != nil {
			return r.Refuse(ref)
		}
		return finishSkillStatus(r, &st, flags.diff)
	}
}

//
//nolint:cyclop,revive // the flag switch is the parser; splitting it hides the grammar
func parseSkillStatusFlags(r *cli.Runner, args []string) (skillStatusFlags, int, bool) {
	var f skillStatusFlags
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
		case arg == "--detect":
			f.detect = true
		case arg == "--diff":
			f.diff = true
		case arg == flagShortHelp || arg == flagHelp:
			fmt.Fprint(r.Out, skillUsageText())
			return f, cli.ExitOK, true
		default:
			return f, r.ReportUsage(&cli.UsageError{
				Message: fmt.Sprintf("unknown flag '%s' for skill status (want --dir, --agent, --detect, --diff, --json)", arg),
			}), true
		}
	}
	return f, 0, false
}

func skillStatusSuffix(r *cli.Runner, flags skillStatusFlags) string {
	suffix := ""
	if flags.diff {
		suffix += " --diff"
	}
	if r.JSONMode {
		suffix += entryJSON
	}
	return suffix
}

func finishSkillStatus(r *cli.Runner, st *skill.TargetStatus, includeDiff bool) int {
	printSkillStatus(r, st, includeDiff)
	if r.JSONMode {
		writeSkillEnvelope(r, skillStatusData(st, includeDiff))
	}
	if st.InSync {
		return cli.ExitOK
	}
	return cli.ExitRefused
}

func finishSkillStatusSet(r *cli.Runner, set *skill.StatusSet, includeDiff bool) int {
	printSkillStatusSet(r, set, includeDiff)
	if r.JSONMode {
		writeSkillEnvelope(r, skillStatusSetData(set, includeDiff))
	}
	if set.InSync {
		return cli.ExitOK
	}
	return cli.ExitRefused
}

func printSkillStatus(r *cli.Runner, st *skill.TargetStatus, includeDiff bool) {
	fmt.Fprintf(r.Human, "skill status: %s v%s -> %s\n", st.Plan.Name, st.Plan.Version, st.Plan.Target)
	printSkillStatusTarget(r, st, includeDiff)
	if !st.InSync {
		fmt.Fprintln(r.Human, "status: drift (run `verdict skill install` to sync)")
	}
}

func printSkillStatusSet(r *cli.Runner, set *skill.StatusSet, includeDiff bool) {
	fmt.Fprintf(r.Human, "skill status: %s v%s -> %d agent(s): %s\n",
		set.Name, set.Version, len(set.Targets), strings.Join(set.Agents, ", "))
	for i := range set.Targets {
		fmt.Fprintf(r.Human, "--- %s: %s (%s) ---\n",
			set.Targets[i].Plan.Agent, set.Targets[i].Plan.Target, set.Targets[i].Plan.Status)
		printSkillStatusChanged(r, &set.Targets[i], includeDiff)
	}
	if !set.InSync {
		fmt.Fprintln(r.Human, "status: drift (run `verdict skill install` to sync)")
	}
}

func printSkillStatusTarget(r *cli.Runner, st *skill.TargetStatus, includeDiff bool) {
	fmt.Fprintf(r.Human, "--- %s (%s) ---\n", st.Plan.Target, st.Plan.Status)
	printSkillStatusChanged(r, st, includeDiff)
}

func printSkillStatusChanged(r *cli.Runner, st *skill.TargetStatus, includeDiff bool) {
	for _, rel := range st.Plan.Changed {
		fmt.Fprintf(r.Human, "  ~ %s\n", rel)
	}
	if st.Plan.ReceiptVersion != "" && st.Plan.ReceiptVersion != st.Plan.Version {
		fmt.Fprintf(r.Human, "receipt v%s -> embedded v%s\n", st.Plan.ReceiptVersion, st.Plan.Version)
	}
	if !includeDiff {
		return
	}
	for _, diff := range st.Diffs {
		fmt.Fprint(r.Human, diff.Body)
	}
}

func skillStatusData(st *skill.TargetStatus, includeDiff bool) *jq.Obj {
	data := jq.NewObj(
		"name", st.Plan.Name,
		"version", st.Plan.Version,
		"target", st.Plan.Target,
		"status", st.Plan.Status,
		"in_sync", st.InSync,
		keyFiles, core.StringValues(st.Plan.Files),
		"changed", core.StringValues(st.Plan.Changed),
		"receipt_version", st.Plan.ReceiptVersion,
	)
	if includeDiff {
		data.Set("diff", skillDiffsData(st.Diffs))
	}
	return data
}

func skillStatusSetData(set *skill.StatusSet, includeDiff bool) *jq.Obj {
	targets := make([]any, 0, len(set.Targets))
	for i := range set.Targets {
		targets = append(targets, skillStatusTargetData(&set.Targets[i], includeDiff))
	}
	return jq.NewObj(
		"name", set.Name,
		"version", set.Version,
		"agents", core.StringValues(set.Agents),
		"targets", targets,
		"in_sync", set.InSync,
	)
}

func skillStatusTargetData(st *skill.TargetStatus, includeDiff bool) *jq.Obj {
	data := jq.NewObj(
		"agent", st.Plan.Agent,
		"target", st.Plan.Target,
		"status", st.Plan.Status,
		"in_sync", st.InSync,
		keyFiles, core.StringValues(st.Plan.Files),
		"changed", core.StringValues(st.Plan.Changed),
		"receipt_version", st.Plan.ReceiptVersion,
	)
	if includeDiff {
		data.Set("diff", skillDiffsData(st.Diffs))
	}
	return data
}

func skillDiffsData(diffs []skill.Diff) []any {
	out := make([]any, 0, len(diffs))
	for _, diff := range diffs {
		out = append(out, jq.NewObj(
			"path", diff.Path,
			"diff", diff.Body,
			"truncated", diff.Truncated,
		))
	}
	return out
}
