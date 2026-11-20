// Package recordcore is the record domain service: `record` as a typed
// (ctx, request) → (response, refusal) call plus the validate_and_merge
// engine the post verb shares. It owns the merge, the typed
// findings/record stores, and the atomic writes; internal/record owns argv
// parsing, stream writes, and exit mapping.
package recordcore

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/approval"
	"github.com/stainless-code/verdict/internal/core/coverage"
	panelcore "github.com/stainless-code/verdict/internal/core/panel"
	"github.com/stainless-code/verdict/internal/core/reviewdoc"
	"github.com/stainless-code/verdict/internal/diagram"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const recordCommand = "record"

// MergeParams carries one shared merge invocation: the record and post
// verbs run the same validate_and_merge core over different bodies (the
// review body, or the drift-annotated post body with the hosting split
// hook), persisting to different findings files (dry runs stage to
// findings.dryrun.json).
type MergeParams struct { //nolint:govet // field grouping documents the merge contract
	Dir          string
	Prov         string
	HeadFull     string
	Review       *jq.Obj
	ReviewFile   string
	Root         string
	RepoRoot     string
	Banners      []string
	DryRun       bool
	MinSeverity  string
	FindingsFile string
	Effective    core.Effective
	Forces       *core.GlobalForces
	Repo         string
	Pr           string
	Cmd          string
	Controls     *controls.Snapshot
	Run          *config.RunState
	HunkRows     []hunks.Hunk
	InputChecked bool
	// RequireCoverage is the opt-in gate: absent or partial diff coverage
	// refuses coverage-incomplete. --force never bypasses it.
	RequireCoverage bool
	// BodyOnlyAnchors sends every finding to the body instead of an inline
	// anchor: the provider advertises inline_anchors=false, so an anchor would
	// be refused or silently dropped. Keep-biased: the finding survives, only
	// the inline position is lost.
	BodyOnlyAnchors bool
	// RequireRigor carries post's --require-rigor floors into the shared
	// merge; record leaves it empty and reads the base policy alone.
	RequireRigor []string
}

// MergeResult carries the shared merge outputs both verbs need.
type MergeResult struct { //nolint:govet // field order is the published property order
	Approval     *core.ApprovalReceipt
	Evidence     *core.EvidenceReceipt
	Verification *core.PanelSummary
	SpecChecks   []core.SpecCheck
	Receipts     []core.Receipt
	Validation   *ValidationResult
	RenderInput  *reviewdoc.RenderInput
	Body         string
	Diagram      diagram.Status
	Entries      []any
	Kept         []any
	KeptSigs     []string
	NKept        int
	NMoved       int
	HunkN        int
	Marker       string
	Dir          string
	FindingsFile string
	Confidence   any
	Security     any
	Risk         *core.RiskClaim
	Coverage     *coverage.Summary
	Warnings     []string
}

func (in *MergeParams) repoRootPath() string {
	if in.RepoRoot == "" {
		return "."
	}
	return in.RepoRoot
}

// Merge runs the shared validate_and_merge core: the merge engine, the
// findings write, KB credit staging, and the anchor/findings report. The
// record verb discards the curated body and finalizes the record artifact;
// the post verb carries the body into the payload. Errors are typed
// refusals matched with [errors.As] at the edge.
func Merge(rt *core.Runtime, in *MergeParams) (MergeResult, error) {
	if in.Controls == nil {
		return MergeResult{}, &core.RefusalError{Code: controls.CodeUnavailable, Message: "merge needs a controls snapshot"}
	}
	st := &state{
		review:          in.Review,
		forces:          in.Forces,
		progress:        rt.Progress,
		effective:       in.Effective,
		cmd:             in.Cmd,
		repo:            in.Repo,
		pr:              in.Pr,
		env:             rt.Env,
		now:             rt.TimeUTC(),
		findingsFile:    in.FindingsFile,
		dir:             in.Dir,
		reviewFile:      in.ReviewFile,
		root:            in.Root,
		repoRoot:        in.repoRootPath(),
		headFull:        in.HeadFull,
		headShort:       readcommon.ShortSHA(in.HeadFull),
		prov:            in.Prov,
		dryRun:          in.DryRun,
		controls:        in.Controls,
		run:             in.Run,
		minSeverity:     in.MinSeverity,
		hunkRows:        in.HunkRows,
		inputChecked:    in.InputChecked,
		requireCoverage: in.RequireCoverage,
		requireRigor:    in.RequireRigor,
		bodyOnlyAnchors: in.BodyOnlyAnchors,
	}
	panel, err := st.loadPanel(in)
	if err != nil {
		return MergeResult{}, agentRefusal(err)
	}
	// The evidence checks run before the merge builds entries: the pass stamps
	// each finding's quote standing, and the entries read that stamp, so the
	// stored verification and the receipt are one calculation.
	st.evidence = checkBundleEvidence(st.dir, in.Review)
	if err := st.validateAndMerge(); err != nil {
		return MergeResult{}, err
	}
	verification := st.applyPanel(panel)
	if err := st.persistMerged(); err != nil {
		return MergeResult{}, err
	}
	reportFindings(st)
	if st.hunkN == 0 {
		st.warn("no hunks indexed — anchors unchecked, everything kept")
	}
	var compliance *config.ReadCompliance
	if st.run != nil && st.run.Resolved != nil {
		// The decision reads the UNFILTERED set: clauseSigs has to see an
		// unverified blocker to refuse it, which a pre-filtered view hides.
		captured := config.EvaluateReadCompliance(st.run, in.HeadFull, approval.Views(st.entries))
		compliance = &captured
	}
	decision := approval.Evaluate(st.entries, "", "", compliance,
		reviewdoc.HasSpecViolation(in.Review), approval.Evidence{Receipt: st.evidence})
	specChecks := reviewdoc.SpecChecksTyped(in.Review)
	renderIn := st.buildRenderInput(in.Review, decision, verification, specChecks, in.Banners)
	curated := st.applyFooter(AppendCoverageLine(reviewdoc.Render(renderIn), st.coverage))
	st.setMarker(curated)
	return MergeResult{
		Body:         curated,
		RenderInput:  renderIn,
		Approval:     decision,
		Evidence:     st.evidence,
		SpecChecks:   specChecks,
		Receipts:     reviewdoc.ReceiptsTyped(in.Review),
		Verification: verification,
		Validation:   st.validation,
		Diagram:      diagram.Status{State: "absent"},
		Entries:      st.entries,
		Kept:         st.kept,
		KeptSigs:     st.keptSigs,
		NKept:        st.nKept,
		NMoved:       st.nMoved,
		HunkN:        st.hunkN,
		Marker:       st.marker,
		Dir:          st.dir,
		FindingsFile: st.findingsFile,
		Confidence:   st.confidence,
		Security:     st.security,
		Risk:         st.risk,
		Coverage:     st.coverage,
		Warnings:     st.warnings,
	}, nil
}

