package recordcore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	packcore "github.com/stainless-code/verdict/internal/core/pack"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/identity"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Run executes `record <owner/repo> <local-slug>`: bundle gates, the
// shared merge, the record artifact, and the findings report. It never
// writes stdout/stderr or exits; human lines stream through the runtime
// progress observer, stores go through the shared atomic path, and the
// envelope inputs come back in the response for the adapter to render.
//
//nolint:revive // the gate order is the service contract
func Run(ctx context.Context, req *core.RecordRequest) (*core.RecordResponse, *core.RefusalError) {
	st := &svc{req: req, warnings: append([]string{}, req.Warnings...)}
	if err := ctx.Err(); err != nil {
		return nil, &core.RefusalError{Code: codeCanceled, Message: "canceled before record completed"}
	}
	dir, prov, headFull, now, ref := st.bundle()
	if ref != nil {
		return nil, st.withWarnings(ref)
	}
	check, ref := st.checkInputs(dir)
	if ref != nil {
		return nil, st.withWarnings(ref)
	}
	st.check = &check.Summary
	st.addCheckWarnings(check)
	if ref := reviewRefusal(check, false); ref != nil {
		return nil, st.withWarnings(ref)
	}
	snap, ref := controls.Gate(dir, readcommon.BundleBase(dir, st.store))
	if ref != nil {
		return nil, st.withWarnings(ref)
	}
	if ref := packcore.CheckLock(snap); ref != nil {
		return nil, st.withWarnings(ref)
	}
	st.controls = snap
	if rs, ref := st.verifyRunState(dir); ref != nil {
		return nil, st.withWarnings(ref)
	} else {
		st.run = rs
		st.warnLensUsage(dir, rs)
		st.warnDeepValidation(dir, rs)
	}
	if ref := reviewRefusal(check, strictRecord(st.req, snap)); ref != nil {
		return nil, st.withWarnings(ref)
	}
	review, ref := st.loadInputs(ctx, dir)
	if ref != nil {
		return nil, st.withWarnings(ref)
	}
	res, names, ref := st.mergeAndName(dir, prov, headFull, review)
	if ref != nil {
		return nil, st.withWarnings(ref)
	}
	st.warnCostModels(dir, names)
	if st.req.DryRun {
		return st.dryRun(dir, now, &res), nil
	}
	return st.finalize(ctx, dir, prov, headFull, now, &res)
}

// verifyRunState binds the bundle to its typed evidence through the
// same integrity check post enforces: a deleted or tampered run bundle
// refuses controls-stale instead of silently degrading to no policy.
// Record is not a binding action, so a bundle that predates typed
// evidence warns and proceeds; a pre-integrity bundle enforces its
// stored inputs and reports unavailable provenance.
func (st *svc) verifyRunState(dir string) (*config.RunState, *core.RefusalError) {
	rs, ref := config.VerifyRunState(dir)
	if ref != nil {
		return nil, ref
	}
	if rs == nil {
		st.addWarning("run bundle has no typed evidence (pre-typed bundle?) — recording without repository policy checks")
		return nil, nil
	}
	if !rs.Verified() {
		st.addWarning("typed evidence provenance unavailable (pre-integrity bundle) — stored inputs apply without verified evidence")
	}
	return rs, nil
}

// warnLensUsage preserves incomplete, over-cap, and unbound lens evidence
// as visible warnings on a record that stays available: record never
// certifies compliance, and silence is reported as unknown, not zero.
func (st *svc) warnLensUsage(dir string, rs *config.RunState) {
	usage := config.ReadLensUsage(rs, dir)
	for _, violation := range usage.Violations() {
		st.addWarning("profile lens evidence: " + violation.Detail)
	}
}

// warnDeepValidation surfaces each deep group's receipt standing on a record
// that stays available: record never certifies validation it cannot read.
func (st *svc) warnDeepValidation(dir string, rs *config.RunState) {
	head := ""
	if st.store != nil {
		head = st.store.Head()
	}
	for _, gap := range verifycore.DeepGaps(rs, dir, head, st.req.RepoRootPath()) {
		st.addWarning(gap)
	}
}

