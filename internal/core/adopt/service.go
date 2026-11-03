// Package adoptcore is the adopt verb's domain service: carry a local
// bundle's findings into a fresh PR bundle for posting. The findings copy
// verbatim (stamped with the handoff), anchors re-resolve against the PR
// hunks (drifted ones clear), and the store write goes through the shared
// atomic path. internal/adopt owns argv parsing, progress routing, envelope
// rendering, and exit mapping.
package adoptcore

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/bundle"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request carries the resolved adopt invocation: the shared runtime plus the
// --from slug.
type Request struct {
	core.ReadRequest
	From string
}

// Response carries the completed handoff: the envelope fields, the human
// summary line, and the typed data profile.
type Response struct {
	At       string
	Next     string
	Human    string
	Data     *Data
	Warnings []string
}

// Data is the adopt envelope's data profile.
type Data struct {
	Dir     string `json:"dir"`
	From    string `json:"from"`
	Adopted int    `json:"adopted"`
	Open    int    `json:"open"`
}

// Wire renders the profile in published wire order.
func (d *Data) Wire() *jq.Obj {
	return jq.NewObj("dir", d.Dir, "from", d.From, "adopted", d.Adopted, "open", d.Open)
}

// state carries one adopt run: the request, the accumulated warnings, and
// the completed handoff summary.
type state struct { //nolint:govet // adopt service state is allocated once per invocation
	req      *Request
	warnings []string
	human    string
	data     *Data
}

// Run carries the local bundle's findings into the target PR bundle.
func Run(ctx context.Context, req *Request) (*Response, *core.RefusalError) {
	st := &state{req: req}
	if err := ctx.Err(); err != nil {
		return nil, st.refuse("canceled", "canceled before adopt completed")
	}
	if ref := st.runAdopt(); ref != nil {
		return nil, ref
	}
	return st.response(), nil
}

// warn records and streams one warning.
func (st *state) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	st.req.Warn(msg)
}

// refuse builds a typed refusal carrying the accumulated warnings.
func (st *state) refuse(code, msg string) *core.RefusalError {
	return &core.RefusalError{Code: code, Message: msg, Warnings: st.warnings}
}

// response assembles the typed response after the store write.
func (st *state) response() *Response {
	return &Response{
		At:       st.req.TimeUTC(),
		Next:     nextAdopted,
		Human:    st.human,
		Data:     st.data,
		Warnings: st.warnings,
	}
}

// runAdopt mirrors the adopt block: target gates, the verbatim copy with the
// handoff stamp, and the PR-hunk re-anchor.
func (st *state) runAdopt() *core.RefusalError {
	dir, fromDir, ref := st.resolveAdoptDirs()
	if ref != nil {
		return ref
	}
	entries, ref := st.loadFromEntries(fromDir)
	if ref != nil {
		return ref
	}
	if ref := st.checkAdoptTarget(dir); ref != nil {
		return ref
	}
	if ref := st.checkAdoptFresh(dir); ref != nil {
		return ref
	}
	return st.finishAdopt(dir, fromDir, entries)
}

// resolveAdoptDirs resolves the target and source bundle dirs, refusing
// local targets and empty --from values.
func (st *state) resolveAdoptDirs() (string, string, *core.RefusalError) {
	dir := reviewRoot + st.req.Pr
	if strings.HasPrefix(st.req.Pr, localPrefix) {
		return "", "", st.refuse("bad-target", msgBadTarget)
	}
	if st.req.From == "" {
		return "", "", st.refuse("bad-from", msgBadFrom)
	}
	fromDir := reviewRoot + st.req.From
	if !strings.HasPrefix(st.req.From, localPrefix) {
		fromDir = reviewRoot + localPrefix + st.req.From
	}
	return dir, fromDir, nil
}

// loadFromEntries reads and decodes the source findings array, refusing
// missing or corrupt stores.
func (st *state) loadFromEntries(fromDir string) ([]any, *core.RefusalError) {
	if !bundle.IsFile(fromDir + findingsSuffix) {
		return nil, st.refuse("missing-findings", fmt.Sprintf(msgMissingFindings, fromDir))
	}
	fromRaw, err := os.ReadFile(fromDir + findingsSuffix)
	if err != nil {
		return nil, st.refuse("missing-findings", fmt.Sprintf(msgMissingFindings, fromDir))
	}
	entries, err := decodeEntries(fromRaw)
	if err != nil {
		return nil, st.refuse("corrupt-store", fmt.Sprintf(msgCorruptStore, fromDir))
	}
	return entries, nil
}

