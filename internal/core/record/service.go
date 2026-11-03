package recordcore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/controls"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Run executes `record <owner/repo> <local-slug>`: bundle gates, the
// shared merge, the record artifact, and the findings report. It never
// writes stdout/stderr or exits; human lines stream through the runtime
// progress observer, stores go through the shared atomic path, and the
// envelope inputs come back in the response for the adapter to render.
func Run(ctx context.Context, req *core.RecordRequest) (*core.RecordResponse, *core.RefusalError) {
	st := &svc{req: req, warnings: append([]string{}, req.Warnings...)}
	if err := ctx.Err(); err != nil {
		return nil, &core.RefusalError{Code: codeCanceled, Message: "canceled before record completed"}
	}
	dir, prov, headFull, now, ref := st.bundle()
	if ref != nil {
		return nil, st.withWarnings(ref)
	}
	review, ref := st.loadInputs(dir)
	if ref != nil {
		return nil, st.withWarnings(ref)
	}
	snap, ref := controls.Gate(dir, BundleBase(dir, st.store))
	if ref != nil {
		return nil, st.withWarnings(ref)
	}
	st.controls = snap
	res, ref := st.merge(dir, prov, headFull, review)
	if ref != nil {
		return nil, st.withWarnings(ref)
	}
	return st.finalize(dir, prov, headFull, now, &res)
}

// bundle resolves the bundle dir, provider, store, head, and clock.
func (st *svc) bundle() (dir, prov, headFull, now string, ref *core.RefusalError) {
	req := st.req
	dir = readcommon.ResolveBundleDir(req.Pr)
	st.store = readcommon.OpenBundle(dir)
	if prov, ref = resolveProvider(dir, st.store); ref != nil {
		return "", "", "", "", ref
	}
	if ref = checkBundleFiles(dir, prov); ref != nil {
		return "", "", "", "", ref
	}
	if err := EnsureStore(dir); err != nil {
		return "", "", "", "", recordFailed(err)
	}
	return dir, prov, resolveHead(dir, st.store, prov), req.TimeUTC(), nil
}

// loadInputs snapshots hunks and loads the review, mapping core errors to
// typed refusals.
func (st *svc) loadInputs(dir string) (*jq.Obj, *core.RefusalError) {
	if err := EnsureHunksSnapshot(st.runtime(), dir); err != nil {
		return nil, refusalOrFailed(err)
	}
	review, err := LoadReview(dir + "/review.json")
	if err != nil {
		return nil, refusalOrFailed(err)
	}
	return review, nil
}

// merge runs the shared validate_and_merge core for the record verb.
func (st *svc) merge(dir, prov, headFull string, review *jq.Obj) (MergeResult, *core.RefusalError) {
	req := st.req
	rt := st.runtime()
	res, err := Merge(&rt, &MergeParams{
		Dir:          dir,
		Prov:         prov,
		HeadFull:     headFull,
		Review:       review,
		ReviewFile:   dir + "/review.json",
		Root:         req.RootPath(),
		Body:         reviewBodyOf(review),
		FindingsFile: dir + "/findings.json",
		Effective:    req.Effective,
		Forces:       req.Forces,
		Repo:         req.Repo,
		Pr:           req.Pr,
		Cmd:          "record",
		Controls:     st.controls,
	})
	if err != nil {
		return MergeResult{}, refusalOrFailed(err)
	}
	return res, nil
}

// refusalOrFailed passes typed core refusals through and wraps anything
// else as a record-failed edge error.
func refusalOrFailed(err error) *core.RefusalError {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return ref
	}
	return recordFailed(err)
}

// svc carries one record run: the request, the decoded bundle store, and
// the warnings accumulated before the response or refusal.
type svc struct {
	req      *core.RecordRequest
	controls *controls.Snapshot
	store    *readcommon.Bundle
	warnings []string
}

// BundleBase reads the base commit a bundle was fetched at: the local
// source.json base_sha, else the PR record's baseRefOid, else its base_sha.
// store is the caller's decoded bundle so the run reads PR.json once.
func BundleBase(dir string, store *readcommon.Bundle) string {
	if base := readcommon.JSONStr(dir+"/source.json", "base_sha"); base != "" {
		return base
	}
	if store == nil {
		store = readcommon.OpenBundle(dir)
	}
	if base := store.StrField("baseRefOid"); base != "" {
		return base
	}
	return store.StrField("base_sha")
}

// runtime builds the injected runtime for the merge: the request's env,
// clock, and a warning-collecting progress observer.
func (st *svc) runtime() core.Runtime {
	var rt core.Runtime
	if st.req.Runtime != nil {
		rt = *st.req.Runtime
	}
	rt.Progress = warnCollector{inner: rt.Progress, warnings: &st.warnings}
	return rt
}

// warnCollector forwards progress observations to the adapter while
// accumulating every warning for the envelope.
type warnCollector struct {
	inner    core.Progress
	warnings *[]string
}

// Human forwards one human output line.
func (w warnCollector) Human(line string) {
	if w.inner != nil {
		w.inner.Human(line)
	}
}

