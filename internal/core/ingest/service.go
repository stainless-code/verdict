package ingestcore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/sig"
)

const (
	// VerbIngest is the wire verb name.
	VerbIngest = "ingest"

	codeMissingSARIF  = "missing-sarif"
	codeSARIFTooLarge = "sarif-too-large"
	codeIngestFailed  = "ingest-failed"
	codeMissingBundle = "missing-bundle"
	codeMissingDiff   = "missing-diff"
	codeCorruptStore  = "corrupt-store"
	codeCanceled      = "canceled"

	rigorSaid          = "said"
	rigorSourceDefault = "default"
	sourceTool         = "tool"
	sourceExclusion    = "exclusion"
	eventWithheld      = "withheld"
	statusOpen         = "open"
	actorPrefix        = "sarif:"
	newlineSep         = "\n"
)

// Request carries the resolved ingest invocation: target bundle, SARIF source
// (either file path in SARIF or pre-read bytes in SARIFData when SARIFInline),
// and clock/runtime overrides.
type Request struct {
	core.ReadRequest
	SARIF       string `json:"sarif"`
	SARIFData   []byte `json:"-"`
	SARIFInline bool   `json:"-"`
}

// Data is the ingest envelope's data profile.
type Data struct {
	Dir       string   `json:"dir"`
	Tools     []string `json:"tools"`
	Ingested  int      `json:"ingested"  min:"0"`
	Added     int      `json:"added"     min:"0"`
	Updated   int      `json:"updated"   min:"0"`
	Unchanged int      `json:"unchanged" min:"0"`
	Anchored  int      `json:"anchored"  min:"0"`
	Withheld  int      `json:"withheld"  min:"0"`
	Open      int      `json:"open"      min:"0"`
}

// Wire renders Data in published property order.
func (d *Data) Wire() *jq.Obj {
	tools := make([]any, 0, len(d.Tools))
	for _, t := range d.Tools {
		tools = append(tools, t)
	}
	return jq.NewObj(
		"dir", d.Dir,
		"tools", tools,
		"ingested", d.Ingested,
		"added", d.Added,
		"updated", d.Updated,
		"unchanged", d.Unchanged,
		"anchored", d.Anchored,
		"withheld", d.Withheld,
		"open", d.Open,
	)
}

// Response carries the completed ingest result.
type Response struct {
	At       string
	Next     string
	Human    string
	Data     *Data
	Warnings []string
}

type ingestState struct {
	dir          string
	exclusionDir string
	now          string
	headShort    string
	hunks        []hunks.Hunk
	entries      []any
	bySig        map[string]int
	byToolFP     map[string]int
	warnings     []string
	existed      bool
}

// Run validates and ingests a SARIF 2.1.0 document into the bundle's
// findings.json store, keeping idempotency across re-runs, preserving model
// entries, and resolving anchors and path exclusions uniformly with record.
func Run(ctx context.Context, req *Request) (*Response, *core.RefusalError) {
	st, parsed, ref := prepareIngest(ctx, req)
	if ref != nil {
		return nil, ref
	}
	if ctx != nil && ctx.Err() != nil {
		return nil, &core.RefusalError{Code: codeCanceled, Message: "ingest canceled"}
	}
	data := st.mergeResults(parsed)
	if err := persistStore(st, data); err != nil {
		return nil, &core.RefusalError{Code: codeIngestFailed, Message: fmt.Sprintf("writing %s/findings.json: %v", st.dir, err)}
	}
	return &Response{
		At:       st.now,
		Next:     renderNext(req.Pr, data.Open),
		Human:    renderHuman(st.dir, data),
		Data:     data,
		Warnings: st.warnings,
	}, nil
}

