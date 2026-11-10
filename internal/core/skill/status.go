package skill

import (
	"context"
	"fmt"
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
	return buildStatus(&plan, includeDiff)
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
	return buildStatusSet(files, agents, plans, includeDiff)
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
	set, ref := buildStatusSet(Inventory{Name: det.Name, Version: det.Version}, det.Agents, det.Plans, includeDiff)
	if ref != nil {
		return StatusSet{}, ref
	}
	return set, nil
}

func buildStatus(plan *Plan, includeDiff bool) (TargetStatus, *core.RefusalError) {
	st := TargetStatus{Plan: *plan, InSync: inSync(plan)}
	if includeDiff {
		diffs, ref := diffPlan(plan)
		if ref != nil {
			return TargetStatus{}, ref
		}
		st.Diffs = diffs
	}
	return st, nil
}

func buildStatusSet(files Inventory, agents []string, plans []Plan, includeDiff bool) (StatusSet, *core.RefusalError) {
	set := StatusSet{Name: files.Name, Version: files.Version, Agents: agents, InSync: true}
	set.Targets = make([]TargetStatus, 0, len(plans))
	for i := range plans {
		st, ref := buildStatus(&plans[i], includeDiff)
		if ref != nil {
			return StatusSet{}, ref
		}
		set.Targets = append(set.Targets, st)
		if !st.InSync {
			set.InSync = false
		}
	}
	return set, nil
}

func inSync(plan *Plan) bool {
	return plan.Status == StatusKeep && plan.ReceiptVersion == plan.Version
}

func diffPlan(plan *Plan) ([]Diff, *core.RefusalError) {
	root, err := os.OpenRoot(plan.Target)
	if err == nil {
		defer func() { _ = root.Close() }()
		return diffChanged(root, plan), nil
	}
	if plan.Status != StatusCreate {
		return nil, refuse(codeRead, fmt.Sprintf("cannot read %s: %s", plan.Target, err))
	}
	return diffChanged(nil, plan), nil
}

// diffChanged renders one unified diff per changed file. A nil root is a
// target that does not exist yet, so every file diffs against empty.
func diffChanged(root *os.Root, plan *Plan) []Diff {
	budget := DiffCapBytes
	diffs := make([]Diff, 0, len(plan.Changed))
	for _, rel := range plan.Changed {
		want, err := skillfs.Read(rel)
		if err != nil {
			continue
		}
		body, truncated := unifiedDiff(rel, diffBytes(root, rel), want, budget)
		budget -= len(body)
		if budget < 0 {
			budget = 0
		}
		diffs = append(diffs, Diff{Path: rel, Body: body, Truncated: truncated})
	}
	return diffs
}

// diffBytes is the installed copy of rel; a nil root or an unreadable file
// diffs as empty.
func diffBytes(root *os.Root, rel string) []byte {
	if root == nil {
		return nil
	}
	got, err := root.ReadFile(filepath.FromSlash(rel))
	if err != nil {
		return nil
	}
	return got
}
