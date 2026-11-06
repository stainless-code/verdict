package fetchcore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
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
func (rc *runConfig) resolvePhaseTwo(
	ctx context.Context, cmd Commander, repo, baseSHA string,
	chunkFlag bool, files []config.FileInput, settingsPath string,
) *core.RefusalError {
	rc.baseSHA = baseSHA
	base, ref := config.ReadBase(ctx, cmd, repo, rc.root, baseSHA)
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
	}
	resolved, rref := config.Resolve(in)
	if rref != nil {
		return rref
	}
	rc.inputs, rc.resolved = in, resolved
	return nil
}

// resolveLocalPhaseTwo resolves the fetch-local policy from the diff
// base commit: the reviewed content is local, so the base commit the
// diff is measured against carries the trusted inputs — never the
// mutable worktree. An unresolvable base (a fresh repository with no
// commit yet) reads no repository policy. Present base policy marks the
// run trusted with the base recorded; head edits never move it.
func (rc *runConfig) resolveLocalPhaseTwo(
	ctx context.Context, cmd Commander, baseSHA string, chunkFlag bool,
	files []config.FileInput, settingsPath string,
) *core.RefusalError {
	rc.baseSHA = baseSHA
	cfg, ref := rc.readBasePolicy(ctx, cmd, baseSHA)
	if ref != nil {
		return ref
	}
	in := &config.Inputs{
		Repo:         cfg,
		Personal:     rc.personal,
		BaseSHA:      baseSHA,
		SettingsPath: settingsPath,
		Files:        files,
		Flags:        flagPartial(chunkFlag),
		Env:          rc.envPartial,
	}
	resolved, rref := config.Resolve(in)
	if rref != nil {
		return rref
	}
	rc.inputs, rc.resolved = in, resolved
	return nil
}

// readBasePolicy reads and parses the trusted base policy when the run has a
// base commit: a present document becomes the raw capture the run is trusted
// with, and head edits never move it.
func (rc *runConfig) readBasePolicy(ctx context.Context, cmd Commander, baseSHA string) (*core.Config, *core.RefusalError) {
	if baseSHA == "" {
		return nil, nil
	}
	base, ref := config.ReadBase(ctx, cmd, "", rc.root, baseSHA)
	if ref != nil {
		return nil, ref
	}
	rc.base = base
	cfg, ref := config.ParseBase(base)
	if ref != nil {
		return nil, ref
	}
	if base.Present {
		rc.raw = base.Raw
		rc.trusted = true
	}
	return cfg, nil
}

// diffInputs resolves inputs from the raw captured diff before the bundle
// manifest exists: the hunk-bearing sections plus rename old paths, so scope
// matching and per-group caps see actual files, never a speculative list.
func diffInputs(dir string) []config.FileInput {
	raw := readOptional(dir + suffixFullDiff)
	files := diffHunkFiles(raw)
	slices.Sort(files)
	out := make([]config.FileInput, 0, len(files))
	for _, path := range dedupeSorted(files) {
		out = append(out, config.FileInput{Path: path})
	}
	return withRenames(dir, out)
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
		if name, prev, ok := renameOf(entry); ok {
			renames[name] = prev
		}
	}
	return renames
}

// renameOf reads one PR-metadata file entry's rename: the current name and the
// previous path, present only when the provider recorded a real move.
func renameOf(entry any) (name, prev string, ok bool) {
	em, ok := jq.AsMap(entry)
	if !ok {
		return "", "", false
	}
	name, ok = em["filename"].(string)
	if !ok || name == "" {
		return "", "", false
	}
	prev, ok = em["previous_filename"].(string)
	if !ok || prev == "" || prev == name {
		return "", "", false
	}
	return name, prev, true
}

// persistRunBundle writes the typed run bundle and the trusted-base
// inventory snapshot into the bundle directory after a successful
// bundle build: the allowlisted fetch-time evidence, the resolved
// policy, the inventory bytes with their capture-time hashes, and the
// combined pending plan. Later verbs append outcomes by reference
// without reconstructing inputs.
func (rc *runConfig) persistRunBundle(snap *controls.Snapshot, dir, name, capturedAt string) *core.RefusalError {
	if rc.resolved == nil || rc.inputs == nil {
		return nil
	}
	evidence := config.Capture(rc.inputs, rc.resolved, rc.raw, capturedAt)
	rawHash, filteredHash, ref := diffHashes(dir, rc.resolved.Privacy.Exclusions)
	if ref != nil {
		return ref
	}
	evidence.PatchSHA256, evidence.FilteredPatchSHA256 = rawHash, filteredHash
	rs := &config.RunState{
		Name:         name,
		BaseSHA:      rc.baseSHA,
		Trusted:      rc.trusted,
		CapturedAt:   capturedAt,
		ConfigSHA256: evidence.ConfigSHA256,
		Evidence:     evidence,
		Resolved:     rc.resolved,
		Root:         config.RootSet(rc.inputs.Repo, rc.baseSHA),
		Inventory:    config.InventoryHashes(snap),
	}
	if serr := config.WriteRunState(dir, rs); serr != nil {
		if ref := serr.Refusal(); ref != nil {
			return ref
		}
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write run state: %v", serr)}
	}
	return nil
}