func prepareIngest(ctx context.Context, req *Request) (*ingestState, *parseOutput, *core.RefusalError) {
	if ctx != nil && ctx.Err() != nil {
		return nil, nil, &core.RefusalError{Code: codeCanceled, Message: "ingest canceled"}
	}
	dir, ref := readcommon.ResolveWritableBundleDir(req.RepoRootPath(), req.Pr)
	if ref != nil {
		return nil, nil, ref
	}
	raw, ref := readSARIFInput(req)
	if ref != nil {
		return nil, nil, ref
	}
	parsed, ref := parseSARIF(raw)
	if ref != nil {
		return nil, nil, ref
	}
	st, ref := initState(req, dir, parsed.Warnings)
	if ref != nil {
		return nil, nil, ref
	}
	return st, parsed, nil
}

func persistStore(st *ingestState, data *Data) error {
	if data.Added == 0 && data.Updated == 0 && st.existed {
		return nil
	}
	payload := jq.PrettyOrdered(st.entries)
	if !strings.HasSuffix(payload, newlineSep) {
		payload += newlineSep
	}
	return atomic.WriteFile(filepath.Join(st.dir, "findings.json"), []byte(payload))
}

func readSARIFInput(req *Request) ([]byte, *core.RefusalError) {
	if req.SARIFInline {
		return validateInlineSARIF(req.SARIFData)
	}
	path := strings.TrimSpace(req.SARIF)
	if path == "" {
		return nil, &core.RefusalError{Code: codeMissingSARIF, Message: "--sarif <file|-> is required"}
	}
	// ReadRegularCapped opens via O_NONBLOCK without O_NOFOLLOW, so check Lstat first.
	if linfo, err := os.Lstat(path); err == nil && linfo.Mode()&os.ModeSymlink != 0 {
		return nil, &core.RefusalError{
			Code:    codeBadSARIF,
			Message: fmt.Sprintf("SARIF path %s must be a regular file (not a symlink or directory)", path),
		}
	}
	raw, err := readcommon.ReadRegularCapped(path, MaxSARIFBytes)
	if err != nil {
		return nil, classifySARIFReadErr(path, err)
	}
	return raw, nil
}

func validateInlineSARIF(raw []byte) ([]byte, *core.RefusalError) {
	if len(raw) > MaxSARIFBytes {
		return nil, &core.RefusalError{
			Code:    codeSARIFTooLarge,
			Message: fmt.Sprintf("SARIF input exceeds maximum size of %d bytes", MaxSARIFBytes),
		}
	}
	if len(raw) == 0 {
		return nil, &core.RefusalError{Code: codeMissingSARIF, Message: "SARIF input is empty"}
	}
	return raw, nil
}

func classifySARIFReadErr(path string, err error) *core.RefusalError {
	switch {
	case errors.Is(err, jq.ErrTooLarge):
		return &core.RefusalError{
			Code:    codeSARIFTooLarge,
			Message: fmt.Sprintf("SARIF file %s exceeds maximum size of %d bytes", path, MaxSARIFBytes),
		}
	case errors.Is(err, readcommon.ErrNotRegular):
		return &core.RefusalError{
			Code:    codeBadSARIF,
			Message: fmt.Sprintf("SARIF path %s must be a regular file (not a symlink or directory)", path),
		}
	case errors.Is(err, fs.ErrNotExist):
		return &core.RefusalError{
			Code:    codeMissingSARIF,
			Message: "missing SARIF file " + path,
		}
	default:
		return &core.RefusalError{
			Code:    codeIngestFailed,
			Message: fmt.Sprintf("reading SARIF file %s: %v", path, err),
		}
	}
}

