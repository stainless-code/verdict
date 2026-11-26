package fetchcore

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/budget"
	"github.com/stainless-code/verdict/internal/diagram"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/overrides"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// previewScratchPrefix names the throwaway directory a preview stages its host
// reads in. A preview never writes the bundle: the scratch dir lives outside
// the reviewed repository, and the run removes it before returning.
const previewScratchPrefix = "verdict-preview-"

// cleanScratch removes a preview's staging directory. A preview never writes
// the bundle, so nothing outside this directory may survive the run.
func (c *fetchRunCtx) cleanScratch() {
	if c.scratch == "" {
		return
	}
	_ = os.RemoveAll(c.scratch)
}

// claimBundleDir creates the run's bundle leaf and records it as the run's
// own only when this run's exclusive create made it: a bundle another run
// created, or one that existed before, is never ours to remove. ctx2.dir is
// set here, at creation, so a refusal on the very next step still cleans up.
func (c *fetchRunCtx) claimBundleDir(root, dir string) *core.RefusalError {
	created, serr := config.NewLayout(root).CreateBundleDir(filepath.Base(dir))
	if serr != nil {
		return stateRefusal(serr, dir)
	}
	c.dir, c.bundleRoot, c.createdDir = dir, root, created
	return nil
}

// discardCreatedBundle removes the bundle directory a refused run created,
// so a refusal leaves no bundle without its source.json, and returns a
// warning when the removal fails. A bundle that existed before the run is
// kept.
func (c *fetchRunCtx) discardCreatedBundle() string {
	if !c.createdDir || c.preview || c.dir == "" {
		return ""
	}
	if serr := config.NewLayout(c.bundleRoot).RemoveBundleDir(filepath.Base(c.dir)); serr != nil {
		return core.Msgf("could not remove the refused run's bundle %s: %s", c.dir, serr.Error())
	}
	return ""
}

// discardRefused removes the bundle a refused run created and carries a
// failed removal as a refusal warning.
func (st *state) discardRefused(ctx2 *fetchRunCtx, err error) error {
	msg := ctx2.discardCreatedBundle()
	if msg == "" {
		return err
	}
	ctx2.runner.Warn(msg)
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		ref.Warnings = st.warnings
	}
	return err
}

// previewStage is the directory a preview stages host work in, creating it on
// first use. `fetch-local --preview --worktree` takes it for its isolated
// checkout, so a command that writes no bundle does not create
// `.verdict/state` inside the repository it was asked to leave alone. A
// non-preview run gets "" and keeps the shared bundle home.
func (c *fetchRunCtx) previewStage() (string, *core.RefusalError) {
	if !c.preview {
		return "", nil
	}
	if c.scratch != "" {
		return c.scratch, nil
	}
	scratch, ref := previewScratch()
	if ref != nil {
		return "", ref
	}
	c.scratch = scratch
	return scratch, nil
}

// previewScratch makes the directory a preview stages into. The caller owns the
// removal; the service does it once, on every path, in its deferred cleanup.
func previewScratch() (string, *core.RefusalError) {
	dir, err := os.MkdirTemp("", previewScratchPrefix)
	if err != nil {
		return "", &core.RefusalError{Code: CodeFetchFailed, Message: core.Msgf(
			"preview could not create its scratch directory: %v", err)}
	}
	// macOS ships a TMPDIR under a symlinked /var, and every confined read
	// rejects a symlinked parent: resolve the real path once, here, so the
	// staging reads go through the same confinement a bundle read does.
	resolved, rerr := filepath.EvalSymlinks(dir)
	if rerr != nil {
		_ = os.RemoveAll(dir)
		return "", &core.RefusalError{Code: CodeFetchFailed, Message: core.Msgf(
			"preview could not resolve its scratch directory: %v", rerr)}
	}
	return resolved, nil
}