func (st *svc) checkInputs(dir string) (CheckResult, *core.RefusalError) {
	raw, err := readAgentFile(dir + reviewSuffix)
	if err != nil {
		if ref, ok := errors.AsType[*core.RefusalError](err); ok {
			return CheckResult{}, ref
		}
		return CheckResult{}, &core.RefusalError{Code: codeMissingReviewFile, Message: fmt.Sprintf(msgReviewMissing, dir+"/review.json")}
	}
	st.normalized, st.repairs = NormalizeReview(raw)
	res, ref := checkNormalized(dir, st.normalized, st.repairs)
	if ref != nil {
		return CheckResult{}, ref
	}
	return res, nil
}

func (st *svc) addCheckWarnings(check CheckResult) {
	for _, p := range check.Problems {
		if p.Level == core.ProblemLevelWarning {
			st.addWarning(p.Path + ": " + p.Message)
		}
	}
}

func (st *svc) addWarning(msg string) {
	if slices.Contains(st.warnings, msg) {
		return
	}
	st.warnings = append(st.warnings, msg)
	if st.req.Runtime != nil && st.req.Progress != nil {
		st.req.Progress.Warn(msg)
	}
}

func reviewRefusal(check CheckResult, strict bool) *core.RefusalError {
	problems := make([]core.Problem, len(check.Problems))
	copy(problems, check.Problems)
	for i := range problems {
		if problems[i].Level == core.ProblemLevelWarning && strict {
			problems[i].Level = core.ProblemLevelError
		}
	}
	if check.Errors == 0 && (!strict || check.Warnings == 0) {
		return nil
	}
	total := check.Errors + check.Warnings
	msg := fmt.Sprintf("review.json has %d problem(s); fix every one, then re-run", total)
	return &core.RefusalError{Code: codeBadReviewInput, Message: msg, Problems: problems, ProblemsTruncated: check.Truncated}
}

// strictRecord decides whether warnings refuse the run: the --strict flag,
// the fetch-time strict_input snapshot, or REVIEW_STRICT_INPUT=1 in the
// record request's own environment. The environment leg matters because the
// snapshot only captures fetch time, while CLI record, dry run, MCP, and SDK
// callers all carry their runtime env into this request.
func strictRecord(req *core.RecordRequest, snap *controls.Snapshot) bool {
	if req.Strict {
		return true
	}
	if snap.StrictInput() {
		return true
	}
	return runtimeStrictInput(req.Runtime)
}

// runtimeStrictInput mirrors the fetch-time REVIEW_STRICT_INPUT reading: a
// blank value leaves the snapshot in charge, any other value wins, and only
// "1" promotes.
func runtimeStrictInput(rt *core.Runtime) bool {
	if rt == nil {
		return false
	}
	raw, ok := rt.Env["REVIEW_STRICT_INPUT"]
	return ok && strings.TrimSpace(raw) == "1"
}

func (st *svc) bundle() (dir, prov, headFull, now string, ref *core.RefusalError) {
	req := st.req
	dir, ref = readcommon.ResolveWritableBundleDir(req.RepoRootPath(), req.Pr)
	if ref != nil {
		return "", "", "", "", ref
	}
	st.store = readcommon.OpenBundle(dir)
	if prov, ref = resolveProvider(dir, st.store, req.DryRun); ref != nil {
		return "", "", "", "", ref
	}
	if ref = checkBundleFiles(dir, prov); ref != nil {
		return "", "", "", "", ref
	}
	if !req.DryRun {
		if err := EnsureStore(dir); err != nil {
			return "", "", "", "", recordFailed(err)
		}
	}
	return dir, prov, resolveHead(dir, st.store, prov), req.TimeUTC(), nil
}