// checkAdoptTarget refuses missing diffs and non-GitHub targets, ensuring
// the store marker exists.
func (st *state) checkAdoptTarget(dir string) *core.RefusalError {
	if !bundle.IsFile(dir + fullDiffSuffix) {
		return st.refuse("missing-diff", fmt.Sprintf(msgMissingDiff, dir))
	}
	if err := ensureStore(dir); err != nil {
		return st.refuse("adopt-failed", fmt.Sprintf("adopt failed: %v", err))
	}
	if prov := bundleProvider(dir); prov != providerGitHub {
		return st.refuse("provider-unimplemented", msgProviderUnimplemented)
	}
	return nil
}

// checkAdoptFresh refuses targets that already carry findings.
func (st *state) checkAdoptFresh(dir string) *core.RefusalError {
	if bundle.Exists(dir + findingsSuffix) {
		return st.refuse("already-adopted", fmt.Sprintf(msgAlreadyAdopted, dir))
	}
	return nil
}

// finishAdopt stamps the handoff, re-anchors, writes the store, and records
// the human and JSON reports.
func (st *state) finishAdopt(dir, fromDir string, entries []any) *core.RefusalError {
	now := st.req.TimeUTC()
	commitID := readcommon.PrField(dir, "head_sha")
	if ref := st.ensureHunks(dir); ref != nil {
		return ref
	}
	hs := readHunks(dir + hunksSuffix)
	stamped := adoptEntries(entries, readcommon.ShortSHA(commitID), now, st.req.From, hs)
	nOpen := countStatus(stamped, statusOpen)
	if err := atomic.WriteFile(dir+findingsSuffix, []byte(jq.PrettyOrdered(stamped))); err != nil {
		return st.refuse("adopt-failed", fmt.Sprintf("could not write %s/findings.json: %v", dir, err))
	}
	st.human = fmt.Sprintf(msgAdopted, len(stamped), fromDir, nOpen)
	st.data = &Data{
		Dir:     dir,
		From:    st.req.From,
		Adopted: len(stamped),
		Open:    nOpen,
	}
	return nil
}

// decodeEntries parses findings.json as a JSON array, order-preserving.
func decodeEntries(raw []byte) ([]any, error) {
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return nil, fmt.Errorf("decode findings: %v", err)
	}
	arr, ok := doc.([]any)
	if !ok {
		return nil, errNotArray
	}
	return arr, nil
}

// adoptEntries stamps the handoff (lastSeen + adopted history event) and
// re-resolves anchors against the PR hunks, clearing drifted ones.
func adoptEntries(entries []any, shortSHA, now, from string, hs []hunks.Hunk) []any {
	for _, e := range entries {
		m, ok := e.(*jq.Obj)
		if !ok || m == nil {
			continue
		}
		m.Set("lastSeen", shortSHA)
		hist := historyOf(m)
		hist = append(hist, jq.NewObj(
			"at", now,
			"event", "adopted",
			"actor", "script",
			"note", "from "+from,
		))
		m.Set("history", hist)
		if !anchorHolds(m, hs) {
			m.Set("anchored", false)
		}
	}
	return entries
}

// anchorHolds ports the re-anchor loop: entries without a numeric path+line
// keep their anchor; the rest hold only when a PR hunk covers the range on
// the entry's side (LEFT reads OLD, else NEW).
func anchorHolds(m *jq.Obj, hs []hunks.Hunk) bool {
	path, line, ok := anchorPathLine(m)
	if !ok {
		return true
	}
	side := anchorSide(m)
	start, end, ok := anchorRange(m, line)
	if !ok {
		return true
	}
	return hunks.Contains(hs, anchorKey(path), anchorWant(side), start, end)
}

