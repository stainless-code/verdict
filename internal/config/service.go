package config

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/stainless-code/verdict/internal/core"
)

// Run executes one config request: the positional resolves to the run
// bundle (the fetch-local local- form when that exists), then explain
// renders the stored bundle or preview resolves the current inputs. No
// live calls, no exec, no writes, and never an approval.
func Run(ctx context.Context, req *core.ConfigRequest) (*core.ConfigResponse, *core.RefusalError) {
	var name string
	var data *core.ConfigData
	var warnings []string
	var ref *core.RefusalError
	if req.Flags.Explain {
		if name, ref = ResolveBundleName("", req.Pr); ref != nil {
			return nil, ref
		}
		data, warnings, ref = Explain("", name)
	} else {
		name, _ = lookupBundleName("", req.Pr)
		data, warnings, ref = Preview("", name, req.Flags.File, req.SettingsPath, reqEnvMap(req))
	}
	if ref != nil {
		return nil, ref
	}
	return &core.ConfigResponse{
		Repo: req.Repo, Pr: name, At: core.NowUTC(clockNow(req)),
		Data: data, Warnings: append(append([]string{}, req.Warnings...), warnings...),
		Next: nextStep(req.Flags, name),
	}, nil
}

// reqEnvMap returns the request environment for the preview layers.
func reqEnvMap(req *core.ConfigRequest) map[string]string {
	if req.Runtime == nil || req.Runtime.Env == nil {
		return map[string]string{}
	}
	return req.Runtime.Env
}

// nextStep names the envelope follow-up: preview points at explain once
// a bundle exists, and explain points at the verbs that consume it.
func nextStep(flags core.ConfigFlags, name string) string {
	if flags.Explain {
		return "post, report, and verify enforce this policy; re-fetch to move it"
	}
	return "fetch the run to explain the stored policy"
}

// ResolveBundleName maps the positional to the persisted run bundle: the
// exact name wins, else the fetch-local local- form. Neither existing
// refuses with the typed no-bundle code naming both paths.
func ResolveBundleName(root, pr string) (string, *core.RefusalError) {
	if name, found := lookupBundleName(root, pr); found {
		return name, nil
	}
	return "", refuse(CodeNoBundle, "no run bundle %s (fetch the run first, then explain it)", RunBundlePath(root, pr))
}

// lookupBundleName resolves the bundle without refusing: the exact name
// wins, else the fetch-local local- form, else the raw positional (the
// preview paths read inventory beside it when one exists).
func lookupBundleName(root, pr string) (string, bool) {
	if _, err := os.Stat(filepath.Join(ifEmpty(root), VerdictDir, StateDirName, pr)); err == nil {
		return pr, true
	}
	if _, err := os.Stat(filepath.Join(ifEmpty(root), VerdictDir, StateDirName, "local-"+pr)); err == nil {
		return "local-" + pr, true
	}
	return pr, false
}

// clockNow reads the request clock, defaulting to the wall clock.
func clockNow(req *core.ConfigRequest) (t time.Time) {
	if req.Runtime != nil && req.Now != nil {
		return req.Now()
	}
	return time.Now()
}

// Explain renders the stored effective policy for one run bundle: the
// fetch-time evidence with origins, the pending plan, the spend and
// privacy views, and the appended check outcomes. Explain never
// re-resolves: later environment or settings changes cannot change the
// explanation of an existing review. The bundle is required; absent
// reads config-no-bundle naming the expected path.
func Explain(root, name string) (*core.ConfigData, []string, *core.RefusalError) {
	rs, found, serr := ReadRunState(root, name)
	if serr != nil {
		if ref := serr.Refusal(); ref != nil {
			return nil, nil, ref
		}
		return nil, nil, refuse(CodeBadConfig, "could not read run bundle %s: %v", RunBundlePath(root, name), serr)
	}
	if !found {
		return nil, nil, refuse(CodeNoBundle, "no run bundle %s (fetch the run first, then explain it)", RunBundlePath(root, name))
	}
	data := dataFromEvidence(rs)
	data.Mode = "explain"
	return data, nil, nil
}