func initState(req *Request, dir string, parseWarnings []string) (*ingestState, *core.RefusalError) {
	if ref := checkBundleFiles(req, dir); ref != nil {
		return nil, ref
	}
	hs, ref := loadHunks(dir)
	if ref != nil {
		return nil, ref
	}
	entries, existed, ref := loadStore(dir)
	if ref != nil {
		return nil, ref
	}
	var rt core.Runtime
	if req.Runtime != nil {
		rt = *req.Runtime
	}
	// ResolveWritableBundleDir already refused symlinked .verdict/state/<pr> dirs;
	// resolve OS ancestors (e.g. macOS /var -> /private/var) so ReadConfined works.
	exclusionDir := dir
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		exclusionDir = resolved
	}
	st := &ingestState{
		dir:          dir,
		exclusionDir: exclusionDir,
		now:          rt.TimeUTC(),
		headShort:    resolveHeadShort(dir),
		hunks:        hs,
		entries:      entries,
		warnings:     append([]string{}, parseWarnings...),
		existed:      existed,
	}
	st.buildIndexes()
	return st, nil
}

func checkBundleFiles(req *Request, dir string) *core.RefusalError {
	if _, err := os.Stat(dir); err != nil {
		return &core.RefusalError{
			Code:    codeMissingBundle,
			Message: fmt.Sprintf("missing bundle directory %s (run fetch first)", dir),
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "full.diff")); err != nil {
		return &core.RefusalError{
			Code:    codeMissingDiff,
			Message: fmt.Sprintf("missing %s/full.diff (run fetch first)", dir),
		}
	}
	var rt core.Runtime
	if req.Runtime != nil {
		rt = *req.Runtime
	}
	if err := recordcore.EnsureHunksSnapshot(rt, dir); err != nil {
		if ref, ok := errors.AsType[*core.RefusalError](err); ok {
			return ref
		}
		return &core.RefusalError{Code: codeIngestFailed, Message: err.Error()}
	}
	return nil
}

func loadHunks(dir string) ([]hunks.Hunk, *core.RefusalError) {
	path := filepath.Join(dir, "hunks.tsv")
	raw, err := readcommon.ReadRegularCapped(path, jq.MaxFindingsBytes)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, &core.RefusalError{Code: codeIngestFailed, Message: fmt.Sprintf("reading %s: %v", path, err)}
	}
	if len(raw) == 0 {
		diffRaw, err := readcommon.ReadRegularCapped(filepath.Join(dir, "full.diff"), jq.MaxFindingsBytes)
		if err != nil {
			return nil, &core.RefusalError{Code: codeIngestFailed, Message: fmt.Sprintf("reading %s/full.diff: %v", dir, err)}
		}
		return hunks.Parse(string(diffRaw)), nil
	}
	return hunks.ParseTSV(string(raw)), nil
}

func loadStore(dir string) (entries []any, existed bool, ref *core.RefusalError) {
	path := filepath.Join(dir, "findings.json")
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return []any{}, false, nil
	}
	loaded, err := readcommon.LoadFindings(dir)
	if err != nil {
		if rerr, ok := errors.AsType[*core.RefusalError](err); ok {
			return nil, true, rerr
		}
		return nil, true, &core.RefusalError{Code: codeCorruptStore, Message: err.Error()}
	}
	backfillStoreEntries(loaded)
	return loaded, true, nil
}

func backfillStoreEntries(entries []any) {
	for _, item := range entries {
		if m, ok := item.(*jq.Obj); ok && m != nil {
			recordcore.BackfillEntry(m)
		}
	}
}

func resolveHeadShort(dir string) string {
	if sha := readcommon.PrField(dir, "head_sha"); sha != "" {
		return readcommon.ShortSHA(sha)
	}
	if sha := readSourceHeadSHA(dir); sha != "" {
		return readcommon.ShortSHA(sha)
	}
	return "unknown"
}

func readSourceHeadSHA(dir string) string {
	raw, err := jq.CappedFile(filepath.Join(dir, "source.json"), jq.MaxPRBytes)
	if err != nil {
		return ""
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return ""
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return ""
	}
	return jq.StrOf(m["head_sha"])
}

func (st *ingestState) buildIndexes() {
	st.bySig = make(map[string]int, len(st.entries))
	st.byToolFP = make(map[string]int, len(st.entries))
	for i, item := range st.entries {
		m, ok := item.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		st.indexSig(i, m)
		st.indexToolFP(i, m)
	}
}

