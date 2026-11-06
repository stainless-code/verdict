package fetchcore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// runConfig carries the typed configuration for one fetch run: the
// trusted-base read, the operator settings, the parsed environment
// layer, and the resolution. Phase one (no repository policy yet)
// guards the spend before any provider call; phase two (base known)
// resolves the full policy for the bundle build.
type runConfig struct {
	resolved   *config.Resolved
	inputs     *config.Inputs
	base       *config.BaseConfig
	personal   *config.PersonalSettings
	root       string
	baseSHA    string
	raw        []byte
	envWarns   []string
	envPartial config.Partial
	trusted    bool
}

// resolvePhaseOne parses the invocation layers that need no base
// commit: the operator settings file, the consumed environment names,
// and the flag opt-ins. Absent settings read as no personal layer;
// malformed settings refuse before any provider call.
func resolvePhaseOne(env map[string]string, settingsPath string, chunkFlag bool) (*runConfig, *core.RefusalError) {
	rc := &runConfig{}
	personal, ref := loadPersonalLayer(settingsPath)
	if ref != nil {
		return nil, ref
	}
	rc.personal = personal
	partial, warns, ref := config.ParseEnv(env)
	if ref != nil {
		return nil, ref
	}
	rc.envPartial = partial
	rc.envWarns = warns
	resolved, ref := config.Resolve(&config.Inputs{
		Personal: personal,
		Env:      partial,
		Flags:    flagPartial(chunkFlag),
	})
	if ref != nil {
		return nil, ref
	}
	rc.resolved = resolved
	return rc, nil
}

// flagPartial carries the invocation opt-ins that outrank every other
// layer: only a given flag sets its field, so an unset flag never
// shadows the repository or personal layers beneath it.
func flagPartial(chunkFlag bool) config.Partial {
	var out config.Partial
	if chunkFlag {
		chunk := true
		out.Chunk = &chunk
	}
	return out
}

// loadPersonalLayer reads the operator settings file: absent reads as
// no personal layer, present parses through the strict personal
// parser. The path is empty when the caller supplied none.
func loadPersonalLayer(settingsPath string) (*config.PersonalSettings, *core.RefusalError) {
	if settingsPath == "" {
		return &config.PersonalSettings{}, nil
	}
	settings, ref := config.LoadPersonal(settingsPath)
	if ref != nil {
		return nil, ref
	}
	return settings, nil
}

// resolvePhaseTwo reads the trusted-base configuration at the base
// commit and resolves the full policy over the phase-one layers with
// the review file list. Absent base policy resolves personal, env,
// flags, and built-ins (the no-init rule); present policy parses
// strictly and refuses before the bundle builds.
func (rc *runConfig) resolvePhaseTwo(ctx context.Context, cmd Commander, repo, baseSHA string, chunkFlag bool, files []config.FileInput, settingsPath string, legacy config.LegacyInputs) *core.RefusalError {
	rc.baseSHA = baseSHA
	base, ref := config.ReadBase(ctx, cmd, repo, baseSHA)
	if ref != nil {
		return ref
	}
	rc.base = base
	cfg, ref := config.ParseBase(base)
	if ref != nil {
		return ref
	}
	if base.Present {
		rc.raw = base.Raw
		rc.trusted = true
	}
	in := &config.Inputs{
		Repo:         cfg,
		Personal:     rc.personal,
		BaseSHA:      baseSHA,
		SettingsPath: settingsPath,
		Files:        files,
		Flags:        flagPartial(chunkFlag),
		Env:          rc.envPartial,
		Legacy:       legacy,
	}
	resolved, ref := config.Resolve(in)
	if ref != nil {
		return ref
	}
	rc.inputs, rc.resolved = in, resolved
	return nil
}