func (st *state) buildRenderInput(
	review *jq.Obj,
	decision *core.ApprovalReceipt,
	verification *core.PanelSummary,
	specChecks []core.SpecCheck,
	banners []string,
) *reviewdoc.RenderInput {
	baseURL := ""
	if provenance.EvidenceLinkable(st.dir, st.headFull) == core.FlagOn {
		baseURL = provenance.EvidenceBaseURL(st.dir, st.repo)
	}
	return &reviewdoc.RenderInput{
		Review:       review,
		Approval:     decision,
		Evidence:     st.evidence,
		Verification: verification,
		Risk:         st.risk,
		Entries:      st.entries,
		Kept:         settledRenderFindings(st.entries, st.kept, st.keptSigs),
		Moved:        settledRenderFindings(st.entries, st.moved, st.movedSigs),
		SpecChecks:   specChecks,
		Banners:      append([]string{}, banners...),
		Basis:        provenance.FormatBasisLine(review, st.entries, st.now),
		Security:     jq.StrOf(st.security),
		BaseURL:      baseURL,
		HeadFull:     st.headFull,
		Now:          st.now,
		BundleMeta:   reviewdoc.LoadBundleMeta(st.dir, st.headFull),
	}
}

func indexEntriesBySig(entries []any) map[string]*jq.Obj {
	bySig := make(map[string]*jq.Obj, len(entries))
	for _, entry := range entries {
		m, ok := entry.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		if s, ok := sigString(m); ok && s != "" {
			bySig[s] = m
		}
	}
	return bySig
}

func settledRenderFindings(entries, raw []any, sigs []string) []any {
	bySig := indexEntriesBySig(entries)
	out := make([]any, 0, len(raw))
	for i, item := range raw {
		if m, ok := item.(*jq.Obj); ok && m != nil {
			out = append(out, mergeRenderFinding(m, sigAt(sigs, i), bySig))
		}
	}
	return out
}

func mergeRenderFinding(raw *jq.Obj, sig string, bySig map[string]*jq.Obj) *jq.Obj {
	base := raw.Clone()
	if settled, ok := bySig[sig]; ok {
		base = settled.Clone()
		for _, k := range []string{fieldLine, fieldSide, fieldAnchored, fieldStartLine, fieldDropReason} {
			if v, has := raw.Get(k); has && v != nil {
				base.Set(k, v)
			} else {
				base.Delete(k)
			}
		}
	}
	return base
}

func sigAt(sigs []string, i int) string {
	if i < len(sigs) {
		return sigs[i]
	}
	return ""
}

func reportFindings(st *state) {
	line := fmt.Sprintf("» findings: %d sigs -> %s", len(st.entries), st.findingsFile)
	if st.dryRun && st.cmd == recordCommand {
		line = fmt.Sprintf("» findings: %d sigs (dry run — no findings file written)", len(st.entries))
	}
	st.human(line)
	for _, e := range st.entries {
		m, ok := e.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		sv, _ := m.Get("sig")
		tv, _ := m.Get("status")
		av, _ := m.Get("anchored")
		pv, _ := m.Get(fieldPath)
		hv, _ := m.Get("heading")
		st.human(fmt.Sprintf("  %s  %s  %s  %s",
			readcommon.Prefix(jq.Interp(sv)),
			jq.Interp(tv),
			jq.Interp(av),
			jq.Interp(jq.Alt(jq.Alt(pv, hv), "?")),
		))
	}
}