// Preview resolves what would be effective without any live call, exec,
// or write: a proposed file when given (trusted false), else the
// trusted-base inventory beside the run bundle when one exists, else
// the no-policy layers. Per-file plan groups resolve when the legacy
// bundle's file list survives beside the state.
func Preview(root, name, proposedPath, settingsPath string, env map[string]string) (*core.ConfigData, []string, *core.RefusalError) {
	personal, ref := LoadPersonal(settingsPath)
	if ref != nil {
		return nil, nil, ref
	}
	partial, warns, ref := ParseEnv(env)
	if ref != nil {
		return nil, nil, ref
	}
	cfg, trusted, source, ref := previewInputs(root, name, proposedPath)
	if ref != nil {
		return nil, nil, ref
	}
	in := &Inputs{
		Repo:         cfg,
		Personal:     personal,
		SettingsPath: settingsPath,
		Files:        previewFiles(root, name),
		Env:          partial,
	}
	resolved, ref := Resolve(in)
	if ref != nil {
		return nil, nil, ref
	}
	data := dataFromResolved(resolved, nil)
	data.Mode = "preview"
	data.Trusted = trusted
	data.Source = source
	return data, warns, nil
}

// previewInputs reads the proposed file or the trusted-base inventory:
// the file wins outright (proposed, never trusted), else the inventory
// beside the run bundle, else no repository policy.
func previewInputs(root, name, proposedPath string) (*core.Config, bool, string, *core.RefusalError) {
	if proposedPath != "" {
		raw, err := os.ReadFile(proposedPath)
		if err != nil {
			return nil, false, "", refuse(CodeBadConfig, "could not read proposed config %s: %v", proposedPath, err)
		}
		cfg, ref := Parse(raw)
		if ref != nil {
			return nil, false, "", ref
		}
		return cfg, false, "proposed file " + proposedPath, nil
	}
	raw, ok := previewInventory(root, name)
	if !ok {
		return nil, false, "no repository policy", nil
	}
	cfg, ref := Parse(raw)
	if ref != nil {
		return nil, false, "", ref
	}
	return cfg, true, "trusted base inventory", nil
}

// previewInventory reads the trusted-base configuration snapshot the
// fetch persisted beside the run bundle. Absent reads absent.
func previewInventory(root, name string) ([]byte, bool) {
	raw, err := os.ReadFile(filepath.Join(ifEmpty(root), VerdictDir, StateDirName, name, inventoryDirName, "config.json"))
	if err != nil {
		return nil, false
	}
	return raw, true
}

// previewFiles reads the legacy bundle's file list for per-file plan
// groups when it survives: absent reads global-only resolution.
func previewFiles(root, name string) []FileInput {
	raw, err := os.ReadFile(filepath.Join(ifEmpty(root), ".review", name, ".difffiles"))
	if err != nil {
		return nil
	}
	var out []FileInput
	for _, line := range splitIgnoreLines(string(raw)) {
		if line != "" {
			out = append(out, FileInput{Path: line})
		}
	}
	return out
}

// RunBundlePath names the persisted run bundle for diagnostics.
func RunBundlePath(root, name string) string {
	return filepath.Join(ifEmpty(root), VerdictDir, StateDirName, name, runStateFileName)
}

// dataFromEvidence replays the stored capture: later verbs observe
// this policy, never re-resolve it.
func dataFromEvidence(rs *RunState) *core.ConfigData {
	data := &core.ConfigData{
		Trusted:    rs.Trusted,
		Source:     evidenceSource(rs),
		PlanStatus: rs.Evidence.Plan.Status,
	}
	for _, value := range rs.Evidence.Values {
		data.Values = append(data.Values, core.ConfigValue{
			Key: value.Key, Value: value.Value,
			Layer: value.Origin.Layer, Source: value.Origin.Source,
		})
	}
	for _, req := range rs.Evidence.Requirements {
		data.Requirements = append(data.Requirements, core.ConfigRequirement{
			Key: req.Key, Threshold: req.Threshold,
			Status: req.Status, Sources: req.Sources,
		})
	}
	data.Plan = planGroups(rs.Evidence.Plan)
	data.Spend = spendView(rs.Resolved)
	data.Privacy = privacyView(rs.Resolved)
	for _, obs := range rs.Outcomes {
		data.Checks = append(data.Checks, core.ConfigCheck{
			Check: obs.Check, Group: obs.Group,
			Passed: obs.Passed, At: obs.At, Detail: obs.Detail,
		})
	}
	return data
}

