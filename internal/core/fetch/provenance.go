package fetchcore

import (
	"fmt"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const overridesDirPrefix = ".verdict/overrides/"

// writeProvenanceReceipt builds and persists provenance.json after the bundle's
// artifacts and truncation receipt are finalized. Write errors warn and never
// fail fetch.
func writeProvenanceReceipt(ctx2 *fetchRunCtx, data *core.FetchData) {
	if ctx2 == nil || ctx2.runner == nil {
		return
	}
	dir := ctx2.dir
	if dir == "" && data != nil {
		dir = data.Dir
	}
	if dir == "" {
		return
	}
	in := buildProvenanceInput(ctx2, data, dir)
	receipt := provenance.BuildReceipt(in)
	if err := provenance.WriteReceipt(dir, receipt); err != nil {
		ctx2.runner.Warn(fmt.Sprintf("could not write %s: %v", provenance.ReceiptFile, err))
	}
}

func buildProvenanceInput(ctx2 *fetchRunCtx, data *core.FetchData, dir string) *provenance.ReceiptBuildInput {
	headSHA := readcommon.PrField(dir, keyHeadSHA)
	if headSHA == "" {
		headSHA = readcommon.JSONStr(dir+"/source.json", keyHeadSHA)
	}
	baseSHA := readcommon.BundleBase(dir, nil)
	if baseSHA == "" && ctx2.runCfg != nil {
		baseSHA = ctx2.runCfg.baseSHA
	}
	ctrls, overrides, learnings := collectControlInputs(ctx2)
	cfgSHA, cfgLayers, cfgVals := collectConfigInputs(ctx2.runCfg)
	var bundleTrunc *core.Truncation
	if data != nil {
		bundleTrunc = data.Receipt
	}
	return &provenance.ReceiptBuildInput{
		Root:         ctx2.root,
		Dir:          dir,
		HeadSHA:      headSHA,
		BaseSHA:      baseSHA,
		PanelSource:  ctx2.panelSrc,
		Panel:        slices.Clone(ctx2.panelList),
		ConfigSHA256: cfgSHA,
		ConfigLayers: cfgLayers,
		ConfigValues: cfgVals,
		Controls:     ctrls,
		Overrides:    overrides,
		Learnings:    learnings,
		Exclusions:   collectExclusionsInput(ctx2),
		KB:           collectKBInput(ctx2.kbOutcome),
		BundleTrunc:  bundleTrunc,
	}
}

func collectControlInputs(ctx2 *fetchRunCtx) (ctrls, overrides []provenance.ControlInput, learnings *provenance.ControlInput) {
	snap := ctx2.runner.controls
	if snap == nil {
		return nil, nil, nil
	}
	policy := snap.File.Policy
	for _, f := range snap.File.Files {
		ci := provenance.ControlInput{
			Path:   f.Path,
			Status: f.Status,
			Source: policy,
			SHA256: f.SHA256,
			Bytes:  f.Bytes,
		}
		switch {
		case f.Path == config.LearningsPath:
			learn := ci
			learn.Status = "included"
			learnings = &learn
		case strings.HasPrefix(f.Path, overridesDirPrefix):
			overrides = append(overrides, ci)
		default:
			ctrls = append(ctrls, ci)
		}
	}
	return ctrls, overrides, learnings
}

func collectConfigInputs(rc *runConfig) (sha string, layers []string, vals []provenance.ConfigOriginInput) {
	if rc == nil || rc.resolved == nil || rc.inputs == nil {
		return "", nil, nil
	}
	ev := config.Capture(rc.inputs, rc.resolved, rc.raw, "")
	seenLayer := map[string]bool{}
	vals = make([]provenance.ConfigOriginInput, 0, len(ev.Values))
	for _, v := range ev.Values {
		if v.Origin.Layer != "" && !seenLayer[v.Origin.Layer] {
			seenLayer[v.Origin.Layer] = true
			layers = append(layers, v.Origin.Layer)
		}
		vals = append(vals, provenance.ConfigOriginInput{
			Key:    v.Key,
			Layer:  v.Origin.Layer,
			Source: v.Origin.Source,
		})
	}
	slices.Sort(layers)
	return ev.ConfigSHA256, layers, vals
}

func collectExclusionsInput(ctx2 *fetchRunCtx) provenance.ExclusionsInput {
	ex := ctx2.exclusions()
	if ex == nil {
		return provenance.ExclusionsInput{}
	}
	return provenance.ExclusionsInput{
		Present:           true,
		RawDiffSHA256:     ex.RawDiffSHA256,
		DiffSHA256:        ex.DiffSHA256,
		PatternsSource:    ex.PatternsSource,
		Excluded:          len(ex.Excluded),
		ExcludedBytes:     int64(ex.ExcludedBytes),
		KeptBytes:         int64(ex.KeptBytes),
		Defaults:          len(ex.Defaults) > 0,
		Ignore:            len(ex.Ignore) > 0,
		NoDefaultExcludes: ex.NoDefaultExcludes,
	}
}

func collectKBInput(kb *kbRecallOutcome) *provenance.KBRecallInput {
	if kb == nil {
		return nil
	}
	cands := make([]provenance.KBCandidateInput, 0, len(kb.Candidates))
	for _, c := range kb.Candidates {
		cands = append(cands, provenance.KBCandidateInput{
			ID:     c.ID,
			Status: c.Status,
			Reason: c.Reason,
		})
	}
	return &provenance.KBRecallInput{
		Candidates:    cands,
		Cap:           kb.Cap,
		Reason:        kb.Reason,
		Included:      kb.Included,
		Omitted:       kb.Omitted,
		CharsIncluded: kb.CharsIncluded,
		CharsTotal:    kb.CharsTotal,
		Truncated:     kb.Truncated,
		NotesWritten:  kb.NotesWritten,
	}
}