func (st *ingestState) indexSig(idx int, m *jq.Obj) {
	sv, has := m.Get("sig")
	if !has {
		return
	}
	s := jq.StrOf(sv)
	if s == "" {
		return
	}
	if _, exists := st.bySig[s]; !exists {
		st.bySig[s] = idx
	}
}

func (st *ingestState) indexToolFP(idx int, m *jq.Obj) {
	tv, _ := m.Get("tool")
	fv, _ := m.Get("fingerprint")
	tool := jq.StrOf(tv)
	fp := jq.StrOf(fv)
	if tool == "" || fp == "" {
		return
	}
	key := toolFPKey(tool, fp)
	if _, exists := st.byToolFP[key]; !exists {
		st.byToolFP[key] = idx
	}
}

func toolFPKey(tool, fp string) string {
	return tool + "\x00" + fp
}

func (st *ingestState) mergeResults(parsed *parseOutput) *Data {
	deduped := deduplicateBatch(parsed.Results)
	tools := parsed.Tools
	if tools == nil {
		tools = []string{}
	}
	data := &Data{Dir: st.dir, Tools: tools, Ingested: len(deduped)}
	for i := range deduped {
		st.mergeOne(&deduped[i], data)
	}
	data.Open = countOpen(st.entries)
	return data
}

func (st *ingestState) mergeOne(res *parsedResult, data *Data) {
	cand := st.buildCandidate(res)
	if cand.Anchored {
		data.Anchored++
	}
	if cand.Withheld != nil {
		data.Withheld++
	}
	idx, found := st.lookupExisting(res.VerdictSig, cand.Entry.Sig, res.Tool, res.Fingerprint)
	if !found {
		st.appendCandidate(res, cand)
		data.Added++
		return
	}
	if st.updateExisting(idx, cand) {
		data.Updated++
	} else {
		data.Unchanged++
	}
}

func (st *ingestState) appendCandidate(res *parsedResult, cand candidate) {
	wire := cand.Entry.Wire()
	newIdx := len(st.entries)
	st.entries = append(st.entries, wire)
	st.bySig[cand.Entry.Sig] = newIdx
	if res.Tool != "" && res.Fingerprint != "" {
		st.byToolFP[toolFPKey(res.Tool, res.Fingerprint)] = newIdx
	}
}

