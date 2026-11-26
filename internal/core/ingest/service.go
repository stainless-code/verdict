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
	"github.com/stainless-code/verdict/internal/evidence"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
	"github.com/stainless-code/verdict/internal/sig"
)

const (
	// VerbIngest is the wire verb name.
	VerbIngest = "ingest"

	codeMissingSARIF  = "missing-sarif"
	codeBadFrom       = "bad-from"
	codeSARIFTooLarge = "sarif-too-large"
	codeIngestFailed  = "ingest-failed"
	codeMissingBundle = "missing-bundle"
	keyTool           = "tool"
	codeMissingDiff   = "missing-diff"
	codeCorruptStore  = "corrupt-store"
	codeCanceled      = "canceled"

	rigorSourceDefault = "default"
	sourceTool         = "tool"
	sourceExclusion    = "exclusion"
	eventWithheld      = "withheld"
	statusOpen         = "open"
	actorPrefix        = "sarif:"
	newlineSep         = "\n"
	kindInline         = "inline"
	sideRight          = "RIGHT"
	sideNew            = "NEW"
)

// Request carries the resolved ingest invocation: target bundle, the source
// format named by --from (SourceSARIF, or empty for the legacy --sarif form),
// the SARIF source (either a file path in SARIF or pre-read bytes in SARIFData
// when SARIFInline), and clock/runtime overrides.
type Request struct {
	core.ReadRequest
	From        string `json:"from"`
	SARIF       string `json:"sarif"`
	SARIFData   []byte `json:"-"`
	SARIFInline bool   `json:"-"`
}

// Skip is one SARIF result the ingest did not merge, with the reason and the
// locator it reported. Every result that cannot be anchored is published here
// instead of being dropped silently or stored at line 0.
type Skip struct {
	Reason string `enum:"no-location|invalid-line|path-escapes-repo|out-of-diff|ambiguous-path|snippet-mismatch" json:"reason"`
	Tool   string `json:"tool"`
	RuleID string `json:"rule_id"`
	Path   string `json:"path"`
	Line   int    `json:"line"`
}

// Data is the ingest envelope's data profile. Field order is the published
// property order Wire renders, so the schema and the generated types read the
// same sequence the store writes.
type Data struct { //nolint:govet // field order is the published property order
	Dir       string   `json:"dir"`
	Tools     []string `json:"tools"`
	Ingested  int      `json:"ingested"  min:"0"`
	Added     int      `json:"added"     min:"0"`
	Updated   int      `json:"updated"   min:"0"`
	Unchanged int      `json:"unchanged" min:"0"`
	Anchored  int      `json:"anchored"  min:"0"`
	Withheld  int      `json:"withheld"  min:"0"`
	Skipped   []Skip   `json:"skipped"`
	Open      int      `json:"open"      min:"0"`
}

