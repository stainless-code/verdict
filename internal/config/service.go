package config

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
)

// Run executes one config request: the positional resolves to the run
// bundle (the fetch-local local- form when that exists), then explain
// renders the stored bundle or preview resolves the current inputs. No live
// calls, no writes, and never an approval. Resolving the root runs one
// `git rev-parse` probe through the environment, not the injected commander.
func Run(ctx context.Context, req *core.ConfigRequest) (*core.ConfigResponse, *core.RefusalError) {
	var name string
	var data *core.ConfigData
	var warnings []string
	var ref *core.RefusalError
	root := req.RepoRootPath()
	if err := ctx.Err(); err != nil {
		return nil, refuse(CodeCanceled, "canceled before config completed")
	}
	if req.Flags.Explain {
		if name, ref = ResolveBundleName(root, req.Pr); ref != nil {
			return nil, ref
		}
		data, warnings, ref = Explain(root, name)
	} else {
		name, _, ref = lookupBundleName(root, req.Pr)
		if ref != nil {
			return nil, ref
		}
		data, warnings, ref = Preview(root, name, req.Flags.File, req.SettingsPath, reqEnvMap(req))
	}
	if ref != nil {
		return nil, ref
	}
	return &core.ConfigResponse{
		Repo: req.Repo, Pr: name, At: core.NowUTC(clockNow(req)),
		Data: data, Warnings: append(append([]string{}, req.Warnings...), warnings...),
		Next: nextStep(req.Flags),
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
func nextStep(flags core.ConfigFlags) string {
	if flags.Explain {
		return "post, report, and verify enforce this policy; re-fetch to move it"
	}
	return "fetch the run to explain the stored policy"
}

// ResolveBundleName maps the positional to the persisted run bundle: the
// exact name wins, else the fetch-local local- form. Neither existing
// refuses with the typed no-bundle code naming both paths.
func ResolveBundleName(root, pr string) (string, *core.RefusalError) {
	name, found, ref := lookupBundleName(root, pr)
	if ref != nil {
		return "", ref
	}
	if found {
		return name, nil
	}
	dir, ref := BundleDir(root, name)
	if ref != nil {
		return "", ref
	}
	return "", refuse(CodeNoBundle, "no run bundle %s (fetch the run first, then explain it)", RunBundlePath(dir))
}

// lookupBundleName resolves the positional without refusing: the exact name
// wins, else the fetch-local local- form, else the raw positional (the
// preview paths read inventory beside it when one exists).
func lookupBundleName(root, pr string) (string, bool, *core.RefusalError) {
	exact, ref := BundleDir(root, pr)
	if ref != nil {
		return "", false, ref
	}
	if _, err := os.Stat(exact); err == nil {
		return pr, true, nil
	}
	local, ref := BundleDir(root, "local-"+pr)
	if ref != nil {
		return "", false, ref
	}
	if _, err := os.Stat(local); err == nil {
		return "local-" + pr, true, nil
	}
	return pr, false, nil
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
// reads config-no-bundle naming the expected path. A deleted or
// tampered bundle refuses controls-stale through the same integrity
// check record and post enforce.
func Explain(root, name string) (*core.ConfigData, []string, *core.RefusalError) {
	dir, ref := BundleDir(root, name)
	if ref != nil {
		return nil, nil, ref
	}
	rs, ref := VerifyRunState(dir)
	if ref != nil {
		return nil, nil, ref
	}
	if rs == nil {
		return nil, nil, refuse(CodeNoBundle, "no run bundle %s (fetch the run first, then explain it)", RunBundlePath(dir))
	}
	data := dataFromEvidence(rs)
	data.Mode = "explain"
	if !rs.Verified() {
		note := "typed evidence provenance unavailable (pre-integrity bundle) — " +
			"policy enforced from stored capture, not verified evidence"
		return data, []string{note}, nil
	}
	return data, nil, nil
}

// Labels and warnings for a preview whose repository layer is not proven:
// without a run bundle nothing read the base configuration, so the preview
// never claims the base holds no policy.
const (
	noBaseLabel   = "trusted base unavailable (no run bundle)"
	noBaseWarning = "trusted base configuration not read: no run bundle for this run, so this preview " +
		"excludes the repository layer — fetch the run, or preview a proposed file, to see repository policy"
)

// Preview resolves what would be effective without any live call, exec,
// or write: a proposed file when given (never trusted, with every value the
// repository layer supplied naming that file), else the recorded trusted-base
// inventory beside the run bundle, else the layers the checkout proves.
// Per-file plan groups resolve when the bundle's file list survives beside the
// state. Requirements and plan checks come from the same resolution the fetch
// capture stores, so a preview and the bundle fetched from it name one policy;
// every prospective requirement stays pending because a preview has no
// evidence.
func Preview(root, name, proposedPath, settingsPath string, env map[string]string) (*core.ConfigData, []string, *core.RefusalError) {
	dir, ref := BundleDir(root, name)
	if ref != nil {
		return nil, nil, ref
	}
	personal, ref := LoadPersonal(settingsPath)
	if ref != nil {
		return nil, nil, ref
	}
	partial, warns, ref := ParseEnv(env)
	if ref != nil {
		return nil, nil, ref
	}
	src, cfg, ref := previewInputs(root, name, proposedPath)
	if ref != nil {
		return nil, nil, ref
	}
	files, ref := previewFiles(dir)
	if ref != nil {
		return nil, nil, ref
	}
	in := &Inputs{
		Repo:         cfg,
		Personal:     personal,
		SettingsPath: settingsPath,
		BaseSHA:      src.baseSHA,
		Files:        files,
		Env:          partial,
	}
	resolved, ref := Resolve(in)
	if ref != nil {
		return nil, nil, ref
	}
	data := dataFromResolved(in, resolved)
	data.Mode = "preview"
	data.Trusted = src.trusted
	data.Source = src.label
	if src.proposed != "" {
		labelProposed(data, src.proposed)
	}
	if src.warning != "" {
		warns = append(warns, src.warning)
	}
	return data, warns, nil
}

// previewProvenance is where a preview's repository layer came from and how
// much of it is proven: the trusted-base inventory recorded beside a run
// bundle, the operator's proposed file, or nothing read at all. Only a
// verified snapshot sets trusted; a proposed file never does, and an absent
// bundle proves nothing about the base.
type previewProvenance struct {
	label    string
	proposed string
	warning  string
	baseSHA  string
	trusted  bool
}

// previewBaseState is what the recorded inventory proves about the base
// configuration: present, verifiably absent, or never read.
type previewBaseState int

const (
	// baseNeverRead: no run bundle exists, so no read covered the base
	// configuration. The repository layer is unknown, never empty.
	baseNeverRead previewBaseState = iota
	// baseNoPolicy: the verified snapshot records no base configuration.
	baseNoPolicy
	// baseHasPolicy: the verified snapshot carries the base configuration.
	baseHasPolicy
)

// previewInputs reads the proposed file or the recorded trusted-base
// inventory: the file wins outright (proposed, never trusted), else the
// inventory beside the run bundle, else the state the checkout proves.
func previewInputs(root, name, proposedPath string) (previewProvenance, *core.Config, *core.RefusalError) {
	if proposedPath != "" {
		return proposedInputs(proposedPath)
	}
	snap, raw, state, ref := previewInventory(root, name)
	if ref != nil {
		return previewProvenance{}, nil, ref
	}
	if state == baseNoPolicy {
		return previewProvenance{label: "no repository policy"}, nil, nil
	}
	if state == baseNeverRead {
		return previewProvenance{label: noBaseLabel, warning: noBaseWarning}, nil, nil
	}
	cfg, ref := Parse(raw)
	if ref != nil {
		return previewProvenance{}, nil, ref
	}
	if snap.File.Policy == controls.PolicyHead {
		return previewProvenance{label: "recorded worktree inventory"}, cfg, nil
	}
	return previewProvenance{label: "trusted base inventory", trusted: true, baseSHA: snapshotBaseSHA(snap)}, cfg, nil
}

// snapshotBaseSHA names the base commit the recorded policy was read at. A
// head-policy snapshot recorded the worktree instead, so it names no base
// rather than claiming one.
func snapshotBaseSHA(snap *controls.Snapshot) string {
	if snap == nil || snap.File.Policy != controls.PolicyBase {
		return ""
	}
	return snap.File.BaseSHA
}

// proposedInputs parses the operator's proposed file: it reports the proposed
// origin and is never trusted, whatever the base says.
func proposedInputs(path string) (previewProvenance, *core.Config, *core.RefusalError) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return previewProvenance{}, nil, refuse(CodeBadConfig, "could not read proposed config %s: %v", path, err)
	}
	cfg, ref := Parse(raw)
	if ref != nil {
		return previewProvenance{}, nil, ref
	}
	label := "proposed file " + path
	return previewProvenance{label: label, proposed: label}, cfg, nil
}

// labelProposed moves the provenance of every value and requirement the
// proposed file supplied from the tracked path to that file: the resolver
// records .verdict/config.json for the repository layer, but a proposed
// preview never read it. Layers, values, and keys do not move, so the
// untrusted bit and the proposed source are the only claims made here; a
// proposed input is never certified as trusted policy. Scope-sourced values
// keep their scope label, which names the scope and claims no trust.
func labelProposed(data *core.ConfigData, source string) {
	relabelRepoSource(data.Values, source)
	relabelRepoSource(data.Shadowed, source)
	for i := range data.Files {
		relabelRepoSource(data.Files[i].Values, source)
		relabelRepoSource(data.Files[i].Shadowed, source)
	}
	relabelRequirementSources(data.Requirements, repoSource(""), source)
}

// relabelRepoSource names the proposed file on every value the repository
// layer supplied, leaving every other layer's own label alone.
func relabelRepoSource(values []core.ConfigValue, source string) {
	for i := range values {
		if values[i].Layer == LayerRepo && values[i].Source == configSourceName {
			values[i].Source = source
		}
	}
}

// relabelRequirementSources renames one requirement provenance: a root clause
// the resolver attributes to the tracked configuration came from the proposed
// file instead, and a scope clause keeps its scope label.
func relabelRequirementSources(reqs []core.ConfigRequirement, from, to string) {
	for i := range reqs {
		for j := range reqs[i].Sources {
			if reqs[i].Sources[j] == from {
				reqs[i].Sources[j] = to
			}
		}
	}
}

// previewInventory reads the configuration the fetch captured at the trusted
// base, re-verifying the snapshot hashes first: a tampered or truncated
// snapshot refuses controls-stale instead of being previewed as trusted
// policy. Absence is reported only when it is proven, and an absent bundle is
// distinguished from an absent configuration.
func previewInventory(root, name string) (*controls.Snapshot, []byte, previewBaseState, *core.RefusalError) {
	dir, ref := BundleDir(root, name)
	if ref != nil {
		return nil, nil, baseNeverRead, ref
	}
	snap, ref := controls.GateRecorded(dir)
	if ref != nil {
		if missingBundle(dir) {
			return nil, nil, baseNeverRead, nil
		}
		return nil, nil, baseNeverRead, ref
	}
	raw, ok := snap.ReadFile(ConfigPath)
	if !ok {
		return snap, nil, baseNoPolicy, nil
	}
	return snap, raw, baseHasPolicy, nil
}

// missingBundle reports whether no run bundle exists at dir at all. Only a
// verifiable absence licenses the no-policy preview: a bundle path that
// exists but cannot be read (including a file where the bundle directory
// belongs) keeps the gate refusal instead of previewing as absent.
func missingBundle(dir string) bool {
	_, err := os.Stat(dir)
	return errors.Is(err, fs.ErrNotExist)
}

// previewFiles preserves the captured review identities, including rename
// endpoints. Legacy bundles without run.json fall back to their flat list.
func previewFiles(dir string) ([]FileInput, *core.RefusalError) {
	_, found, err := ReadRunState(dir)
	if err != nil {
		return nil, stale(dir, "run bundle is unreadable")
	}
	if !found {
		return legacyPreviewFiles(dir), nil
	}
	rs, ref := VerifyRunState(dir)
	if ref != nil {
		return nil, ref
	}
	if rs == nil {
		return nil, stale(dir, "run bundle disappeared during preview")
	}
	return capturedFileInputs(rs), nil
}

func capturedFileInputs(rs *RunState) []FileInput {
	if rs.Resolved == nil {
		return nil
	}
	files := make([]FileInput, 0, len(rs.Resolved.Files))
	for i := range rs.Resolved.Files {
		file := &rs.Resolved.Files[i]
		files = append(files, FileInput{Path: file.Path, OldPath: file.OldPath})
	}
	return files
}

func legacyPreviewFiles(dir string) []FileInput {
	raw, err := os.ReadFile(filepath.Join(dir, ".difffiles"))
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
func RunBundlePath(dir string) string {
	return filepath.Join(dir, runStateFileName)
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
	data.Shadowed = shadowValues(rs.Evidence.Shadowed)
	data.Plan = planGroups(rs.Evidence.Plan)
	data.Spend = spendView(rs.Resolved)
	data.Privacy = privacyView(rs.Resolved)
	if rs.Resolved != nil {
		data.Files = resolvedFiles(rs.Resolved)
	}
	for _, obs := range rs.Outcomes {
		data.Checks = append(data.Checks, core.ConfigCheck{
			Check: obs.Check, Group: obs.Group, Head: obs.Head,
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

// dataFromResolved resolves the current inputs with no outcomes yet: every
// prospective requirement reads pending, and the plan carries the checks the
// requirements add, so a preview names the work the run would enforce.
func dataFromResolved(in *Inputs, resolved *Resolved) *core.ConfigData {
	data := &core.ConfigData{
		Source:     "preview",
		PlanStatus: resolved.Plan.Status,
	}
	data.Values = resolvedValues(resolved)
	data.Requirements = prospectiveRequirements(in, resolved)
	data.Shadowed = shadowValues(resolved.Defaults.Shadowed)
	data.Files = resolvedFiles(resolved)
	data.Plan = planGroups(resolved.Plan)
	data.Spend = spendView(resolved)
	data.Privacy = privacyView(resolved)
	return data
}

// prospectiveRequirements renders the requirement set the fetch capture would
// store for these inputs: the root clauses, the per-file scope clauses, the
// required checks, and the bind, each with pending status and its sources. A
// preview therefore shows the missing prospective requirements instead of an
// empty list, and still certifies no compliance because none has evidence.
func prospectiveRequirements(in *Inputs, resolved *Resolved) []core.ConfigRequirement {
	merged := evidenceRequirements(in, resolved)
	out := make([]core.ConfigRequirement, 0, len(merged))
	for i := range merged {
		out = append(out, core.ConfigRequirement{
			Key:       merged[i].Key,
			Threshold: merged[i].Threshold,
			Status:    merged[i].Status,
			Sources:   append([]string{}, merged[i].Sources...),
		})
	}
	return out
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
	for i := range plan.Groups {
		group := &plan.Groups[i]
		groups = append(groups, core.ConfigPlanGroup{
			ID: group.ID, Label: group.Label, Profile: group.Profile, Depth: group.Depth,
			Checks: group.Checks, MaxLenses: group.MaxLenses, Files: group.Files, Scopes: group.Scopes,
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