func (st *svc) loadInputs(ctx context.Context, dir string) (*jq.Obj, *core.RefusalError) {
	if ref := readcommon.CheckLocalFresh(ctx, st.identityCommander(), st.req.RepoRootPath(), dir); ref != nil {
		return nil, st.withWarnings(ref)
	}

	if st.req.DryRun {
		raw, err := os.ReadFile(dir + diffSuffix)
		if err != nil {
			return nil, &core.RefusalError{Code: codeMissingDiff, Message: fmt.Sprintf(msgMissingDiff, dir)}
		}
		st.hunkRows = hunks.Parse(string(raw))
	} else if err := EnsureHunksSnapshot(st.runtime(), dir); err != nil {
		return nil, refusalOrFailed(err)
	}
	review, err := LoadReviewBytes(st.normalized)
	if err != nil {
		return nil, refusalOrFailed(err)
	}
	return review, nil
}

// mergeAndName merges the review and then applies the cost-name plan validated
// before it. Planning comes first so a missing panel, a contradiction, or an
// unpriceable shape refuses while the bundle is untouched: a refused record
// leaves no findings behind for a later `fix` to consume. Applying writes
// nothing on a dry run. The returned pass is what the warning scan skips: a
// planned file's nameless entries are the repair message's, in both modes.
func (st *svc) mergeAndName(dir, prov, headFull string, review *jq.Obj) (MergeResult, *costNamePass, *core.RefusalError) {
	names, ref := st.planCostNames(dir)
	if ref != nil {
		return MergeResult{}, nil, ref
	}
	res, ref := st.merge(dir, prov, headFull, review)
	if ref != nil {
		return MergeResult{}, nil, ref
	}
	if ref := st.applyCostNames(names); ref != nil {
		return MergeResult{}, nil, ref
	}
	return res, names, nil
}

func (st *svc) merge(dir, prov, headFull string, review *jq.Obj) (MergeResult, *core.RefusalError) {
	req := st.req
	rt := st.runtime()
	res, err := Merge(&rt, &MergeParams{
		Dir:             dir,
		Prov:            prov,
		HeadFull:        headFull,
		Review:          review,
		ReviewFile:      dir + reviewSuffix,
		Root:            req.RootPath(),
		RepoRoot:        req.RepoRootPath(),
		Body:            reviewBodyOf(review),
		FindingsFile:    dir + "/findings.json",
		Effective:       req.Effective,
		Forces:          req.Forces,
		Repo:            req.Repo,
		Pr:              req.Pr,
		Cmd:             recordCommand,
		DryRun:          st.req.DryRun,
		Controls:        st.controls,
		Run:             st.run,
		HunkRows:        st.hunkRows,
		InputChecked:    true,
		RequireCoverage: st.req.Flags.RequireCoverage,
	})
	if err != nil {
		return MergeResult{}, refusalOrFailed(err)
	}
	return res, nil
}

func refusalOrFailed(err error) *core.RefusalError {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return ref
	}
	return recordFailed(err)
}

type svc struct {
	req        *core.RecordRequest
	controls   *controls.Snapshot
	run        *config.RunState
	check      *core.CheckSummary
	store      *readcommon.Bundle
	normalized []byte
	repairs    []Problem
	hunkRows   []hunks.Hunk
	warnings   []string
}

func (st *svc) runtime() core.Runtime {
	var rt core.Runtime
	if st.req.Runtime != nil {
		rt = *st.req.Runtime
	}
	rt.Progress = warnCollector{inner: rt.Progress, warnings: &st.warnings}
	return rt
}

type warnCollector struct {
	inner    core.Progress
	warnings *[]string
}

func (w warnCollector) Human(line string) {
	if w.inner != nil {
		w.inner.Human(line)
	}
}

func (w warnCollector) Warn(msg string) {
	*w.warnings = append(*w.warnings, msg)
	if w.inner != nil {
		w.inner.Warn(msg)
	}
}

func (w warnCollector) Diag(line string) {
	if w.inner != nil {
		w.inner.Diag(line)
	}
}

func (st *svc) withWarnings(ref *core.RefusalError) *core.RefusalError {
	ref.Warnings = st.warnings
	return ref
}

func recordFailed(err error) *core.RefusalError {
	return &core.RefusalError{Code: codeRecordFailed, Message: fmt.Sprintf("record failed: %v", err)}
}

