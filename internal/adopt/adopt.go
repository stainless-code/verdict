// Package adopt ports the `adopt` verb: carry a local bundle's findings
// into a fresh PR bundle for posting. The findings copy verbatim (stamped
// with the handoff), anchors re-resolve against the PR hunks (drifted ones
// clear), and the store write goes through the shared atomic path.
package adopt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/bundle"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
)

// Request carries the resolved adopt invocation.
type Request struct {
	Env  map[string]string
	Repo string
	Pr   string
	Root string
}

// Run executes `adopt <owner/repo> <pr> --from <local-slug> [--json]`,
// returning the process exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	r.Rentry = ""
	from, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{Code: "canceled", Message: "canceled before adopt completed"}, cli.ExitBroke)
	}
	code, err := runAdopt(r, req, from)
	if err != nil {
		if ref, ok := errors.AsType[*cli.RefusalError](err); ok {
			return r.Refuse(ref)
		}
		return r.Refuse(&cli.RefusalError{Code: "adopt-failed", Message: fmt.Sprintf("adopt failed: %v", err)})
	}
	return code
}

// parseFlags strips adopt's flags, reporting unknown ones.
func parseFlags(r *cli.Runner, args []string) (string, int, bool) {
	from := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--from":
			if i+1 < len(args) {
				i++
				from = args[i]
			} else {
				from = ""
			}
		case "--json":
		default:
			msg := fmt.Sprintf(msgUnknownFlag, a)
			code := r.ReportUsage(&cli.UsageError{Message: msg})
			return "", code, true
		}
	}
	return from, 0, false
}

// runAdopt mirrors the adopt block: target gates, the verbatim copy with the
// handoff stamp, and the PR-hunk re-anchor.
func runAdopt(r *cli.Runner, req Request, from string) (int, error) {
	dir, fromDir, ref := resolveAdoptDirs(req, from)
	if ref != nil {
		return 0, ref
	}
	entries, ref := loadFromEntries(fromDir)
	if ref != nil {
		return 0, ref
	}
	if ref := checkAdoptTarget(dir); ref != nil {
		return 0, ref
	}
	if ref := checkAdoptFresh(dir); ref != nil {
		return 0, ref
	}
	return finishAdopt(r, dir, fromDir, from, entries)
}

// resolveAdoptDirs resolves the target and source bundle dirs, refusing
// local targets and empty --from values.
func resolveAdoptDirs(req Request, from string) (string, string, error) {
	dir := reviewRoot + req.Pr
	if strings.HasPrefix(req.Pr, localPrefix) {
		return "", "", &cli.RefusalError{Code: "bad-target", Message: msgBadTarget}
	}
	if from == "" {
		return "", "", &cli.RefusalError{Code: "bad-from", Message: msgBadFrom}
	}
	fromDir := reviewRoot + from
	if !strings.HasPrefix(from, localPrefix) {
		fromDir = reviewRoot + localPrefix + from
	}
	return dir, fromDir, nil
}

// loadFromEntries reads and decodes the source findings array, refusing
// missing or corrupt stores.
func loadFromEntries(fromDir string) ([]any, error) {
	if !bundle.IsFile(fromDir + findingsSuffix) {
		return nil, &cli.RefusalError{
			Code:    "missing-findings",
			Message: fmt.Sprintf(msgMissingFindings, fromDir),
		}
	}
	fromRaw, err := os.ReadFile(fromDir + findingsSuffix)
	if err != nil {
		return nil, &cli.RefusalError{
			Code:    "missing-findings",
			Message: fmt.Sprintf(msgMissingFindings, fromDir),
		}
	}
	entries, err := decodeEntries(fromRaw)
	if err != nil {
		return nil, &cli.RefusalError{
			Code:    "corrupt-store",
			Message: fmt.Sprintf(msgCorruptStore, fromDir),
		}
	}
	return entries, nil
}

// checkAdoptTarget refuses missing diffs and non-GitHub targets, ensuring
// the store marker exists.
func checkAdoptTarget(dir string) error {
	if !bundle.IsFile(dir + fullDiffSuffix) {
		return &cli.RefusalError{
			Code:    "missing-diff",
			Message: fmt.Sprintf(msgMissingDiff, dir),
		}
	}
	if err := ensureStore(dir); err != nil {
		return err
	}
	if prov := providerOf(dir); prov != providerGitHub {
		return &cli.RefusalError{Code: "provider-unimplemented", Message: msgProviderUnimplemented}
	}
	return nil
}

// checkAdoptFresh refuses targets that already carry findings.
func checkAdoptFresh(dir string) error {
	if bundle.Exists(dir + findingsSuffix) {
		return &cli.RefusalError{
			Code:    "already-adopted",
			Message: fmt.Sprintf(msgAlreadyAdopted, dir),
		}
	}
	return nil
}

