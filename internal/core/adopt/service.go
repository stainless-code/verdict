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
	"github.com/stainless-code/verdict/internal/core"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/identity"
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

type state struct { //nolint:govet // adopt service state is allocated once per invocation
	req       *Request
	warnings  []string
	human     string
	data      *Data
	autoPatch string
}

// Run carries the local bundle's findings into the target PR bundle.
func Run(ctx context.Context, req *Request) (*Response, *core.RefusalError) {
	st := &state{req: req}
	if err := ctx.Err(); err != nil {
		return nil, st.refuse("canceled", "canceled before adopt completed")
	}
	if ref := st.runAdopt(ctx); ref != nil {
		return nil, ref
	}
	return st.response(), nil
}

func (st *state) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	st.req.Warn(msg)
}

func (st *state) refuse(code, msg string) *core.RefusalError {
	return &core.RefusalError{Code: code, Message: msg, Warnings: st.warnings}
}

func (st *state) response() *Response {
	return &Response{
		At:       st.req.TimeUTC(),
		Next:     nextAdopted,
		Human:    st.human,
		Data:     st.data,
		Warnings: st.warnings,
	}
}

func (st *state) runAdopt(ctx context.Context) *core.RefusalError {
	dir, fromDir, display, ref := st.resolveAdoptDirs(ctx)
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
	return st.finishAdopt(dir, fromDir, display, entries)
}

func (st *state) resolveAdoptDirs(ctx context.Context) (string, string, string, *core.RefusalError) {
	root := st.req.RepoRootPath()
	dir := readcommon.ResolveBundleDir(root, st.req.Pr)
	if strings.HasPrefix(st.req.Pr, localPrefix) {
		return "", "", "", st.refuse("bad-target", msgBadTarget)
	}
	if st.req.From == "" {
		return "", "", "", st.refuse("bad-from", msgBadFrom)
	}
	if st.req.From == fromAuto {
		fromDir, display, ref := st.resolveAutoFrom(ctx, dir)
		if ref != nil {
			return "", "", "", ref
		}
		return dir, fromDir, display, nil
	}
	fromDir := readcommon.ResolveBundleDir(root, st.req.From)
	if !strings.HasPrefix(st.req.From, localPrefix) {
		fromDir = readcommon.ResolveBundleDir(root, localPrefix+st.req.From)
	}
	return dir, fromDir, st.req.From, nil
}

// identityCommander returns the injected command runner for patch identity
// recomputation, or nil when the request carries none: without a runner the
// algorithm claim cannot be recomputed, so git-kind identities stay
// unverified instead of trusted.
func (st *state) identityCommander() core.Commander {
	if st.req == nil || st.req.Runtime == nil {
		return nil
	}
	return st.req.Commander
}

// resolveAutoFrom matches the target bundle's verified patch identity
// against every local bundle: exactly one twin adopts, zero or many refuse
// naming the candidates. The target identity must bind the target's present
// bytes and head, and each twin must bind its own bytes and head with the
// requested repository — a stale or foreign bundle is not a twin. The auto
// sentinel matches before the local- prefix rule, so a local bundle
// literally named local-auto stays reachable as --from local-auto.
func (st *state) resolveAutoFrom(ctx context.Context, dir string) (string, string, *core.RefusalError) {
	target := readcommon.OpenBundle(dir)
	cur, ok := identity.Verify(ctx, st.identityCommander(), dir, target.Head(), target.Base())
	if !ok || cur.PatchID == nil {
		return "", "", st.refuse("no-patch-match", fmt.Sprintf(
			"no-patch-match: target %s has no verifiable patch identity "+
				"(re-fetch so identity.json exists) — pass --from <local-slug>", dir))
	}
	twins := st.scanPatchTwins(ctx, cur)
	switch len(twins) {
	case 0:
		return "", "", st.refuse("no-patch-match", fmt.Sprintf(
			"no-patch-match: patch %s (%s) has no local twin (target %s) — pass --from <local-slug>",
			*cur.PatchID, cur.PatchIDKind, dir))
	case 1:
		st.autoPatch = *cur.PatchID
		return readcommon.ResolveBundleDir(st.req.RepoRootPath(), twins[0]), "auto -> " + twins[0], nil
	default:
		return "", "", st.refuse("ambiguous-patch-match", ambiguousPatchMessage(*cur.PatchID, twins))
	}
}