// identityCommander returns the injected command runner for patch identity
// recomputation, or nil when the request carries none: without a runner the
// algorithm claim cannot be recomputed, so git-kind identities stay
// unstamped instead of trusted.
func (st *svc) identityCommander() core.Commander {
	if st.req == nil || st.req.Runtime == nil {
		return nil
	}
	return st.req.Commander
}

func (st *svc) finalize(ctx context.Context, dir, prov, headFull, now string, res *MergeResult) (*core.RecordResponse, *core.RefusalError) {
	nOpen := CountStatus(res.Entries, statusOpen)
	nDeferred := CountStatus(res.Entries, "deferred")
	nAddressed := CountStatus(res.Entries, "addressed")
	diffSHA := sha256File(dir + diffSuffix)
	toolVersion, promptsHash, model := provenance.Triple(st.req.RootPath(), dir)
	record := &core.Record{
		SchemaVersion:   "1",
		Status:          prov,
		Head:            headFull,
		DiffSHA256:      diffSHA,
		RecordedAt:      now,
		Findings:        len(res.Entries),
		Open:            nOpen,
		Verification:    res.Verification,
		Confidence:      intOrNil(res.Confidence),
		ConfidenceRaw:   res.Confidence,
		SecuritySummary: strOrNil(res.Security),
		ToolVersion:     toolVersion,
		PromptsHash:     promptsHash,
		Model:           modelOrNil(model),
		Repairs:         repairList(st.repairs),
		Coverage:        res.Coverage.Receipt(),
		ChunkCoverage:   res.Coverage.ChunkReceipt(),
	}
	base := readcommon.BundleBase(dir, st.store)
	if ident, ok := identity.Verify(ctx, st.identityCommander(), dir, identityHead(dir, prov, st.store), base); ok {
		record.PatchID, record.PatchIDKind = ident.PatchID, ident.PatchIDKind
	}
	if err := atomic.WriteFile(dir+"/record.json", []byte(jq.PrettyOrdered(record.Wire()))); err != nil {
		return nil, st.withWarnings(&core.RefusalError{
			Code: codeRecordFailed, Message: fmt.Sprintf("could not write %s/record.json: %v", dir, err),
		})
	}
	if st.req.Runtime != nil {
		st.req.Human(strings.TrimSuffix(fmt.Sprintf(msgRecorded, dir, nOpen, dir), "\n"))
	}
	return &core.RecordResponse{
		Repo: st.req.Repo,
		Pr:   st.req.Pr,
		At:   now,
		Data: &core.RecordData{
			Dir:             dir,
			Anchors:         core.AnchorsSummary{Kept: res.NKept, Moved: res.NMoved},
			Findings:        core.RecordFindings{Open: nOpen, Deferred: nDeferred, Addressed: nAddressed, File: res.FindingsFile},
			Verification:    res.Verification,
			Confidence:      intOrNil(res.Confidence),
			ConfidenceRaw:   res.Confidence,
			SecuritySummary: strOrNil(res.Security),
			Items:           typedItems(res.Entries),
			ItemsRaw:        EnvelopeItems(res.Entries),
		},
		Warnings: st.warnings,
		Next:     "report or triage the local bundle; post stays refused (no PR target)",
	}, nil
}

func (st *svc) dryRun(dir, now string, res *MergeResult) *core.RecordResponse {
	nOpen := CountStatus(res.Entries, statusOpen)
	nDeferred := CountStatus(res.Entries, "deferred")
	nAddressed := CountStatus(res.Entries, "addressed")
	dry := &core.RecordData{
		Dir:             dir,
		Anchors:         core.AnchorsSummary{Kept: res.NKept, Moved: res.NMoved},
		Findings:        core.RecordFindings{Open: nOpen, Deferred: nDeferred, Addressed: nAddressed},
		Verification:    res.Verification,
		Confidence:      intOrNil(res.Confidence),
		ConfidenceRaw:   res.Confidence,
		SecuritySummary: strOrNil(res.Security),
		Items:           typedItems(res.Entries),
		ItemsRaw:        EnvelopeItems(res.Entries),
		DryRun:          true,
		Check:           st.check,
		Normalized:      normalizedObject(st.normalized),
		Repairs:         repairList(st.repairs),
	}
	next := "re-run without --dry-run to record; fix every problem first"
	if st.store != nil && st.store.Provider() == "github" {
		next = "fix every problem, then post the review with verdict post"
	}
	return &core.RecordResponse{
		Repo:     st.req.Repo,
		Pr:       st.req.Pr,
		At:       now,
		Data:     dry,
		Warnings: st.warnings,
		Next:     next,
	}
}

