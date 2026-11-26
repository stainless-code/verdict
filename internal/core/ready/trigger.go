package readycore

import (
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/trigger"
)

const (
	codeFilteredOut   = "filtered-out"
	codeTriggerFail   = "trigger-failed"
	codeMissingBundle = "missing-bundle"
)

func filterGate(req *Request) *core.RefusalError {
	if !req.Filter {
		return nil
	}
	st := &state{
		req:      req,
		repo:     req.Repo,
		pr:       req.Pr,
		warnings: append([]string{}, req.Warnings...),
	}
	dir, ref := readcommon.ResolveBundleDir(req.RepoRootPath(), req.Pr)
	if ref != nil {
		return st.withWarnings(ref)
	}
	if isLocalDir(dir) {
		return st.withWarnings(&core.RefusalError{
			Code:    codeNoPRTarget,
			Message: "ready needs a PR bundle (fetch owner/repo N first); local bundles have no trigger policy",
		})
	}
	if !isRegular(dir + "/PR.json") {
		return st.withWarnings(&core.RefusalError{
			Code:    codeMissingBundle,
			Message: core.Msgf("missing %s/PR.json (run fetch %s %s first)", dir, st.repo, st.pr),
		})
	}
	if ref := st.filterTrigger(dir); ref != nil {
		return ref
	}
	req.Warnings = st.warnings
	return nil
}

func (st *state) filterTrigger(dir string) *core.RefusalError {
	predicates, ref := baseTrigger(dir)
	if ref != nil {
		return st.withWarnings(ref)
	}
	facts, err := trigger.ReadFacts(dir)
	if err != nil {
		return st.withWarnings(triggerFailure("could not read trigger facts", err))
	}
	if facts.IncompletePaths {
		st.warn("trigger file list is incomplete — path predicates skipped, proceeding with review")
	}
	if _, ref := config.VerifyRunState(dir); ref != nil {
		return st.withWarnings(triggerFailure("could not verify the run receipt", ref))
	}
	run, reason := trigger.Evaluate(&facts, predicates)
	decision := &config.TriggerDecision{
		At:     st.req.TimeUTC(),
		Head:   readcommon.OpenBundle(dir).Head(),
		Run:    run,
		Reason: reason,
	}
	written, err := config.AppendTrigger(dir, decision)
	if err != nil || !written {
		return st.withWarnings(triggerFailure("could not record the trigger decision in "+dir+"/run.json (re-fetch)", err))
	}
	if !run {
		return st.withWarnings(&core.RefusalError{
			Code:    codeFilteredOut,
			Message: core.Msgf("filtered out by %s (policy: %s at the PR base)", reason, config.ConfigPath),
		})
	}
	return nil
}

func triggerFailure(what string, cause error) *core.RefusalError {
	if cause == nil {
		return &core.RefusalError{Code: codeTriggerFail, Message: core.Msgf("%s", what)}
	}
	return &core.RefusalError{Code: codeTriggerFail, Message: core.Msgf("%s: %s", what, cause.Error())}
}

func baseTrigger(dir string) (*core.ConfigTrigger, *core.RefusalError) {
	snap, ref := controls.GateRecorded(dir)
	if ref != nil {
		return nil, triggerFailure("could not verify the recorded base snapshot", ref)
	}
	if ref := checkBasePolicy(snap); ref != nil {
		return nil, ref
	}
	entry := snap.Entry(config.ConfigPath)
	if entry == nil || entry.Status != config.StatusUsed {
		return nil, nil
	}
	raw, ok := snap.ReadFile(config.ConfigPath)
	if !ok {
		return nil, triggerFailure("base snapshot has no readable "+config.ConfigPath+" (re-fetch)", nil)
	}
	return parseBaseTrigger(raw)
}

func checkBasePolicy(snap *controls.Snapshot) *core.RefusalError {
	if snap.File.Policy != controls.PolicyBase || snap.File.SHA == "" ||
		snap.File.SHA != snap.File.BaseSHA {
		return triggerFailure("trigger needs a base-policy snapshot (re-fetch without --allow-head-controls)", nil)
	}
	return nil
}

func parseBaseTrigger(raw []byte) (*core.ConfigTrigger, *core.RefusalError) {
	cfg, ref := config.Parse(raw)
	if ref == nil {
		return cfg.Trigger, nil
	}
	if config.IsUnknownField(ref) {
		return nil, ref
	}
	return nil, triggerFailure("could not parse the base "+config.ConfigPath, ref)
}