// finishAdopt stamps the handoff, re-anchors, writes the store, and emits
// the human and JSON reports.
func finishAdopt(r *cli.Runner, dir, fromDir, from string, entries []any) (int, error) {
	commitID := prHeadSHA(dir)
	now := cli.NowUTC(r.Now())
	if err := ensureHunks(r, dir); err != nil {
		return 0, err
	}
	hs := readHunks(dir + hunksSuffix)
	stamped := adoptEntries(entries, shortSHA(commitID), now, from, hs)
	nOpen := countStatus(stamped, statusOpen)
	if err := atomic.WriteFile(dir+findingsSuffix, []byte(jq.PrettyOrdered(stamped))); err != nil {
		return 0, &cli.RefusalError{
			Code:    "adopt-failed",
			Message: fmt.Sprintf("could not write %s/findings.json: %v", dir, err),
		}
	}
	fmt.Fprintf(r.Human, msgAdopted+"\n", len(stamped), fromDir, nOpen)
	if r.JSONMode {
		emitAdoptEnvelope(r, dir, from, now, stamped, nOpen)
	}
	return 0, nil
}

// emitAdoptEnvelope prints the --json machine envelope for a completed adopt.
func emitAdoptEnvelope(r *cli.Runner, dir, from, now string, entries []any, nOpen int) {
	env := jq.NewObj(
		"schema_version", "1",
		"verb", "adopt",
		"repo", r.Repo,
		"pr", cli.PrJSON(r.Pr),
		"at", now,
		"next", "post the adopted findings, then calibrate to settle them",
		"warnings", cliStrings(r.Warnings),
		"data", jq.NewObj("dir", dir, "from", from, "adopted", len(entries), "open", nOpen),
		"error", nil,
	)
	fmt.Fprintln(r.Out, env.MarshalPretty())
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

// numVal reads a JSON number as an int (decoded stores hold json.Number;
// the shell's arithmetic contexts only see validated digit strings).
func numVal(v any) (int, bool) {
	if n, ok := jq.IntOf(v); ok {
		return n, true
	}
	if s, ok := v.(string); ok {
		return atoi(s)
	}
	return 0, false
}

// atoi parses a validated digit string (the shell's [[ =~ ^[0-9]+$ ]] gate).
func atoi(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*decimalBase + int(s[i]-'0')
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

// shortSHA ports ${commit_id:0:7}.
func shortSHA(head string) string {
	if len(head) > shortSHALen {
		return head[:shortSHALen]
	}
	return head
}

// providerOf ports `jq -r '.provider // "github"'` with the unreadable-file
// fallback to github.
func providerOf(dir string) string {
	raw, err := os.ReadFile(dir + prSuffix)
	if err != nil {
		return providerGitHub
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return providerGitHub
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return providerGitHub
	}
	prov, ok := m["provider"]
	if !ok || prov == nil {
		return providerGitHub
	}
	if s, ok := prov.(string); ok {
		return s
	}
	return providerOther
}

// prHeadSHA ports pr_field head_sha for the github provider shape.
func prHeadSHA(dir string) string {
	raw, err := os.ReadFile(dir + prSuffix)
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
	prov := ""
	if p, ok := m["provider"].(string); ok {
		prov = p
	}
	if prov == "" {
		return jq.StrOf(m["headRefOid"])
	}
	return jq.StrOf(m["head_sha"])
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
func ensureHunks(r *cli.Runner, dir string) error {
	if bundle.Exists(dir + hunksSuffix) {
		return nil
	}
	r.Warn(fmt.Sprintf(msgHunksRebuild, dir))
	raw, err := os.ReadFile(dir + fullDiffSuffix)
	if err != nil {
		return &cli.RefusalError{Code: "missing-diff", Message: fmt.Sprintf(msgMissingDiff, dir)}
	}
	return atomic.WriteFile(dir+hunksSuffix, []byte(hunks.Format(hunks.Parse(string(raw)))))
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
	start, ok1 := atoi(f[hunkStartIdx])
	end, ok2 := atoi(f[hunkEndIdx])
	if !ok1 || !ok2 {
		return hunks.Hunk{}, false
	}
	return hunks.Hunk{File: f[0], Side: f[1], Start: start, End: end}, true
}

// cliStrings converts warnings to JSON string values.
func cliStrings(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

const (
	// reviewRoot prefixes bundle directories under the invocation cwd.
	reviewRoot = ".review/"
	// localPrefix marks local bundle slugs.
	localPrefix = "local-"
	// providerGitHub is the only adopt target provider.
	providerGitHub = "github"
	// providerOther marks unparseable provider values as non-GitHub.
	providerOther = "non-github"
	// findingsSuffix is the findings store filename suffix.
	findingsSuffix = "/findings.json"
	// prSuffix is the bundle PR-record path suffix.
	prSuffix = "/PR.json"
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
	// shortSHALen is the ${var:0:7} width.
	shortSHALen = 7
	// decimalBase is the radix for hand-rolled integer parsing.
	decimalBase = 10
	// hunkFieldCount is the hunks.tsv column count.
	hunkFieldCount = 4
	// hunkStartIdx indexes the hunks.tsv start column.
	hunkStartIdx = 2
	// hunkEndIdx indexes the hunks.tsv end column.
	hunkEndIdx = 3
)

const (
	msgUnknownFlag = "unknown flag '%s' for adopt (want --from <local-slug>, --json)"
	msgBadTarget   = "adopt targets a PR bundle, not another local one"
	msgBadFrom     = "adopt needs --from <local-slug> (a .review/local-* dir with findings.json)"

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