// Wire renders Data in published property order.
func (d *Data) Wire() *jq.Obj {
	tools := make([]any, 0, len(d.Tools))
	for _, t := range d.Tools {
		tools = append(tools, t)
	}
	skipped := make([]any, 0, len(d.Skipped))
	for _, s := range d.Skipped {
		skipped = append(skipped, jq.NewObj(
			"reason", s.Reason,
			"tool", s.Tool,
			"rule_id", s.RuleID,
			"path", s.Path,
			"line", s.Line,
		))
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
		"skipped", skipped,
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
	diff         *evidence.DiffIndex
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
		return nil, &core.RefusalError{Code: codeIngestFailed, Message: core.Msgf("writing %s/findings.json: %v", st.dir, err)}
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
	if ref := validateSource(req.From); ref != nil {
		return nil, nil, ref
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

// validateSource accepts the one source format this build reads. An empty value
// is the legacy `--sarif <file|->` form of the same thing.
func validateSource(from string) *core.RefusalError {
	switch strings.ToLower(strings.TrimSpace(from)) {
	case "", SourceSARIF:
		return nil
	default:
		return &core.RefusalError{
			Code:    codeBadFrom,
			Message: core.Msgf("unknown --from %q for ingest (want %s)", from, SourceSARIF),
		}
	}
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
		return nil, &core.RefusalError{Code: codeMissingSARIF, Message: "--from sarif <file|-> is required"}
	}
	// ReadRegularCapped opens via O_NONBLOCK without O_NOFOLLOW, so check Lstat first.
	if linfo, err := os.Lstat(path); err == nil && linfo.Mode()&os.ModeSymlink != 0 {
		return nil, &core.RefusalError{
			Code:    codeBadSARIF,
			Message: core.Msgf("SARIF path %s must be a regular file (not a symlink or directory)", path),
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
			Message: core.Msgf("SARIF input exceeds maximum size of %d bytes", MaxSARIFBytes),
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
			Message: core.Msgf("SARIF file %s exceeds maximum size of %d bytes", path, MaxSARIFBytes),
		}
	case errors.Is(err, readcommon.ErrNotRegular):
		return &core.RefusalError{
			Code:    codeBadSARIF,
			Message: core.Msgf("SARIF path %s must be a regular file (not a symlink or directory)", path),
		}
	case errors.Is(err, fs.ErrNotExist):
		return &core.RefusalError{
			Code:    codeMissingSARIF,
			Message: core.Msgf("missing SARIF file %s", path),
		}
	default:
		return &core.RefusalError{
			Code:    codeIngestFailed,
			Message: core.Msgf("reading SARIF file %s: %v", path, err),
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
	diff, ref := loadDiffIndex(dir)
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
		diff:         diff,
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
			Message: core.Msgf("missing bundle directory %s (run fetch first)", dir),
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "full.diff")); err != nil {
		return &core.RefusalError{
			Code:    codeMissingDiff,
			Message: core.Msgf("missing %s/full.diff (run fetch first)", dir),
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
		return &core.RefusalError{Code: codeIngestFailed, Message: core.Msgf("%v", err)}
	}
	return nil
}

func loadHunks(dir string) ([]hunks.Hunk, *core.RefusalError) {
	path := filepath.Join(dir, "hunks.tsv")
	raw, err := readcommon.ReadRegularCapped(path, jq.MaxFindingsBytes)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, &core.RefusalError{Code: codeIngestFailed, Message: core.Msgf("reading %s: %v", path, err)}
	}
	if len(raw) == 0 {
		diffRaw, err := readcommon.ReadRegularCapped(filepath.Join(dir, "full.diff"), jq.MaxFindingsBytes)
		if err != nil {
			return nil, &core.RefusalError{Code: codeIngestFailed, Message: core.Msgf("reading %s/full.diff: %v", dir, err)}
		}
		return hunks.Parse(string(diffRaw)), nil
	}
	return hunks.ParseTSV(string(raw)), nil
}

// loadDiffIndex indexes the bundle's pinned full.diff so an ingested line can
// be proven against the bytes the review is anchored to, never against the
// scanner's own snippet alone.
func loadDiffIndex(dir string) (*evidence.DiffIndex, *core.RefusalError) {
	raw, err := readcommon.ReadRegularCapped(filepath.Join(dir, "full.diff"), jq.MaxFindingsBytes)
	if err != nil {
		return nil, &core.RefusalError{Code: codeIngestFailed, Message: core.Msgf("reading %s/full.diff: %v", dir, err)}
	}
	return evidence.IndexDiff(raw), nil
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
		return nil, true, &core.RefusalError{Code: codeCorruptStore, Message: core.Msgf("%v", err)}
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
	tools := parsed.Tools
	if tools == nil {
		tools = []string{}
	}
	data := &Data{Dir: st.dir, Tools: tools, Skipped: []Skip{}}
	seenSkip := make(map[string]bool)
	seenSig := make(map[string]bool)
	seenFP := make(map[string]bool)
	for i := range parsed.Results {
		res := &parsed.Results[i]
		cand, reason := st.gradeResult(res)
		if reason != "" {
			// A skipped result never claims its identity, so a later duplicate
			// the pinned diff can anchor still lands.
			addSkip(data, res, reason, seenSkip)
			continue
		}
		// Two anchorable results that resolve to the same stable finding
		// signature, or that the scanner gives the same identity, are one
		// finding: the first wins, so an input that repeats itself never
		// doubles the store.
		if seenSig[cand.Entry.Sig] || claimsIdentity(seenFP, res) {
			continue
		}
		seenSig[cand.Entry.Sig] = true
		st.mergeOne(res, cand, data)
	}
	data.Ingested = data.Added + data.Updated + data.Unchanged
	data.Open = countOpen(st.entries)
	return data
}

// gradeResult turns one parsed result into a candidate, or names the typed
// reason it cannot become one. Parse-stage skips (no location, an unusable
// line, a URI outside the repository) pass through unchanged.
func (st *ingestState) gradeResult(res *parsedResult) (candidate, string) {
	if res.SkipReason != "" {
		return candidate{}, res.SkipReason
	}
	return st.buildCandidate(res)
}

// addSkip records one unmergeable result, deduplicated so the same scanner
// repeating itself does not repeat its skip rows.
func addSkip(data *Data, res *parsedResult, reason string, seen map[string]bool) {
	skip := Skip{
		Reason: reason,
		Tool:   res.Tool,
		RuleID: res.RuleID,
		Path:   res.Path,
		Line:   max(res.StartLine, 0),
	}
	key := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d", skip.Reason, skip.Tool, skip.RuleID, skip.Path, skip.Line)
	if seen[key] {
		return
	}
	seen[key] = true
	data.Skipped = append(data.Skipped, skip)
}

func (st *ingestState) mergeOne(res *parsedResult, cand candidate, data *Data) {
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
	// Wire renders the store's fixed key order and leaves quote to the writer:
	// the verified diff line rides beside the finding it proves.
	if cand.Entry.Quote != "" {
		wire.Set("quote", cand.Entry.Quote)
	}
	newIdx := len(st.entries)
	st.entries = append(st.entries, wire)
	st.bySig[cand.Entry.Sig] = newIdx
	if res.Tool != "" && res.Fingerprint != "" {
		st.byToolFP[toolFPKey(res.Tool, res.Fingerprint)] = newIdx
	}
}

// claimsIdentity records a graded result's (tool, fingerprint) identity,
// reporting whether an anchorable result already claimed it. Only anchorable
// results reach this: a skipped duplicate is recorded in skipped[] and leaves
// the identity free for a later copy the diff can place.
func claimsIdentity(seen map[string]bool, res *parsedResult) bool {
	if res.Tool == "" || res.Fingerprint == "" {
		return false
	}
	key := toolFPKey(res.Tool, res.Fingerprint)
	if seen[key] {
		return true
	}
	seen[key] = true
	return false
}

type candidate struct {
	Entry    *core.FindingEntry
	Withheld *core.Withheld
	Anchored bool
}

// buildCandidate grades one parsed result against the bundle's pinned diff. A
// result that cannot be anchored returns the typed skip reason instead of a
// candidate: the store never holds a finding at line 0 or at a line the diff
// does not carry, and the recorded quote is the changed line the diff proves,
// not the scanner's unchecked text. Parse-stage skips (no location, no usable
// line, a URI outside the repository) were already returned by gradeResult, so
// an excluded path only reaches this with a usable line. The one unanchored
// entry ingest writes is a withheld one: a path the trusted exclusion control
// suppressed from the diff is recorded, never posted, and never claimed as
// proven.
func (st *ingestState) buildCandidate(r *parsedResult) (candidate, string) {
	withheld, history := st.buildExclusionWithheld(r, st.reportedHistory(r))
	quote, reason := st.anchorQuote(r)
	anchored := reason == ""
	if !anchored && withheld == nil {
		return candidate{}, reason
	}
	entry := st.newFinding(r, quote, anchored, withheld, history)
	return candidate{Entry: entry, Withheld: withheld, Anchored: anchored}, ""
}

// reportedHistory is the one reported event every ingested finding starts from.
func (st *ingestState) reportedHistory(r *parsedResult) []core.HistoryEvent {
	return []core.HistoryEvent{{
		At:    st.now,
		Event: "reported",
		Actor: actorPrefix + r.Tool,
		Note:  r.RuleID,
		SHA:   st.headShort,
	}}
}

// newFinding renders the shared finding shape: an anchored result grades
// pointed on the verified diff line, while a withheld-but-unanchored one stays
// said. Both carry the line the scanner reported, because a result without a
// usable line was skipped as invalid-line before this runs — a file-level
// result (startLine: 0) on an excluded path is therefore a skip, never a
// withheld finding.
func (st *ingestState) newFinding(
	r *parsedResult, quote string, anchored bool, withheld *core.Withheld, history []core.HistoryEvent,
) *core.FindingEntry {
	rigor := core.RungSaid
	if anchored {
		rigor = core.RungPointed
	}
	kb := []string{}
	return &core.FindingEntry{
		Sig:         sig.Of(r.Path + "\n\n" + r.Body),
		SigVersion:  "1",
		Kind:        kindInline,
		Path:        r.Path,
		Line:        r.StartLine,
		Side:        sideRight,
		Anchored:    anchored,
		Body:        r.Body,
		KB:          &kb,
		InBounds:    anchored,
		Rigor:       rigor,
		RigorSource: rigorSourceDefault,
		Severity:    r.Severity,
		Quote:       quote,
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
}

// anchorQuote proves a result's line against the bundle's pinned hunks and diff
// and returns the exact changed-line text as the finding's quote. A line the
// changed hunks do not carry is out-of-diff; a reported snippet that disagrees
// with the diff line is a stale location, so it is skipped rather than
// re-pointed at text the scanner never saw.
func (st *ingestState) anchorQuote(r *parsedResult) (string, string) {
	// The path lookup decides first: a file the diff names twice is ambiguous
	// wherever the reported line sits, and saying so beats reporting out-of-diff.
	df, why := st.diff.File(r.Path)
	if why != "" {
		return "", skipForIndexReason(why)
	}
	if !hunks.Contains(st.hunks, r.Path, sideNew, r.StartLine, r.EndLine) {
		return "", SkipOutOfDiff
	}
	quote, ok := df.NewLines[r.StartLine]
	if !ok || !rangeIndexed(df.NewLines, r.StartLine+1, r.EndLine) {
		return "", SkipOutOfDiff
	}
	if r.Snippet != "" && !snippetMatches(r.Snippet, quote) {
		return "", SkipSnippetMismatch
	}
	return quote, ""
}

// skipForIndexReason names the skip a refused diff lookup earns: the diff
// naming one path twice is ambiguous (we will not guess), anything else is
// simply outside the diff.
func skipForIndexReason(why string) string {
	if why == evidence.ReasonAmbiguousPath {
		return SkipAmbiguousPath
	}
	return SkipOutOfDiff
}

// rangeIndexed reports whether every line in [from, to] has diff text.
func rangeIndexed(lines map[int]string, from, to int) bool {
	for line := from; line <= to; line++ {
		if _, ok := lines[line]; !ok {
			return false
		}
	}
	return true
}

// snippetMatches compares a scanner's reported snippet with the diff line: the
// first line of each, trimmed, because a tool may pad, indent, or report a
// multi-line region while the anchor itself is one line.
func snippetMatches(snippet, diffLine string) bool {
	first, _, _ := strings.Cut(snippet, newlineSep)
	return strings.TrimSpace(first) == strings.TrimSpace(diffLine)
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
	oldTool := jq.StrOf(readcommon.FieldOf(pm, keyTool))
	oldFP := jq.StrOf(readcommon.FieldOf(pm, "fingerprint"))
	e := cand.Entry
	updated.Set("sig", e.Sig)
	updated.Set("kind", e.Kind)
	setOrDelete(updated, "path", e.Path)
	setOrDelete(updated, "heading", e.Heading)
	setOrDelete(updated, "line", e.Line)
	setOrDelete(updated, "side", e.Side)
	updated.Set("anchored", e.Anchored)
	updated.Set("body", e.Body)
	updated.Set("inBounds", e.InBounds)
	updated.Set("rigor", e.Rigor)
	updated.Set("rigor_source", e.RigorSource)
	setOrDelete(updated, "quote", e.Quote)
	updated.Set("severity", e.Severity)
	updated.Set("source", sourceTool)
	setOrDelete(updated, keyTool, e.Tool)
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
	st.reindexToolFP(idx, oldTool, oldFP, e.Tool, e.Fingerprint)
	st.entries[idx] = updated
	return true
}

// reindexToolFP keeps the (tool, fingerprint) index pointing at the entry after
// an update that relabeled it, so a later result carrying the old identity is
// never folded into the wrong finding.
func (st *ingestState) reindexToolFP(idx int, oldTool, oldFP, tool, fp string) {
	if oldTool != "" && oldFP != "" && (oldTool != tool || oldFP != fp) {
		delete(st.byToolFP, toolFPKey(oldTool, oldFP))
	}
	if tool != "" && fp != "" {
		st.byToolFP[toolFPKey(tool, fp)] = idx
	}
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
	return fmt.Sprintf(
		"ingested %d result(s) from %s into %s (%d added, %d updated, %d unchanged; "+
			"%d anchored, %d withheld, %d skipped; %d open)",
		d.Ingested, tools, dir, d.Added, d.Updated, d.Unchanged, d.Anchored, d.Withheld, len(d.Skipped), d.Open)
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