func evidenceSource(rs *RunState) string {
	if rs.BaseSHA != "" {
		return "run bundle " + rs.Name + " (base " + rs.BaseSHA + ")"
	}
	return "run bundle " + rs.Name
}

// dataFromResolved resolves the current inputs with no outcomes yet:
// every check reads pending.
func dataFromResolved(resolved *Resolved, outcomes []CheckObservation) *core.ConfigData {
	data := &core.ConfigData{
		Source:     "preview",
		PlanStatus: resolved.Plan.Status,
	}
	data.Values = resolvedValues(resolved)
	data.Plan = planGroups(resolved.Plan)
	data.Spend = spendView(resolved)
	data.Privacy = privacyView(resolved)
	return data
}

// resolvedValues flattens the global effective defaults with origins in
// the evidence key order: a fixed slice, so the wire order is stable.
func resolvedValues(resolved *Resolved) []core.ConfigValue {
	values := resolved.Defaults.Values
	origins := resolved.Defaults.Origins
	get := func(key string) FieldOrigin { return origins[key] }
	return []core.ConfigValue{
		{Key: keyPanel, Value: values.Panel, Layer: get(keyPanel).Layer, Source: get(keyPanel).Source},
		{Key: keyBudgetUSD, Value: values.BudgetUSD, Layer: get(keyBudgetUSD).Layer, Source: get(keyBudgetUSD).Source},
		{Key: keyMaxBytes, Value: values.MaxBytes, Layer: get(keyMaxBytes).Layer, Source: get(keyMaxBytes).Source},
		{Key: keyChunk, Value: values.Chunk, Layer: get(keyChunk).Layer, Source: get(keyChunk).Source},
		{Key: keyKBMinJudged, Value: values.KBMinJudged, Layer: get(keyKBMinJudged).Layer, Source: get(keyKBMinJudged).Source},
		{Key: keyKBMaxFPRate, Value: values.KBMaxFPRate, Layer: get(keyKBMaxFPRate).Layer, Source: get(keyKBMaxFPRate).Source},
		{Key: keyEffort, Value: values.Effort, Layer: get(keyEffort).Layer, Source: get(keyEffort).Source},
		{Key: keyDisplayFloor, Value: values.DisplayMinSeverity, Layer: get(keyDisplayFloor).Layer, Source: get(keyDisplayFloor).Source},
		{Key: keyPostFloor, Value: values.PostMinSeverity, Layer: get(keyPostFloor).Layer, Source: get(keyPostFloor).Source},
	}
}

func planGroups(plan Plan) []core.ConfigPlanGroup {
	groups := make([]core.ConfigPlanGroup, 0, len(plan.Groups))
	for _, group := range plan.Groups {
		groups = append(groups, core.ConfigPlanGroup{
			Profile: group.Profile, Depth: group.Depth, Checks: group.Checks,
			MaxLenses: group.MaxLenses, Files: group.Files, Scopes: group.Scopes,
		})
	}
	return groups
}

func spendView(resolved *Resolved) core.ConfigSpend {
	if resolved == nil {
		return core.ConfigSpend{}
	}
	return core.ConfigSpend{
		GuardUSD:             resolved.Spend.GuardUSD,
		PersonalLimit:        resolved.Spend.PersonalLimit,
		RequireKnownEstimate: resolved.Spend.RequireKnownEstimate,
		EnforceCap:           resolved.Spend.EnforceCap,
	}
}

func privacyView(resolved *Resolved) []string {
	if resolved == nil {
		return nil
	}
	return append([]string{}, resolved.Privacy.Exclusions...)
}
