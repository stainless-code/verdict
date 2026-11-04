package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

const codeNoDetected = "skill-no-detected-agent"

// Detection is one --detect install: the agents whose skills dir already
// holds a setup, in agent-table order, and the plan for each.
type Detection struct {
	Name    string
	Version string
	Agents  []string
	Plans   []Plan
}

// InstallDetected plans and applies an install for every agent whose skills
// dir already holds a setup, using install-skill.sh's --detect rule: the dir
// exists and is not empty. The agent table is the same one --agent resolves.
//
// Every target keeps the single-target safety rules: the receipt check, a
// typed refusal without --force, and foreign files left untouched. All targets
// are classified before the first byte is written, so a refused target leaves
// no half-installed set behind.
func InstallDetected(ctx context.Context, req *Request) (Detection, *core.RefusalError) {
	if err := ctx.Err(); err != nil {
		return Detection{}, refuse(codeCanceled, "canceled before skill install completed")
	}
	det, ref := planDetected(ctx, req)
	if ref != nil {
		return Detection{}, ref
	}
	if len(det.Plans) == 0 {
		return Detection{}, refuse(codeNoDetected, "no agent with an existing skills dir was detected; "+
			"run --agent <name> to pick one ("+strings.Join(agentNames, ", ")+")")
	}
	if req.DryRun {
		return det, nil
	}
	if ref := applyDetected(req, det.Plans); ref != nil {
		return Detection{}, ref
	}
	return det, nil
}

func applyDetected(req *Request, plans []Plan) *core.RefusalError {
	for i := range plans {
		if ref := checkDetected(req, &plans[i]); ref != nil {
			return ref
		}
	}
	for i := range plans {
		if ref := apply(req, &plans[i]); ref != nil {
			return ref
		}
	}
	return nil
}

func checkDetected(req *Request, plan *Plan) *core.RefusalError {
	if ref := checkApply(req, plan); ref != nil {
		return ref
	}
	if plan.Status == StatusKeep {
		return nil
	}
	return preflight(plan.Target, plan.Files)
}

func planDetected(ctx context.Context, req *Request) (Detection, *core.RefusalError) {
	files, ref := List(ctx, req)
	if ref != nil {
		return Detection{}, ref
	}
	agents, plans, resolvable, ref := detectAll(files, req)
	if ref != nil {
		return Detection{}, ref
	}
	if !resolvable {
		return Detection{}, refuse(codeNoHome,
			"cannot resolve any agent skills dir: HOME (or CLAUDE_CONFIG_DIR/CODEX_HOME/XDG_CONFIG_HOME) is unset; pass --dir or --agent")
	}
	return Detection{Name: files.Name, Version: files.Version, Agents: agents, Plans: plans}, nil
}

func detectAll(files Inventory, req *Request) (agents []string, plans []Plan, resolvable bool, ref *core.RefusalError) {
	for _, agent := range agentNames {
		dir, _ := req.agentDir(agent)
		if dir == "" {
			continue
		}
		resolvable = true
		plan, ok, refusal := detectTarget(files, agent, dir)
		if refusal != nil {
			return nil, nil, false, refusal
		}
		if ok {
			agents = append(agents, agent)
			plans = append(plans, plan)
		}
	}
	return agents, plans, resolvable, nil
}

func detectTarget(files Inventory, agent, dir string) (Plan, bool, *core.RefusalError) {
	if !isLive(dir) {
		return Plan{}, false, nil
	}
	plan, ref := planAt(files, filepath.Join(dir, Name))
	if ref != nil {
		return Plan{}, false, ref
	}
	plan.Agent = agent
	return plan, true, nil
}

// isLive reports whether a skills dir already holds a setup: it exists as a
// directory and is not empty. That is the same test install-skill.sh's
// --detect applies, so both routes agree on which agents are present.
func isLive(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}