// scanPatchTwins returns the sorted local bundle slugs whose verified
// identity matches cur in the requested repository. The scan is bounded to
// the bundle home's local-* identity files (non-recursive), so malformed,
// stale, or foreign receipts skip instead of refusing.
func (st *state) scanPatchTwins(ctx context.Context, cur *identity.Identity) []string {
	entries, err := os.ReadDir(readcommon.StateDir(st.req.RepoRootPath()))
	if err != nil {
		return nil
	}
	var twins []string
	for _, entry := range entries {
		if twin, ok := st.twinSlug(ctx, entry, cur); ok {
			twins = append(twins, twin)
		}
	}
	return twins
}

// twinSlug reads one candidate bundle: non-local, byte-stale, head-stale,
// unproven-repository, and foreign-repository identities never match.
func (st *state) twinSlug(ctx context.Context, entry os.DirEntry, cur *identity.Identity) (string, bool) {
	name := entry.Name()
	if !entry.IsDir() || !strings.HasPrefix(name, localPrefix) {
		return "", false
	}
	twinDir := readcommon.ResolveBundleDir(st.req.RepoRootPath(), name)
	head := readcommon.JSONStr(twinDir+"/source.json", "head_sha")
	base := readcommon.JSONStr(twinDir+"/source.json", "base_sha")
	id, ok := identity.Verify(ctx, st.identityCommander(), twinDir, head, base)
	if !ok || !identity.Match(cur, id) {
		return "", false
	}
	if repo := readcommon.JSONStr(twinDir+"/source.json", "repo"); repo == "" || repo != st.req.Repo {
		return "", false
	}
	return name, true
}

// ambiguousPatchMessage names the twin candidates (max five shown, then
// +N more) so the human picks one explicitly.
func ambiguousPatchMessage(patch string, twins []string) string {
	shown := twins
	more := ""
	if len(twins) > maxTwinShown {
		shown = twins[:maxTwinShown]
		more = fmt.Sprintf(" (+%d more)", len(twins)-maxTwinShown)
	}
	return fmt.Sprintf("ambiguous-patch-match: patch %s matches %d local bundles: %s%s — "+
		"pass the exact --from <local-slug> named above", patch, len(twins), strings.Join(shown, ", "), more)
}

func (st *state) loadFromEntries(fromDir string) ([]any, *core.RefusalError) {
	if !readcommon.IsFile(fromDir + findingsSuffix) {
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

func (st *state) checkAdoptTarget(dir string) *core.RefusalError {
	if !readcommon.IsFile(dir + fullDiffSuffix) {
		return st.refuse("missing-diff", fmt.Sprintf(msgMissingDiff, dir))
	}
	if err := ensureStore(dir); err != nil {
		return st.refuse("adopt-failed", fmt.Sprintf("adopt failed: %v", err))
	}
	if prov := bundleProvider(dir); !core.CapabilitiesFor(prov).Post {
		return st.refuse("provider-unimplemented", fmt.Sprintf(msgProviderUnimplemented, strings.Join(postableProviders(), ", "), prov))
	}
	return nil
}

// postableProviders lists the providers adopt accepts, read off the
// capability table in stable order.
func postableProviders() []string {
	var out []string
	for _, p := range []string{"azure", "bitbucket", "forgejo", "gitcafe", "github", "gitlab"} {
		if core.CapabilitiesFor(p).Post {
			out = append(out, p)
		}
	}
	return out
}

func (st *state) checkAdoptFresh(dir string) *core.RefusalError {
	if readcommon.Exists(dir + findingsSuffix) {
		return st.refuse("already-adopted", fmt.Sprintf(msgAlreadyAdopted, dir))
	}
	return nil
}

func (st *state) finishAdopt(dir, fromDir, display string, entries []any) *core.RefusalError {
	now := st.req.TimeUTC()
	commitID := readcommon.PrField(dir, "head_sha")
	if ref := st.ensureHunks(dir); ref != nil {
		return ref
	}
	hs := readHunks(dir + hunksSuffix)
	stamped := adoptEntries(entries, readcommon.ShortSHA(commitID), now, display, hs)
	nOpen := countStatus(stamped, statusOpen)
	if err := atomic.WriteFile(dir+findingsSuffix, []byte(jq.PrettyOrdered(stamped))); err != nil {
		return st.refuse("adopt-failed", fmt.Sprintf("could not write %s/findings.json: %v", dir, err))
	}
	if st.autoPatch != "" {
		st.human = fmt.Sprintf("» adopted %d findings from %s (patch %s) (open: %d)",
			len(stamped), display, st.autoPatch, nOpen)
	} else {
		st.human = fmt.Sprintf(msgAdopted, len(stamped), fromDir, nOpen)
	}
	st.data = &Data{
		Dir:     dir,
		From:    display,
		Adopted: len(stamped),
		Open:    nOpen,
	}
	return nil
}

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
		recordcore.BackfillEntry(m)
	}
	return entries
}

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