// resolveLocalPhaseTwo resolves the fetch-local policy from the
// worktree configuration: the reviewed content is local, so the
// worktree file is the trusted input and no base commit is needed.
func (rc *runConfig) resolveLocalPhaseTwo(root string, chunkFlag bool, files []config.FileInput, settingsPath string, legacy config.LegacyInputs) *core.RefusalError {
	raw, err := os.ReadFile(filepath.Join(stateRoot(root), config.ConfigPath))
	if err != nil && !os.IsNotExist(err) {
		return &core.RefusalError{Code: config.CodeBadConfig, Message: fmt.Sprintf("could not read %s: %v", config.ConfigPath, err)}
	}
	var cfg *core.Config
	if err == nil {
		parsed, ref := config.Parse(raw)
		if ref != nil {
			return ref
		}
		cfg, rc.raw, rc.trusted = parsed, raw, true
	}
	in := &config.Inputs{
		Repo:         cfg,
		Personal:     rc.personal,
		SettingsPath: settingsPath,
		Files:        files,
		Flags:        flagPartial(chunkFlag),
		Env:          rc.envPartial,
		Legacy:       legacy,
	}
	resolved, ref := config.Resolve(in)
	if ref != nil {
		return ref
	}
	rc.inputs, rc.resolved = in, resolved
	return nil
}

// stateRoot anchors the run-state bundle to the reviewed repository:
// the working directory, exactly like the legacy diff bundle beside it.
// The request root names the tool checkout (trusted prompts), never the
// reviewed tree, so state never follows it.
func stateRoot(_ string) string {
	return "."
}

// legacyInputs reads the legacy knob files from the reviewed snapshot:
// the base commit for remote runs, the reviewed worktree for local runs.
// Absent files read absent; parsing owns malformed values.
func legacyInputs(r *session) config.LegacyInputs {
	var out config.LegacyInputs
	if raw, ok := r.readControl(".review/panel"); ok {
		out.Panel = raw
	}
	if raw, ok := r.readControl(".review/budget"); ok {
		out.Budget = raw
	}
	if raw, ok := r.readControl(".review/max_bytes"); ok {
		out.MaxBytes = raw
	}
	return out
}

// fileInputs converts the bundle's diff file list to resolution
// inputs. Rename old paths ride beside the new ones when the bundle
// records them, so a rename can never evade a clause.
func fileInputs(dir string) []config.FileInput {
	raw, err := os.ReadFile(dir + "/.difffiles")
	if err != nil {
		return nil
	}
	var out []config.FileInput
	for line := range strings.SplitSeq(string(raw), "\n") {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}
		out = append(out, config.FileInput{Path: path})
	}
	return withRenames(dir, out)
}

// withRenames attaches pre-rename paths from the bundle's PR metadata
// when the provider recorded them (GitHub's previous_filename): the
// clause match already covers both paths, so a rename can never evade.
func withRenames(dir string, files []config.FileInput) []config.FileInput {
	renames := renameMap(dir)
	if len(renames) == 0 {
		return files
	}
	for i, file := range files {
		if old, ok := renames[file.Path]; ok {
			files[i].OldPath = old
		}
	}
	return files
}

// renameMap reads the bundle's pre-rename paths from the PR metadata
// when the provider recorded them (GitHub's previous_filename).
func renameMap(dir string) map[string]string {
	renames := map[string]string{}
	raw, err := os.ReadFile(dir + suffixPRJSON)
	if err != nil {
		return renames
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return renames
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return renames
	}
	for _, entry := range jq.Elements(m["files"]) {
		em, ok := jq.AsMap(entry)
		if !ok {
			continue
		}
		name, ok := em["filename"].(string)
		if !ok || name == "" {
			continue
		}
		prev, ok := em["previous_filename"].(string)
		if !ok || prev == "" || prev == name {
			continue
		}
		renames[name] = prev
	}
	return renames
}

// persistRunBundle writes the run-state bundle and the trusted-base
// inventory snapshot under `.verdict/state/<name>/` after a successful
// bundle build: the allowlisted fetch-time evidence, the resolved
// policy, the inventory bytes, and the combined pending plan. Later
// verbs append outcomes by reference without reconstructing inputs.
func (rc *runConfig) persistRunBundle(ctx context.Context, cmd Commander, repo, dir, name, capturedAt string, inventory map[string][]byte) *core.RefusalError {
	if rc.resolved == nil || rc.inputs == nil {
		return nil
	}
	evidence := config.Capture(rc.inputs, rc.resolved, rc.raw, capturedAt)
	rs := &config.RunState{
		BaseSHA:      rc.baseSHA,
		Trusted:      rc.trusted,
		CapturedAt:   capturedAt,
		ConfigSHA256: evidence.ConfigSHA256,
		Evidence:     evidence,
		Resolved:     rc.resolved,
		Root:         config.RootSet(rc.inputs.Repo, rc.baseSHA),
	}
	if serr := config.WriteRunState(stateRoot(rc.root), name, rs); serr != nil {
		if ref := serr.Refusal(); ref != nil {
			return ref
		}
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write run state: %v", serr)}
	}
	if serr := config.WriteInventory(stateRoot(rc.root), name, inventory); serr != nil {
		if ref := serr.Refusal(); ref != nil {
			return ref
		}
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write inventory snapshot: %v", serr)}
	}
	return nil
}