// Warn records and forwards one warning.
func (w warnCollector) Warn(msg string) {
	*w.warnings = append(*w.warnings, msg)
	if w.inner != nil {
		w.inner.Warn(msg)
	}
}

// Diag forwards one raw diagnostic line.
func (w warnCollector) Diag(line string) {
	if w.inner != nil {
		w.inner.Diag(line)
	}
}

// withWarnings attaches the accumulated warnings to a refusal so the
// adapter's envelope stays byte-identical to a success run's warning list.
func (st *svc) withWarnings(ref *core.RefusalError) *core.RefusalError {
	ref.Warnings = st.warnings
	return ref
}

// recordFailed wraps a non-refusal failure for the edge to render.
func recordFailed(err error) *core.RefusalError {
	return &core.RefusalError{Code: codeRecordFailed, Message: fmt.Sprintf("record failed: %v", err)}
}

// finalize writes the record artifact, streams the summary, and builds the
// typed response.
func (st *svc) finalize(dir, prov, headFull, now string, res *MergeResult) (*core.RecordResponse, *core.RefusalError) {
	nOpen := CountStatus(res.Entries, statusOpen)
	nDeferred := CountStatus(res.Entries, "deferred")
	nAddressed := CountStatus(res.Entries, "addressed")
	diffSHA := sha256File(dir + "/full.diff")
	toolVersion, promptsHash, model := provenance.Triple(st.req.RootPath(), dir)
	record := &core.Record{
		SchemaVersion:   "1",
		Status:          prov,
		Head:            headFull,
		DiffSHA256:      diffSHA,
		RecordedAt:      now,
		Findings:        len(res.Entries),
		Open:            nOpen,
		Confidence:      intOrNil(res.Confidence),
		ConfidenceRaw:   res.Confidence,
		SecuritySummary: strOrNil(res.Security),
		ToolVersion:     toolVersion,
		PromptsHash:     promptsHash,
		Model:           modelOrNil(model),
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

// intOrNil reads an agent confidence as an int (nil when absent or exotic;
// the raw value still renders through ConfidenceRaw).
func intOrNil(v any) *int {
	if v == nil {
		return nil
	}
	if n, ok := jq.IntOf(v); ok {
		return &n
	}
	return nil
}

// strOrNil reads an agent security summary as a string (nil when absent).
func strOrNil(v any) *string {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok {
		return &s
	}
	return nil
}

// modelOrNil reads the provenance model (nil when unrecorded).
func modelOrNil(model string) *string {
	if model == "" {
		return nil
	}
	return &model
}

// typedItems builds the typed envelope summaries alongside the ordered
// items the wire renders verbatim.
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
		sig := jq.StrOf(sv)
		status := jq.StrOf(tv)
		items = append(items, core.FindingItem{
			Sig:      sig,
			Status:   status,
			Anchored: av,
			Locator:  readcommon.Locator(m),
		})
	}
	return items
}

// resolveProvider maps the bundle dir to its provider, refusing non-bundles.
func resolveProvider(dir string, store *readcommon.Bundle) (string, *core.RefusalError) {
	if strings.HasPrefix(dir, ".review/local-") || strings.HasPrefix(dir, ".review/corpus-") {
		// Corpus bundles are marked, local-shaped runs: record merges their
		// reviewed output exactly like a local bundle, while post/ready keep
		// refusing them (they have no PR target).
		return providerLocal, nil
	}
	switch prov := store.Provider(); prov {
	case "":
		return "", &core.RefusalError{Code: codeNotLocal, Message: msgNotLocal}
	case "github":
		return "", &core.RefusalError{Code: codeNotLocal, Message: msgNotGH}
	default:
		return prov, nil
	}
}

// checkBundleFiles refuses when the bundle's required files are absent.
func checkBundleFiles(dir, prov string) *core.RefusalError {
	files := []struct {
		path string
		code string
		hint string
	}{
		{dir + "/review.json", codeMissingReviewFile, "agent must write {body, approved, comments[]}"},
		{dir + "/full.diff", "missing-diff", "run fetch-local / fetch --provider first"},
	}
	for _, f := range files {
		if !exists(f.path) {
			return &core.RefusalError{Code: f.code, Message: fmt.Sprintf("missing %s (%s)", f.path, f.hint)}
		}
	}
	source := dir + prSuffix
	hint := "run fetch --provider first"
	if prov == providerLocal {
		source = dir + "/source.json"
		hint = "run fetch-local first"
	}
	if !exists(source) {
		return &core.RefusalError{Code: "missing-source", Message: fmt.Sprintf("missing %s (%s)", source, hint)}
	}
	return nil
}

// resolveHead reads the reviewed SHA, defaulting per provider.
func resolveHead(dir string, store *readcommon.Bundle, prov string) string {
	if prov == providerLocal {
		if headFull := readcommon.JSONStr(dir+"/source.json", "head_sha"); headFull != "" {
			return headFull
		}
		return "dirty"
	}
	if headFull := store.Head(); headFull != "" {
		return headFull
	}
	return "unknown"
}

// sha256File streams full.diff into sha256, like shasum -a 256.
func sha256File(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