// anchorPathLine reads the entry's path and line, reporting false when the
// entry carries no numeric anchor (unanchored entries keep their anchor).
func anchorPathLine(m *jq.Obj) (string, int, bool) {
	pv, ok := m.Get("path")
	if !ok {
		return "", 0, false
	}
	path := jq.StrOf(pv)
	if path == "" {
		return "", 0, false
	}
	lv, ok := m.Get("line")
	if !ok {
		return "", 0, false
	}
	line, ok := numVal(lv)
	if !ok {
		return "", 0, false
	}
	return path, line, true
}

// anchorSide normalizes the entry's side, defaulting to RIGHT.
func anchorSide(m *jq.Obj) string {
	sv, ok := m.Get("side")
	if !ok {
		return sideRight
	}
	if s := jq.StrOf(sv); s != "" {
		return strings.ToUpper(s)
	}
	return sideRight
}

// anchorRange resolves the entry's inclusive line range, swapping inverted
// start/line pairs and reporting false for unreadable start_line values.
func anchorRange(m *jq.Obj, line int) (int, int, bool) {
	start := line
	if sv, ok := m.Get("start_line"); ok && sv != nil {
		n, ok := numVal(sv)
		if !ok {
			return 0, 0, false
		}
		start = n
	}
	if start > line {
		start, line = line, start
	}
	return start, line, true
}

// anchorWant maps an entry side to the hunk side it must match.
func anchorWant(side string) string {
	if side == sideLeft {
		return sideOld
	}
	return sideNew
}

// anchorKey strips diff path prefixes so bundle paths match hunk files.
func anchorKey(path string) string {
	if strings.HasPrefix(path, prefixA) || strings.HasPrefix(path, prefixB) {
		return path[prefixLen:]
	}
	return path
}

