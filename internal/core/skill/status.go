package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	skillfs "github.com/stainless-code/verdict"
	"github.com/stainless-code/verdict/internal/core"
)

// TargetStatus is one target's read-only drift report. InSync means the
// installed bytes and the receipt version both match the embedded payload.
type TargetStatus struct {
	Diffs  []Diff
	Plan   Plan
	InSync bool
}

// StatusSet is the drift report for several targets.
type StatusSet struct {
	Name    string
	Version string
	Agents  []string
	Targets []TargetStatus
	InSync  bool
}

// Status plans one target and reports its drift without writing anything.
func Status(ctx context.Context, req *Request, includeDiff bool) (TargetStatus, *core.RefusalError) {
	if err := ctx.Err(); err != nil {
		return TargetStatus{}, refuse(codeCanceled, "canceled before skill status completed")
	}
	plan, ref := planInstall(ctx, req)
	if ref != nil {
		return TargetStatus{}, ref
	}
	return buildStatus(&plan, includeDiff), nil
}

// StatusAgents reports one target per named agent, in argv order and deduped,
// without writing anything.
func StatusAgents(ctx context.Context, req *Request, agents []string, includeDiff bool) (StatusSet, *core.RefusalError) {
	if err := ctx.Err(); err != nil {
		return StatusSet{}, refuse(codeCanceled, "canceled before skill status completed")
	}
	files, ref := List(ctx, req)
	if ref != nil {
		return StatusSet{}, ref
	}
	plans, agents, ref := planAgents(req, files, agents)
	if ref != nil {
		return StatusSet{}, ref
	}
	return buildStatusSet(files, agents, plans, includeDiff), nil
}

// StatusDetected reports every agent whose skills dir already holds a setup.
func StatusDetected(ctx context.Context, req *Request, includeDiff bool) (StatusSet, *core.RefusalError) {
	if err := ctx.Err(); err != nil {
		return StatusSet{}, refuse(codeCanceled, "canceled before skill status completed")
	}
	det, ref := planDetected(ctx, req)
	if ref != nil {
		return StatusSet{}, ref
	}
	if len(det.Plans) == 0 {
		return StatusSet{}, refuse(codeNoDetected, "no agent with an existing skills dir was detected; "+
			"run --agent <name> to pick one ("+strings.Join(agentNames, ", ")+")")
	}
	set := buildStatusSet(Inventory{Name: det.Name, Version: det.Version}, det.Agents, det.Plans, includeDiff)
	return set, nil
}

func buildStatus(plan *Plan, includeDiff bool) TargetStatus {
	st := TargetStatus{Plan: *plan, InSync: inSync(plan)}
	if includeDiff {
		st.Diffs = diffPlan(plan)
	}
	return st
}

func buildStatusSet(files Inventory, agents []string, plans []Plan, includeDiff bool) StatusSet {
	set := StatusSet{Name: files.Name, Version: files.Version, Agents: agents, InSync: true}
	set.Targets = make([]TargetStatus, 0, len(plans))
	for i := range plans {
		st := buildStatus(&plans[i], includeDiff)
		set.Targets = append(set.Targets, st)
		if !st.InSync {
			set.InSync = false
		}
	}
	return set
}

func inSync(plan *Plan) bool {
	return plan.Status == StatusKeep && plan.ReceiptVersion == plan.Version
}

func diffPlan(plan *Plan) []Diff {
	budget := DiffCapBytes
	diffs := make([]Diff, 0, len(plan.Changed))
	for _, rel := range plan.Changed {
		want, err := skillfs.Read(rel)
		if err != nil {
			continue
		}
		got, err := os.ReadFile(filepath.Join(plan.Target, filepath.FromSlash(rel)))
		if err != nil {
			got = nil
		}
		body, truncated := unifiedDiff(rel, got, want, budget)
		budget -= len(body)
		if budget < 0 {
			budget = 0
		}
		diffs = append(diffs, Diff{Path: rel, Body: body, Truncated: truncated})
	}
	return diffs
}