// LoadReview reads and gates a review.json file: the size cap and the JSON
// object shape. It normalizes first, so post reads the same repaired object
// record checks and stores; record's dry-run data.normalized is exactly what
// post merges. record's checker owns content validity.
func LoadReview(path string) (*jq.Obj, error) {
	reviewRaw, err := readAgentFile(path)
	if err != nil {
		if ref := agentRefusal(err); ref != nil {
			return nil, ref
		}
		return nil, &core.RefusalError{Code: codeMissingReviewFile, Message: fmt.Sprintf(msgReviewMissing, path)}
	}
	return LoadReviewBytes(reviewRaw)
}

// LoadReviewBytes decodes one already-read review.json document and keeps the
// record path's object and schema refusals. The bytes run through
// NormalizeReview first: normalization is idempotent, so record's
// already-normalized input decodes unchanged while post's raw file gains the
// same repairs record reports.
func LoadReviewBytes(reviewRaw []byte) (*jq.Obj, error) {
	normalized, _ := NormalizeReview(reviewRaw)
	reviewDoc, err := jq.DecodeOrdered(normalized)
	if err != nil {
		return nil, &core.RefusalError{Code: codeBadReviewInput, Message: msgBadReviewJSON}
	}
	review, ok := reviewDoc.(*jq.Obj)
	if !ok {
		return nil, &core.RefusalError{Code: codeBadReviewSchema, Message: msgReviewNotObject}
	}
	reviewdoc.Prepare(review)
	return review, nil
}

// EnsureStore backfills the store marker when absent.
func EnsureStore(dir string) error {
	if exists(dir + "/store.json") {
		return nil
	}
	return atomic.WriteFile(dir+"/store.json", []byte("{\n  \"schema_version\": \"1\"\n}\n"))
}

// EnsureHunksSnapshot rebuilds the hunk index from full.diff when the
// fetch-time snapshot is missing, warning that validation is best-effort.
func EnsureHunksSnapshot(rt core.Runtime, dir string) error {
	if exists(dir + "/hunks.tsv") {
		return nil
	}
	rt.Warn(fmt.Sprintf(msgHunksRebuild, dir))
	raw, err := os.ReadFile(dir + "/full.diff")
	if err != nil {
		return &core.RefusalError{Code: "missing-diff", Message: fmt.Sprintf(msgMissingDiff, dir)}
	}
	return atomic.WriteFile(dir+"/hunks.tsv", []byte(hunks.Snapshot(string(raw))))
}

// CountStatus counts entries with status.
func CountStatus(entries []any, status string) int {
	n := 0
	for _, e := range entries {
		if m, ok := e.(*jq.Obj); ok {
			if s, _ := m.Get(fieldStatus); s == status {
				n++
			}
		}
	}
	return n
}

// ReadOptional reads an optional input file; ok is false when missing or
// unreadable (absent inputs read as absent claims/priors, never failures).
func ReadOptional(path string) ([]byte, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return raw, true
}

const maxAgentBytes = 32 << 20

func readAgentFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, jq.Sealed("read agent file", err)
	}
	defer func() { _ = f.Close() }() // read-only handle; the read error rules
	if fi, statErr := f.Stat(); statErr == nil && fi.Size() > maxAgentBytes {
		return nil, agentCapRefusal(path)
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxAgentBytes+1))
	if err != nil {
		return nil, jq.Sealed("read agent file", err)
	}
	if len(raw) > maxAgentBytes {
		return nil, agentCapRefusal(path)
	}
	return raw, nil
}

func agentCapRefusal(path string) error {
	return &core.RefusalError{Code: codeBadReviewInput, Message: path +
		" exceeds the 32 MiB agent-input cap (shrink the review, then re-run)"}
}

func agentRefusal(err error) error {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return ref
	}
	return nil
}

func removeDerivedSnapshot(st *state) {
	if st.dryRun {
		return
	}
	if st.anchorHunks != st.dir+"/hunks.tsv" {
		removeBestEffort(st.anchorHunks)
	}
}

func removeBestEffort(path string) {
	_ = os.Remove(path)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

var markerRe = regexp.MustCompile(`(?m)^> (✅|ℹ️|⚠️|⛔|\[!IMPORTANT\]|\[!CAUTION\])`)

func (st *state) loadPanel(in *MergeParams) (*panelcore.File, error) {
	file, warnings, err := panelcore.Load(in.Dir, in.HeadFull, panelcore.Findings(in.Review))
	for _, warning := range warnings {
		st.warn(warning)
	}
	return file, agentRefusal(err)
}

func (st *state) applyPanel(file *panelcore.File) *core.PanelSummary {
	summary, warnings := panelcore.Apply(st.entries, file)
	for _, warning := range warnings {
		st.warn(warning)
	}
	return summary
}

func (st *state) persistMerged() error {
	if !st.dryRun || st.cmd != recordCommand {
		if err := writeFindings(st); err != nil {
			return err
		}
		st.kb.CommitStaged(st.findingsFile, st.now)
	}
	return nil
}