// diffHashes binds the raw and filtered patch bytes to the evidence:
// the raw host diff the patch identity covers, and the same diff with
// privacy-excluded files excised — the model-facing review input. The
// roles stay labeled in the evidence; excluded content never leaves the
// bundle. A missing diff reads both hashes empty.
func diffHashes(dir string, exclusions []string) (raw, filtered string, ref *core.RefusalError) {
	body, err := os.ReadFile(dir + suffixFullDiff)
	if errors.Is(err, fs.ErrNotExist) {
		return "", "", nil
	}
	if err != nil {
		return "", "", &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(
			"cannot read %s%s: %v — refusing to record unbound diff hashes", dir, suffixFullDiff, err)}
	}
	filteredBytes, _, fref := filterDiffChecked(body, exclusions)
	if fref != nil {
		return "", "", fref
	}
	return sha256HexBytes(body), sha256HexBytes(filteredBytes), nil
}

// sha256HexBytes renders the lowercase hex SHA-256 of bytes.
func sha256HexBytes(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// ensureBundleState creates the ignore-first layout under the reviewed
// repository root before any bundle write, so generated state is never
// tracked. Reads never call it: validation, explanation, and preview
// must not create files.
func ensureBundleState(root string) *core.RefusalError {
	layout := config.NewLayout(root)
	if serr := layout.EnsureStateIgnore(); serr != nil {
		if ref := serr.Refusal(); ref != nil {
			return ref
		}
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not prepare state directory: %v", serr)}
	}
	if serr := layout.EnsureStateDir(); serr != nil {
		if ref := serr.Refusal(); ref != nil {
			return ref
		}
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not prepare state directory: %v", serr)}
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
// bundle plus the trusted-base inventory snapshot persist inside the
// bundle directory. Later verbs append check outcomes by reference
// without reconstructing these inputs.
func finalizeRemoteRun(ctx context.Context, ctx2 *fetchRunCtx, req *core.FetchRequest, f core.FetchFlags) *core.RefusalError {
	rc := ctx2.runCfg
	if rc == nil {
		return nil
	}
	dir := ctx2.dir
	baseSHA := readcommon.PrField(dir, keyBaseSHA)
	if ref := rc.resolvePhaseTwo(
		ctx, ctx2.runner.commander, req.Repo, baseSHA, f.ChunkFlag, fileInputs(dir), req.SettingsPath,
	); ref != nil {
		return ref
	}
	ctx2.runner.privacy = rc.resolved.Privacy
	return rc.persistRunBundle(ctx2.runner.controls, dir, req.Pr, core.NowUTC(ctx2.runner.Now()))
}

// finalizeLocalRun completes the fetch-local typed policy from the diff
// base inventory: the base commit the diff is measured against carries
// the trusted inputs, and the run bundle lands in the bundle directory
// beside the diff it describes.
func finalizeLocalRun(
	ctx context.Context, ctx2 *fetchRunCtx, req *core.FetchLocalRequest,
	f *core.FetchLocalFlags, baseSHA, listed string,
) *core.RefusalError {
	rc := ctx2.runCfg
	if rc == nil {
		return nil
	}
	if ref := rc.resolveLocalPhaseTwo(
		ctx, ctx2.runner.commander, baseSHA, f.ChunkFlag, listedInputs(ctx2.dir, listed), req.SettingsPath,
	); ref != nil {
		return ref
	}
	ctx2.runner.privacy = rc.resolved.Privacy
	name := "local-" + localSlug(req.Pr)
	return rc.persistRunBundle(ctx2.runner.controls, ctx2.dir, name, core.NowUTC(ctx2.runner.Now()))
}

// listedInputs converts the local stats file list to resolution inputs: the
// captured diff's rename record joins each listed new path to its old path,
// and the old path's own list entry folds into that join instead of standing
// as a second, ghost file. A rename therefore carries both paths through
// scope matching — and both paths' clauses, defaults, and provenance with it.
func listedInputs(dir, listed string) []config.FileInput {
	paths := listedPaths(listed)
	previous, folded := localRenames(dir, paths)
	out := make([]config.FileInput, 0, len(paths))
	for _, path := range paths {
		if folded[path] {
			continue
		}
		out = append(out, config.FileInput{Path: path, OldPath: previous[path]})
	}
	return out
}

// listedPaths splits the local stats file list into paths, blank lines gone.
func listedPaths(listed string) []string {
	var out []string
	for line := range strings.SplitSeq(listed, "\n") {
		if path := strings.TrimSpace(line); path != "" {
			out = append(out, path)
		}
	}
	return out
}

// localRenames reads the captured diff's own rename record: each new path
// maps to the old path its section header names (quote-safe), and each old
// path that joins one is marked folded so it stops being a file of its own.
// Both sides must be in the listed set, so a pathspec-filtered diff never
// hands back a pair the file list does not describe. Old paths are never
// inferred from a basename: a section counts only when Git recorded the
// rename itself.
func localRenames(dir string, listed []string) (previous map[string]string, folded map[string]bool) {
	sections := hunks.Files(readOptional(dir + suffixFullDiff))
	if len(sections) == 0 {
		return nil, nil
	}
	members := make(map[string]bool, len(listed))
	for _, path := range listed {
		members[path] = true
	}
	previous = make(map[string]string, len(sections))
	folded = make(map[string]bool, len(sections))
	for i := range sections {
		oldPath, newPath := sections[i].OldPath, sections[i].Path
		if oldPath == "" || newPath == "" || oldPath == newPath || !members[oldPath] || !members[newPath] {
			continue
		}
		previous[newPath] = oldPath
		folded[oldPath] = true
	}
	for newPath := range previous {
		// A path renamed twice stays an input in its own right.
		delete(folded, newPath)
	}
	return previous, folded
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