// numVal reads a JSON number as an int (decoded stores hold [json.Number];
// the shell's arithmetic contexts only see validated digit strings).
func numVal(v any) (int, bool) {
	if n, ok := jq.IntOf(v); ok {
		return n, true
	}
	s, ok := v.(string)
	if !ok || !jq.Digits(s) {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// historyOf reads the entry's history array (missing reads empty).
func historyOf(m *jq.Obj) []any {
	raw, ok := m.Get("history")
	if !ok {
		return []any{}
	}
	if arr, ok := raw.([]any); ok {
		return arr
	}
	return []any{}
}

// countStatus counts entries with status.
func countStatus(entries []any, status string) int {
	n := 0
	for _, e := range entries {
		if matchesStatus(e, status) {
			n++
		}
	}
	return n
}

// matchesStatus reports whether entry e carries status.
func matchesStatus(e any, status string) bool {
	m, ok := e.(*jq.Obj)
	if !ok || m == nil {
		return false
	}
	s, ok := m.Get("status")
	return ok && s == status
}

// shortSHALen is the abbreviated commit width.
const shortSHALen = 7

// shortSHA ports ${commit_id:0:7}.
func shortSHA(head string) string {
	if len(head) > shortSHALen {
		return head[:shortSHALen]
	}
	return head
}

// bundleProvider reads the bundle provider with adopt's github default: a
// missing, unreadable, or markerless record is GitHub, and any present marker
// routes on its rendered value.
func bundleProvider(dir string) string {
	if prov, present := readcommon.OpenBundle(dir).ProviderMarker(); present {
		return prov
	}
	return providerGitHub
}

// ensureStore ports ensure_store: backfill the store marker when absent.
func ensureStore(dir string) error {
	if bundle.Exists(dir + storeSuffix) {
		return nil
	}
	return atomic.WriteFile(dir+storeSuffix, []byte("{\n  \"schema_version\": \"1\"\n}\n"))
}

// ensureHunks ports ensure_hunks_snapshot: rebuild from full.diff when the
// fetch-time snapshot is missing, warning that validation is best-effort.
func (st *state) ensureHunks(dir string) *core.RefusalError {
	if bundle.Exists(dir + hunksSuffix) {
		return nil
	}
	st.warn(fmt.Sprintf(msgHunksRebuild, dir))
	raw, err := os.ReadFile(dir + fullDiffSuffix)
	if err != nil {
		return st.refuse("missing-diff", fmt.Sprintf(msgMissingDiff, dir))
	}
	if err := atomic.WriteFile(dir+hunksSuffix, []byte(hunks.Format(hunks.Parse(string(raw))))); err != nil {
		return st.refuse("adopt-failed", fmt.Sprintf("adopt failed: %v", err))
	}
	return nil
}

// readHunks parses hunks.tsv into the shared hunk index (tab-separated
// file/side/start/end rows; malformed rows are skipped like the awk read).
func readHunks(path string) []hunks.Hunk {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []hunks.Hunk
	for ln := range strings.SplitSeq(string(raw), newline) {
		if h, ok := parseHunkRow(ln); ok {
			out = append(out, h)
		}
	}
	return out
}

// parseHunkRow parses one hunks.tsv row, reporting false for blank or
// malformed rows (mirroring the awk read's skips).
func parseHunkRow(ln string) (hunks.Hunk, bool) {
	if ln == "" {
		return hunks.Hunk{}, false
	}
	f := strings.Split(ln, hunkSep)
	if len(f) != hunkFieldCount {
		return hunks.Hunk{}, false
	}
	start, ok1 := numVal(f[hunkStartIdx])
	end, ok2 := numVal(f[hunkEndIdx])
	if !ok1 || !ok2 {
		return hunks.Hunk{}, false
	}
	return hunks.Hunk{File: f[0], Side: f[1], Start: start, End: end}, true
}

const (
	// reviewRoot prefixes bundle directories under the invocation cwd.
	reviewRoot = ".review/"
	// localPrefix marks local bundle slugs.
	localPrefix = "local-"
	// providerGitHub is the only adopt target provider.
	providerGitHub = "github"
	// findingsSuffix is the findings store filename suffix.
	findingsSuffix = "/findings.json"
	// fullDiffSuffix is the bundle diff path suffix.
	fullDiffSuffix = "/full.diff"
	// storeSuffix is the store marker path suffix.
	storeSuffix = "/store.json"
	// hunksSuffix is the hunk snapshot path suffix.
	hunksSuffix = "/hunks.tsv"
	// statusOpen selects open findings for the adopted count.
	statusOpen = "open"
	// newline joins rendered body lines.
	newline = "\n"
	// hunkSep separates hunks.tsv columns.
	hunkSep = "\t"
	// sideRight is the default comment side.
	sideRight = "RIGHT"
	// sideLeft is the left comment side.
	sideLeft = "LEFT"
	// sideNew is the NEW hunk side.
	sideNew = "NEW"
	// sideOld is the OLD hunk side.
	sideOld = "OLD"
	// prefixA is the a/ diff path prefix.
	prefixA = "a/"
	// prefixB is the b/ diff path prefix.
	prefixB = "b/"
	// prefixLen strips the diff path prefixes.
	prefixLen = 2
	// hunkFieldCount is the hunks.tsv column count.
	hunkFieldCount = 4
	// hunkStartIdx indexes the hunks.tsv start column.
	hunkStartIdx = 2
	// hunkEndIdx indexes the hunks.tsv end column.
	hunkEndIdx = 3
)

const (
	msgBadTarget = "adopt targets a PR bundle, not another local one"
	msgBadFrom   = "adopt needs --from <local-slug> (a .review/local-* dir with findings.json)"
	nextAdopted  = "post the adopted findings, then calibrate to settle them"

	msgMissingFindings = "missing %s/findings.json (record the local review first)"

	msgCorruptStore = "findings store %s/findings.json is unreadable or not a findings array " +
		"(re-run record to rebuild, or restore the file)"

	msgMissingDiff = "missing %s/full.diff (fetch the PR first)"

	msgProviderUnimplemented = "adopt needs a GitHub PR target " +
		"(only GitHub posts exist to publish into)"

	msgAlreadyAdopted = "%s/findings.json exists — adopt merges once into a fresh fetch " +
		"(re-fetch or post first)"

	msgHunksRebuild = "no fetch-time hunk snapshot (%s/hunks.tsv missing) — " +
		"rebuilding from full.diff, validation is best-effort (re-fetch to pin the snapshot)"

	msgAdopted = "» adopted %d findings from %s (open: %d) — post to publish, calibrate to settle"
)

// errNotArray is the corrupt-store signal for non-array findings.
var errNotArray = valueError("not a findings array")

// valueError is a plain error value for store-shape failures.
type valueError string

// Error implements error.
func (e valueError) Error() string { return string(e) }