// budgetRefusal maps a spend-gate failure: typed refusals pass
// through, anything else becomes a fetch failure at the boundary.
func budgetRefusal(err error) *core.RefusalError {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return ref
	}
	return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("spend gate failed: %v", err)}
}

// finalizeRemoteRun completes the typed policy once the bundle's file
// list exists: phase two re-resolves with per-file inputs, and the run
// bundle plus the trusted-base inventory snapshot persist under
// `.verdict/state/<pr>/`. Later verbs append check outcomes by
// reference without reconstructing these inputs.
func finalizeRemoteRun(ctx context.Context, ctx2 *fetchRunCtx, req *core.FetchRequest, f core.FetchFlags) *core.RefusalError {
	rc := ctx2.runCfg
	if rc == nil {
		return nil
	}
	dir := ctx2.dir
	baseSHA := readcommon.PrField(dir, keyBaseSHA)
	if ref := rc.resolvePhaseTwo(ctx, ctx2.runner.commander, req.Repo, baseSHA, f.ChunkFlag, fileInputs(dir), req.SettingsPath, legacyInputs(ctx2.runner)); ref != nil {
		return ref
	}
	ctx2.runner.privacy = rc.resolved.Privacy
	inventory, warnings, ref := config.SnapshotInventory(ctx, ctx2.runner.commander, req.Repo, baseSHA)
	if ref != nil {
		return ref
	}
	for _, warn := range warnings {
		ctx2.runner.Warn(warn)
	}
	return rc.persistRunBundle(ctx, ctx2.runner.commander, req.Repo, dir, req.Pr, core.NowUTC(ctx2.runner.Now()), inventory)
}

// finalizeLocalRun completes the fetch-local typed policy from the
// worktree inventory: the reviewed content is local, so the snapshot
// reads the worktree files and the bundle lands under
// `.verdict/state/local-<slug>/`.
func finalizeLocalRun(ctx context.Context, ctx2 *fetchRunCtx, req *core.FetchLocalRequest, f *core.FetchLocalFlags, listed string) *core.RefusalError {
	rc := ctx2.runCfg
	if rc == nil {
		return nil
	}
	if ref := rc.resolveLocalPhaseTwo(stateRoot(req.RootPath()), f.ChunkFlag, listedInputs(listed), req.SettingsPath, legacyInputs(ctx2.runner)); ref != nil {
		return ref
	}
	ctx2.runner.privacy = rc.resolved.Privacy
	inventory := config.SnapshotWorktreeInventory(stateRoot(req.RootPath()))
	name := "local-" + localSlug(req.Pr)
	return rc.persistRunBundle(ctx, ctx2.runner.commander, req.Repo, ctx2.dir, name, core.NowUTC(ctx2.runner.Now()), inventory)
}

// listedInputs converts the local stats file list to resolution inputs:
// renames have no old-path record locally, so matching covers the new
// paths the diff names.
func listedInputs(listed string) []config.FileInput {
	var out []config.FileInput
	for line := range strings.SplitSeq(listed, "\n") {
		if path := strings.TrimSpace(line); path != "" {
			out = append(out, config.FileInput{Path: path})
		}
	}
	return out
}

// writeTailReceipt records one fresh receipt for a consented over-budget
// or blind run at the tail, once per run: the phase gates arm the flag,
// the tail writes it beside the bundle it consented to.
func writeTailReceipt(ctx2 *fetchRunCtx, budget *budgetState) {
	if budget == nil || !budget.receiptNeeded {
		return
	}
	writeBudgetReceipt(ctx2.runner, budget, ctx2.dir, core.NowUTC(ctx2.runner.Now()))
}

// persistRunBundle uses the stored phase-two inputs: the capture replays
// the provenance the resolution already computed without re-reading.