func anchorWant(side string) string {
	if side == sideLeft {
		return sideOld
	}
	return sideNew
}

func anchorKey(path string) string {
	if strings.HasPrefix(path, prefixA) || strings.HasPrefix(path, prefixB) {
		return path[prefixLen:]
	}
	return path
}

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

func countStatus(entries []any, status string) int {
	n := 0
	for _, e := range entries {
		if matchesStatus(e, status) {
			n++
		}
	}
	return n
}

func matchesStatus(e any, status string) bool {
	m, ok := e.(*jq.Obj)
	if !ok || m == nil {
		return false
	}
	s, ok := m.Get("status")
	return ok && s == status
}

func bundleProvider(dir string) string {
	if prov, present := readcommon.OpenBundle(dir).ProviderMarker(); present {
		return prov
	}
	return providerGitHub
}

func ensureStore(dir string) error {
	if readcommon.Exists(dir + storeSuffix) {
		return nil
	}
	return atomic.WriteFile(dir+storeSuffix, []byte("{\n  \"schema_version\": \"1\"\n}\n"))
}

func (st *state) ensureHunks(dir string) *core.RefusalError {
	if readcommon.Exists(dir + hunksSuffix) {
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
	// localPrefix marks local bundle slugs.
	localPrefix = "local-"
	// fromAuto is the reserved --from sentinel for patch-identity matching;
	// it never names a directory (a local bundle literally named local-auto
	// stays reachable as --from local-auto).
	fromAuto = "auto"
	// maxTwinShown caps the ambiguous-patch-match candidate list.
	maxTwinShown = 5
	// providerGitHub is the provider a bundle without a host marker reads as.
	providerGitHub = "github"
	findingsSuffix = "/findings.json"
	// fullDiffSuffix is the bundle diff path suffix.
	fullDiffSuffix = "/full.diff"
	// storeSuffix is the store marker path suffix.
	storeSuffix = "/store.json"
	hunksSuffix = "/hunks.tsv"
	statusOpen  = "open"
	newline     = "\n"
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
	prefixLen      = 2
	hunkFieldCount = 4
	// hunkStartIdx indexes the hunks.tsv start column.
	hunkStartIdx = 2
	hunkEndIdx   = 3
)

const (
	msgBadTarget = "adopt targets a PR bundle, not another local one"
	msgBadFrom   = "adopt needs --from <local-slug> (a .verdict/state/local-* dir with findings.json)"
	nextAdopted  = "post the adopted findings, then calibrate to settle them"

	msgMissingFindings = "missing %s/findings.json (record the local review first)"

	msgCorruptStore = "findings store %s/findings.json is unreadable or not a findings array " +
		"(re-run record to rebuild, or restore the file)"

	msgMissingDiff = "missing %s/full.diff (fetch the PR first)"

	msgProviderUnimplemented = "adopt targets a bundle verdict can post to (%s), " +
		"and this bundle is %q — adopt only hands findings to a host with a submit path"

	msgAlreadyAdopted = "%s/findings.json exists — adopt merges once into a fresh fetch " +
		"(re-fetch or post first)"

	msgHunksRebuild = "no fetch-time hunk snapshot (%s/hunks.tsv missing) — " +
		"rebuilding from full.diff, validation is best-effort (re-fetch to pin the snapshot)"

	msgAdopted = "» adopted %d findings from %s (open: %d) — post to publish, calibrate to settle"
)

var errNotArray = valueError("not a findings array")

type valueError string

func (e valueError) Error() string { return string(e) }
