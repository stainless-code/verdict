package readycore

import (
	"fmt"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/trigger"
)

const (
	codeFilteredOut = "filtered-out"
	codeTriggerFail = "trigger-failed"
)

// filterTrigger evaluates the trusted-base trigger before any live host
// read, and records both outcomes in the bundle's run receipt. The policy
// bytes come from the base snapshot the fetch captured, never the worktree.
func (st *state) filterTrigger(dir string) *core.RefusalError {
	predicates, ref := baseTrigger(dir)
	if ref != nil {
		return st.withWarnings(ref)
	}
	facts, err := trigger.ReadFacts(dir)
	if err != nil {
		return st.withWarnings(&core.RefusalError{Code: codeTriggerFail, Message: err.Error()})
	}
	run, reason := trigger.Evaluate(facts, predicates)
	decision := &config.TriggerDecision{
		At:     st.req.TimeUTC(),
		Head:   readcommon.OpenBundle(dir).Head(),
		Run:    run,
		Reason: reason,
	}
	written, err := config.AppendTrigger(dir, decision)
	if err != nil || !written {
		return st.withWarnings(&core.RefusalError{
			Code:    codeTriggerFail,
			Message: "could not record the trigger decision in " + dir + "/run.json (re-fetch the bundle)",
		})
	}
	if !run {
		return st.withWarnings(&core.RefusalError{
			Code:    codeFilteredOut,
			Message: fmt.Sprintf("filtered out by %s (policy: %s at the PR base)", reason, config.ConfigPath),
		})
	}
	return nil
}

func baseTrigger(dir string) (*core.ConfigTrigger, *core.RefusalError) {
	snap, found, ref := controls.GateRecordedPath(dir, config.ConfigPath)
	if ref != nil {
		return nil, ref
	}
	if !found {
		return nil, nil
	}
	raw, ok := snap.ReadFile(config.ConfigPath)
	if !ok {
		return nil, &core.RefusalError{
			Code:    codeTriggerFail,
			Message: "base snapshot has no readable " + config.ConfigPath + " (re-fetch the bundle)",
		}
	}
	cfg, ref := config.Parse(raw)
	if ref != nil {
		return nil, ref
	}
	return cfg.Trigger, nil
}