// buildPreview renders the preview payload from the artifacts the staging pass
// produced: the filtered diff, its exclusion receipt, and the same classifier
// `run` uses. The rows and the classification inputs come from the filtered
// diff alone, never from PR.json: the staging pass writes PR.json before the
// filter runs, so it still names every file the PR touched, withheld ones
// included. Reading it would list a secret as reviewed and size the review
// from the PR instead of from the diff the reviewer would actually receive.
func buildPreview(ctx2 *fetchRunCtx, filtered string, receipt *ExcludedFile) *core.PreviewData {
	included, files := reviewedRows(filtered)
	excluded := receipt.Excluded
	if excluded == nil {
		excluded = []ExcludedPath{}
	}
	decision := budget.Classify(previewInputs(ctx2, files, len(filtered)))
	return &core.PreviewData{
		SchemaVersion: previewSchemaVersion,
		Included:      included,
		Excluded:      excluded,
		IncludedBytes: len(filtered),
		ExcludedBytes: receipt.ExcludedBytes,
		Files:         len(included),
		Budget: core.PreviewBudget{
			Tier:       decision.Tier,
			Reasons:    decision.Reasons,
			Finders:    decision.Finders,
			CrossCheck: budget.CrossForCount(decision.Finders),
		},
	}
}

// reviewedRows renders the reviewed rows from the filtered diff alone: one row
// per path, in diff order, with the bytes summed when a path carries more than
// one section, so a repeated section neither doubles a file in the list nor
// inflates the count the budget compares. A preview writes no roles.txt, so the
// role comes from the path's shape — the same fallback the bundle reader uses.
func reviewedRows(filtered string) ([]core.PreviewFile, []budget.File) {
	sections := hunks.Files(filtered)
	included := make([]core.PreviewFile, 0, len(sections))
	files := make([]budget.File, 0, len(sections))
	at := make(map[string]int, len(sections))
	for i := range sections {
		f := &sections[i]
		if f.Path == "" {
			continue
		}
		if seen, dup := at[f.Path]; dup {
			included[seen].Bytes += int(f.Size)
			continue
		}
		role := readcommon.PathRole(f.Path)
		at[f.Path] = len(included)
		included = append(included, core.PreviewFile{Path: f.Path, Role: role, Bytes: int(f.Size)})
		files = append(files, budget.File{Path: f.Path, PreviousPath: f.PreviousPath, Role: role})
	}
	return included, files
}

// previewSchemaVersion is the preview payload's published schema version.
const previewSchemaVersion = "1"

// previewInputs assembles the classifier's inputs the way a run does — the
// walkthrough thresholds the resolved controls name, the built-in sensitive
// globs extended by the repository configuration — over the reviewed rows the
// filter kept, so an excluded path can neither enlarge the diff nor escalate
// the tier.
func previewInputs(ctx2 *fetchRunCtx, files []budget.File, diffBytes int) *budget.Inputs {
	maxFiles, maxBytes := diagram.Thresholds(overrides.At(filepath.Join(ctx2.dir, controls.DirName, "overrides")))
	globs := budget.BuiltinSensitive()
	if ctx2.runCfg != nil && ctx2.runCfg.resolved != nil {
		globs = append(globs, ctx2.runCfg.resolved.Defaults.Values.SensitivePaths...)
	}
	return &budget.Inputs{
		Files: files, FileCount: len(files), DiffBytes: diffBytes,
		MaxFiles: maxFiles, MaxBytes: maxBytes, Sensitive: globs,
	}
}

// previewResponse turns a finished preview into the service response: the
// envelope identity, the warnings the run raised before the filter, and the
// preview payload, with no bundle next step because nothing was written. The
// warnings travel with the response exactly as they do on a bundle run; a
// preview that swallowed them would hide the reason its own numbers differ
// from a full fetch.
func previewResponse(ctx2 *fetchRunCtx, warnings []string, repo, pr, at string) *Response {
	return &Response{
		Repo:     repo,
		Pr:       pr,
		At:       at,
		Warnings: warnings,
		Data:     &core.FetchData{Preview: ctx2.previewData},
	}
}