// deduplicateBatch keeps the first occurrence per (tool, fingerprint) within a
// single SARIF document so intra-file duplicates stay idempotent.
func deduplicateBatch(results []parsedResult) []parsedResult {
	seen := make(map[string]bool, len(results))
	out := make([]parsedResult, 0, len(results))
	for i := range results {
		key := toolFPKey(results[i].Tool, results[i].Fingerprint)
		if results[i].Fingerprint != "" {
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		out = append(out, results[i])
	}
	return out
}

type candidate struct {
	Entry    *core.FindingEntry
	Withheld *core.Withheld
	Anchored bool
}

func (st *ingestState) buildCandidate(r *parsedResult) candidate {
	var (
		kind        string
		path        any
		heading     any
		line        any
		side        any
		entryAnchor any
		anchored    bool
		inBounds    bool
		sigKey      string
	)
	if r.Path != "" {
		kind = "inline"
		path = r.Path
		side = "RIGHT"
		if r.StartLine > 0 {
			line = r.StartLine
			anchored = hunks.Contains(st.hunks, r.Path, "NEW", r.StartLine, r.EndLine)
			inBounds = anchored
		}
		entryAnchor = anchored
		sigKey = r.Path + "\n\n" + r.Body
	} else {
		kind = "section"
		h := sectionHeading(r.Tool, r.RuleID)
		heading = h
		entryAnchor = "body"
		inBounds = true
		sigKey = newlineSep + h + newlineSep + r.Body
	}
	entrySig := sig.Of(sigKey)
	kb := []string{}
	history := []core.HistoryEvent{{
		At:    st.now,
		Event: "reported",
		Actor: actorPrefix + r.Tool,
		Note:  r.RuleID,
		SHA:   st.headShort,
	}}
	withheld, history := st.buildExclusionWithheld(r, history)
	entry := &core.FindingEntry{
		Sig:         entrySig,
		SigVersion:  "1",
		Kind:        kind,
		Path:        path,
		Heading:     heading,
		Line:        line,
		Side:        side,
		Anchored:    entryAnchor,
		Body:        r.Body,
		KB:          &kb,
		InBounds:    inBounds,
		Rigor:       rigorSaid,
		RigorSource: rigorSourceDefault,
		Severity:    r.Severity,
		Withheld:    withheld,
		Status:      statusOpen,
		FirstSeen:   st.headShort,
		LastSeen:    st.headShort,
		Source:      sourceTool,
		Tool:        r.Tool,
		RuleID:      r.RuleID,
		Fingerprint: r.Fingerprint,
		History:     history,
	}
	return candidate{Entry: entry, Withheld: withheld, Anchored: anchored}
}

func (st *ingestState) buildExclusionWithheld(r *parsedResult, history []core.HistoryEvent) (*core.Withheld, []core.HistoryEvent) {
	if r.Path == "" {
		return nil, history
	}
	ruleID, reason, excluded := recordcore.ExclusionForPath(st.exclusionDir, r.Path)
	if !excluded {
		return nil, history
	}
	withheld := &core.Withheld{
		Source:      sourceExclusion,
		Reason:      reason,
		ExclusionID: ruleID,
		At:          st.now,
	}
	history = append(history, core.HistoryEvent{
		At:    st.now,
		Event: eventWithheld,
		Actor: actorPrefix + r.Tool,
		Note:  reason,
	})
	return withheld, history
}

func sectionHeading(tool, ruleID string) string {
	if ruleID != "" {
		return "### " + ruleID + " (" + tool + ")"
	}
	return "### " + tool
}

func (st *ingestState) lookupExisting(verdictSig, entrySig, tool, fp string) (int, bool) {
	if idx, ok := st.bySig[verdictSig]; verdictSig != "" && ok {
		return idx, true
	}
	if idx, ok := st.bySig[entrySig]; entrySig != "" && ok {
		return idx, true
	}
	if idx, ok := st.byToolFP[toolFPKey(tool, fp)]; tool != "" && fp != "" && ok {
		return idx, true
	}
	return 0, false
}

func (st *ingestState) updateExisting(idx int, cand candidate) bool {
	pm, ok := st.entries[idx].(*jq.Obj)
	if !ok || pm == nil {
		return false
	}
	// Preserve model-reviewed entries unmodified so round-tripping exported
	// SARIF or colliding on sig never downgrades model provenance or rigor.
	if readcommon.SourceOf(pm) != sourceTool {
		return false
	}
	updated := pm.Clone()
	oldSig := jq.StrOf(readcommon.FieldOf(pm, "sig"))
	e := cand.Entry
	updated.Set("sig", e.Sig)
	updated.Set("kind", e.Kind)
	setOrDelete(updated, "path", e.Path)
	setOrDelete(updated, "heading", e.Heading)
	setOrDelete(updated, "line", e.Line)
	setOrDelete(updated, "side", e.Side)
	updated.Set("anchored", e.Anchored)
	updated.Set("body", e.Body)
	if e.Kind == "inline" {
		updated.Set("inBounds", e.InBounds)
		updated.Set("rigor", rigorSaid)
	} else {
		updated.Delete("inBounds")
		updated.Delete("rigor")
	}
	updated.Set("rigor_source", rigorSourceDefault)
	updated.Set("severity", e.Severity)
	updated.Set("source", sourceTool)
	setOrDelete(updated, "tool", e.Tool)
	setOrDelete(updated, "rule_id", e.RuleID)
	setOrDelete(updated, "fingerprint", e.Fingerprint)
	syncExclusionWithheld(updated, cand.Withheld, st.now, actorPrefix+e.Tool)
	updated.Set("lastSeen", st.headShort)

	if jq.Compact(updated) == jq.Compact(pm) {
		return false
	}
	if oldSig != e.Sig {
		delete(st.bySig, oldSig)
		st.bySig[e.Sig] = idx
	}
	st.entries[idx] = updated
	return true
}

func syncExclusionWithheld(m *jq.Obj, desired *core.Withheld, now, actor string) {
	prior, priorSrc := priorWithheld(m)
	if desired == nil {
		if priorSrc == sourceExclusion {
			m.Delete(eventWithheld)
		}
		return
	}
	at := resolveWithheldTimestamp(prior, priorSrc, desired, now)
	if priorSrc == desired.Source && matchesExclusion(prior, desired) {
		return
	}
	w := jq.NewObj("source", desired.Source, "reason", desired.Reason, "exclusion_id", desired.ExclusionID, "at", at)
	appendObjHistory(m, jq.NewObj("at", now, "event", eventWithheld, "actor", actor, "note", desired.Reason))
	m.Set(eventWithheld, w)
}

func matchesExclusion(prior *jq.Obj, desired *core.Withheld) bool {
	return jq.StrOf(readcommon.FieldOf(prior, "exclusion_id")) == desired.ExclusionID &&
		jq.StrOf(readcommon.FieldOf(prior, "reason")) == desired.Reason
}

func priorWithheld(m *jq.Obj) (*jq.Obj, string) {
	raw, _ := m.Get(eventWithheld)
	prior, ok := raw.(*jq.Obj)
	if !ok || prior == nil {
		return nil, ""
	}
	return prior, jq.StrOf(readcommon.FieldOf(prior, "source"))
}

func resolveWithheldTimestamp(prior *jq.Obj, priorSrc string, desired *core.Withheld, now string) string {
	if prior == nil || priorSrc != sourceExclusion || !matchesExclusion(prior, desired) {
		return now
	}
	if priorAt := jq.StrOf(readcommon.FieldOf(prior, "at")); priorAt != "" {
		return priorAt
	}
	return now
}

func appendObjHistory(m, ev *jq.Obj) {
	raw, _ := m.Get("history")
	arr, ok := raw.([]any)
	if !ok {
		arr = nil
	}
	m.Set("history", append(slices.Clone(arr), ev))
}

func setOrDelete(m *jq.Obj, key string, val any) {
	switch v := val.(type) {
	case nil:
		m.Delete(key)
	case string:
		if v == "" {
			m.Delete(key)
		} else {
			m.Set(key, v)
		}
	default:
		m.Set(key, v)
	}
}

func countOpen(entries []any) int {
	n := 0
	for _, item := range entries {
		m, ok := item.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		if v, _ := m.Get("status"); jq.StrOf(v) == statusOpen {
			n++
		}
	}
	return n
}

func renderHuman(dir string, d *Data) string {
	tools := "none"
	if len(d.Tools) > 0 {
		tools = strings.Join(d.Tools, ",")
	}
	return fmt.Sprintf("ingested %d result(s) from %s into %s (%d added, %d updated, %d unchanged; %d anchored, %d withheld; %d open)",
		d.Ingested, tools, dir, d.Added, d.Updated, d.Unchanged, d.Anchored, d.Withheld, d.Open)
}

func renderNext(pr string, open int) string {
	target := strings.TrimSpace(pr)
	if target == "" {
		target = "<pr>"
	}
	if open == 0 {
		return fmt.Sprintf("Run `verdict report <repo> %s` to inspect the clean store.", target)
	}
	return fmt.Sprintf("Run `verdict report <repo> %s` to inspect all findings (including ingested tool findings).", target)
}