func normalizedObject(raw []byte) any {
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return nil
	}
	return doc
}

func repairList(problems []Problem) []core.Repair {
	if len(problems) == 0 {
		return nil
	}
	out := make([]core.Repair, 0, len(problems))
	for _, p := range problems {
		out = append(out, core.Repair{Code: p.Code, Path: p.Path})
	}
	return out
}

func intOrNil(v any) *int {
	if v == nil {
		return nil
	}
	if n, ok := jq.IntOf(v); ok {
		return &n
	}
	return nil
}

func strOrNil(v any) *string {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}

func modelOrNil(model string) *string {
	if model == "" {
		return nil
	}
	return &model
}

func typedItems(entries []any) []core.FindingItem {
	items := make([]core.FindingItem, 0, len(entries))
	for _, e := range entries {
		m, ok := e.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		sv, _ := m.Get("sig")
		tv, _ := m.Get("status")
		av, _ := m.Get("anchored")
		items = append(items, core.FindingItem{
			Sig:      jq.StrOf(sv),
			Status:   jq.StrOf(tv),
			Anchored: av,
			Locator:  readcommon.Locator(m),
		})
	}
	return items
}

func resolveProvider(dir string, store *readcommon.Bundle, allowPR bool) (string, *core.RefusalError) {
	if readcommon.IsLocalBundleDir(dir) || readcommon.IsCorpusBundleDir(dir) {
		return providerLocal, nil
	}
	switch prov := store.Provider(); prov {
	case "":
		return "", &core.RefusalError{Code: codeNotLocal, Message: msgNotLocal}
	case "github":
		if allowPR {
			return prov, nil
		}
		return "", &core.RefusalError{Code: codeNotLocal, Message: msgNotGH}
	default:
		return prov, nil
	}
}

func checkBundleFiles(dir, prov string) *core.RefusalError {
	files := []struct {
		path string
		code string
		hint string
	}{
		{dir + reviewSuffix, codeMissingReviewFile, "agent must write {body, approved, comments[]}"},
		{dir + diffSuffix, "missing-diff", "run fetch-local / fetch --provider first"},
	}
	for _, f := range files {
		if !exists(f.path) {
			return &core.RefusalError{Code: f.code, Message: fmt.Sprintf("missing %s (%s)", f.path, f.hint)}
		}
	}
	source := dir + prSuffix
	hint := "run fetch --provider first"
	if prov == providerLocal {
		source = dir + sourceSuffix
		hint = "run fetch-local first"
	}
	if !exists(source) {
		return &core.RefusalError{Code: "missing-source", Message: fmt.Sprintf("missing %s (%s)", source, hint)}
	}
	return nil
}

// identityHead resolves the head the fetch wrote into identity.json: the
// local source record head (empty for a dirty worktree) or the PR record
// head. resolveHead's "dirty"/"unknown" display defaults never match a
// written identity, so they cannot verify here.
func identityHead(dir, prov string, store *readcommon.Bundle) string {
	if prov == providerLocal {
		return readcommon.JSONStr(dir+sourceSuffix, "head_sha")
	}
	if store == nil {
		store = readcommon.OpenBundle(dir)
	}
	return store.Head()
}

func resolveHead(dir string, store *readcommon.Bundle, prov string) string {
	if prov == providerLocal {
		if headFull := readcommon.JSONStr(dir+sourceSuffix, "head_sha"); headFull != "" {
			return headFull
		}
		return "dirty"
	}
	if headFull := store.Head(); headFull != "" {
		return headFull
	}
	return "unknown"
}

func sha256File(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
