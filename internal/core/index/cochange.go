package indexcore

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
)

// Co-change cache and window constants.
const (
	cochangeDirName    = "cochange"
	cochangeKeep       = 2
	cochangeSchema     = "1"
	commitHeaderPrefix = "\x01"
	commitDateFormat   = "2006-01-02T15:04:05Z07:00"
	jaccardDecimals    = 10000
	weekLength         = 7
	monthsPerYear      = 12
	floatBits          = 64
)

// shallowCoChangeWarning is the published warning a depth-limited clone gets:
// the counts are true of the commits present, not of the full history.
const shallowCoChangeWarning = "» index: shallow clone: co-change counts see only the commits present; " +
	"run 'git fetch --unshallow' for the full history"

// coChangeCacheWarning is the one line a query that could not cache its window
// gets: the cache is an optimization, never a reason to refuse or to write
// outside a store that already exists.
const coChangeCacheWarning = "» index: co-change cache not written: "

// coChangeDoc is the cached history window: one bounded git log, in the order
// git printed it. It carries no timestamp, so the same window writes the same
// bytes.
type coChangeDoc struct {
	SchemaVersion string         `json:"schema_version"`
	Head          string         `json:"head"`
	Since         string         `json:"since"`
	Changes       []windowCommit `json:"changes"`
	Commits       int            `json:"commits"`
}

// windowCommit is one commit in the window and the paths it touched.
type windowCommit struct {
	Commit string   `json:"commit"`
	Paths  []string `json:"paths"`
}

// windowSpec is the resolved --since form: a computed cutoff date or a commit
// range, plus the normalized spec the cache validates against.
type windowSpec struct {
	cutoff   string
	revRange string
	cache    string
}

// coChanged executes `index co-changed`.
//
//nolint:revive // the query ladder is one resolution step per rule
func (st *state) coChanged(ctx context.Context) (*Response, *core.RefusalError) {
	commits, limit, minCount, ref := st.coChangeBounds()
	if ref != nil {
		return nil, ref
	}
	p := normalizePath(st.req.Path)
	if p == "" {
		return nil, &core.RefusalError{
			Code: codeBadFlags, Message: "index co-changed wants one repository path",
		}
	}
	root := st.sourceRoot()
	st.trust = st.readTrust(ctx, root)
	head, ref := st.resolveCommit(ctx, root, "HEAD")
	if ref != nil {
		return nil, ref
	}
	tree, ref := st.resolveTree(ctx, root, head)
	if ref != nil {
		return nil, ref
	}
	entries, ref := st.lsTree(ctx, root, tree)
	if ref != nil {
		return nil, ref
	}
	if _, ok := entryByPath(entries, p); !ok {
		return nil, &core.RefusalError{
			Code: codeBadFlags, Message: "index co-changed: " + p + " is not a file at HEAD",
		}
	}
	spec, ref := st.coChangeSpec(ctx, root, head)
	if ref != nil {
		return nil, ref
	}
	shallow := st.warnShallow(ctx, root)
	doc, ref := st.coChangeWindow(ctx, root, head, commits, spec, shallow)
	if ref != nil {
		return nil, ref
	}
	target, ref := st.ownersAt(ctx, root, entries, p)
	if ref != nil {
		return nil, ref
	}
	rows, targetCommits := coChangePartners(doc.Changes, p, minCount)
	return st.coChangeResponse(doc, target, rows, targetCommits, limit), nil
}

// coChangeBounds resolves and bounds --commits, --limit, and --min-count.
func (st *state) coChangeBounds() (int, int, int, *core.RefusalError) {
	commits, ref := boundedValue(QueryCoChanged, "--commits", st.req.Commits,
		DefaultCoChangeCommits, MaxCommitWindow)
	if ref != nil {
		return 0, 0, 0, ref
	}
	limit, ref := boundedValue(QueryCoChanged, "--limit", st.req.Limit, DefaultCoChangeLimit, MaxLimit)
	if ref != nil {
		return 0, 0, 0, ref
	}
	minCount, ref := boundedValue(QueryCoChanged, "--min-count", st.req.MinCount,
		DefaultMinCount, MaxCommitWindow)
	if ref != nil {
		return 0, 0, 0, ref
	}
	return commits, limit, minCount, nil
}

// coChangeSpec resolves --since: a duration is a calendar span back from HEAD's
// committer date, a ref is a commit whose history is excluded.
func (st *state) coChangeSpec(ctx context.Context, root, head string) (windowSpec, *core.RefusalError) {
	spec := strings.TrimSpace(st.req.Since)
	if spec == "" {
		return windowSpec{}, nil
	}
	if span, ok := parseCoChangeSpan(spec); ok {
		return st.coChangeCutoff(ctx, root, head, spec, span)
	}
	sha, ref := st.resolveCommit(ctx, root, spec)
	if ref != nil {
		return windowSpec{}, ref
	}
	return windowSpec{revRange: sha + ".." + head, cache: sha}, nil
}

// coChangeCutoff computes the --since date a duration names, from HEAD's own
// committer date so the window never depends on the wall clock.
func (st *state) coChangeCutoff(
	ctx context.Context, root, head, spec string, span coChangeSpan,
) (windowSpec, *core.RefusalError) {
	out, exit, stderr, err := st.git(ctx, root, "log", "-1", "--format=%cI", head)
	if err != nil {
		return windowSpec{}, asRefusal(err)
	}
	if exit != 0 {
		return windowSpec{}, &core.RefusalError{
			Code: codeIndexFailed, Message: "git log failed reading HEAD's commit date: " + firstLine(stderr),
		}
	}
	at, err := time.Parse(commitDateFormat, firstLine(out))
	if err != nil {
		return windowSpec{}, &core.RefusalError{
			Code: codeIndexFailed, Message: "unreadable HEAD commit date: " + firstLine(out),
		}
	}
	cutoff := span.back(at.UTC()).Format(commitDateFormat)
	return windowSpec{cutoff: cutoff, cache: spec}, nil
}

// coChangeSpan is one parsed duration: an amount and its unit.
type coChangeSpan struct {
	unit string
	n    int
}

// coChangeSpanRe matches the accepted duration shapes; a bare `m` never
// matches, so a month is `mo` and a minute cannot be typed by accident.
var coChangeSpanRe = regexp.MustCompile(`^([0-9]+)(d|w|mo|y)$`)

// parseCoChangeSpan parses one --since duration.
func parseCoChangeSpan(spec string) (coChangeSpan, bool) {
	match := coChangeSpanRe.FindStringSubmatch(spec)
	if match == nil {
		return coChangeSpan{}, false
	}
	n, err := strconv.Atoi(match[1])
	if err != nil || n <= 0 {
		return coChangeSpan{}, false
	}
	return coChangeSpan{n: n, unit: match[2]}, true
}

// back moves a commit date by the span.
func (s coChangeSpan) back(at time.Time) time.Time {
	switch s.unit {
	case "d":
		return at.AddDate(0, 0, -s.n)
	case "w":
		return at.AddDate(0, 0, -weekLength*s.n)
	case "mo":
		return at.AddDate(0, -s.n, 0)
	case "y":
		return at.AddDate(0, -monthsPerYear*s.n, 0)
	default:
		return at
	}
}

// warnShallow reports a depth-limited clone: a boundary commit reads as the
// history root, so co-change counts are true of what is present only. An
// unreadable probe counts as shallow, so a window nothing proves complete is
// never cached.
func (st *state) warnShallow(ctx context.Context, root string) bool {
	out, exit, _, err := st.git(ctx, root, "rev-parse", "--is-shallow-repository")
	if err != nil || exit != 0 {
		return true
	}
	if strings.TrimSpace(out) != "true" {
		return false
	}
	st.warn(shallowCoChangeWarning)
	return true
}

// coChangeWindow reads the bounded window from the cache or from one git log.
func (st *state) coChangeWindow(
	ctx context.Context, root, head string, commits int, spec windowSpec, shallow bool,
) (*coChangeDoc, *core.RefusalError) {
	dir := coChangeDir(root)
	path := coChangePath(dir, head, commits)
	if doc := readCoChangeCache(path, head, commits, spec.cache); doc != nil {
		return doc, nil
	}
	changes, ref := st.readCoChangeLog(ctx, root, head, commits, spec)
	if ref != nil {
		return nil, ref
	}
	doc := &coChangeDoc{
		SchemaVersion: cochangeSchema, Head: head, Commits: commits,
		Since: spec.cache, Changes: changes,
	}
	st.cacheCoChange(root, dir, path, doc, shallow)
	return doc, nil
}

// coChangeDir returns one repository's co-change cache directory. Only
// [state.cacheCoChange] writes there; a read never creates it.
func coChangeDir(root string) string {
	return filepath.Join(storePath(root), cochangeDirName)
}

// cacheCoChange stores one window best-effort. A query is a read: it never
// creates the index store, and a window that cannot be cached loses the sum of
// nothing — the rows are already computed — so the cache never fails the
// query, and one warning line names the reason it is absent.
func (st *state) cacheCoChange(root, dir, path string, doc *coChangeDoc, shallow bool) {
	switch {
	case shallow:
		// The shallow warning already says why this window is partial.
	case !utf8Paths(doc.Changes):
		st.warn(coChangeCacheWarning + "a path in the window is not valid UTF-8")
	case !storeExists(storePath(root)):
		st.warn(coChangeCacheWarning + "the index store is absent (run verdict index build)")
	default:
		if reason := writeCoChangeCache(dir, path, doc); reason != "" {
			st.warn(coChangeCacheWarning + reason)
		}
	}
}

// utf8Paths reports whether every path in the window survives a JSON
// round-trip unchanged: a non-UTF-8 byte becomes U+FFFD on encode, so a cached
// window would stop matching the target git actually printed.
func utf8Paths(commits []windowCommit) bool {
	for i := range commits {
		for _, p := range commits[i].Paths {
			if !utf8.ValidString(p) {
				return false
			}
		}
	}
	return true
}

// storeExists reports whether the index store exists, without creating it.
func storeExists(store string) bool {
	info, err := os.Stat(store)
	return err == nil && info.IsDir()
}

// coChangePath returns one cache file's path.
func coChangePath(dir, head string, commits int) string {
	return filepath.Join(dir, head+"-"+strconv.Itoa(commits)+jsonSuffix)
}

// readCoChangeCache reads one window cache. A missing, truncated, stale, or
// foreign cache is a miss: the window is derived data, so rebuilding it is
// always correct.
func readCoChangeCache(path, head string, commits int, since string) *coChangeDoc {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc coChangeDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	if doc.SchemaVersion != cochangeSchema || doc.Head != head || doc.Commits != commits || doc.Since != since {
		return nil
	}
	return &doc
}

// coChangeCacheLink names the symlinked component that forbids a cache write,
// or "" when the write may proceed. A symlinked store, cache directory, or
// cache file would carry the write or the prune outside the checkout, so the
// window is skipped like any other the cache cannot hold.
func coChangeCacheLink(dir, path string) string {
	switch {
	case isSymlink(filepath.Dir(dir)):
		return "the index store is a symlink"
	case isSymlink(dir):
		return "the co-change cache directory is a symlink"
	case isSymlink(path):
		return "the co-change cache file is a symlink"
	}
	return ""
}

// isSymlink reports whether path exists and is a symbolic link, without
// following it.
func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

// writeCoChangeCache stores one window and keeps the two newest caches. It
// creates the cache directory inside an existing store and nothing above it.
// What it cannot do, it returns as the warning's reason: a cache miss costs the
// answer nothing, so no caller inspects the cause.
func writeCoChangeCache(dir, path string, doc *coChangeDoc) string {
	if reason := coChangeCacheLink(dir, path); reason != "" {
		return reason
	}
	if err := os.Mkdir(dir, storeDirPerm); err != nil && !errors.Is(err, os.ErrExist) {
		return "could not create " + dir + ": " + err.Error()
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return "could not encode the window: " + err.Error()
	}
	if err := atomic.WriteFile(path, raw); err != nil {
		return "could not write " + path + ": " + err.Error()
	}
	pruneCoChangeCache(dir)
	return ""
}

// pruneCoChangeCache deletes every cache but the two newest, name-breaking
// ties so the choice is never map order.
func pruneCoChangeCache(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	files := cacheCandidates(entries)
	slices.SortFunc(files, newestCacheFirst)
	for _, file := range files[min(len(files), cochangeKeep):] {
		_ = os.Remove(filepath.Join(dir, file.name))
	}
}

// cacheCandidate is one prunable cache file with its modification time.
type cacheCandidate struct {
	name string
	mod  int64
}

// cacheCandidates collects the regular JSON files one cache directory holds. A
// symlink is skipped, never pruned or written through.
func cacheCandidates(entries []os.DirEntry) []cacheCandidate {
	var files []cacheCandidate
	for _, e := range entries {
		if e.IsDir() || e.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(e.Name(), jsonSuffix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, cacheCandidate{name: e.Name(), mod: info.ModTime().UnixNano()})
	}
	return files
}

// newestCacheFirst orders cache files newest first, name-breaking ties so the
// choice is never directory order.
func newestCacheFirst(a, b cacheCandidate) int {
	if a.mod != b.mod {
		return cmp.Compare(b.mod, a.mod)
	}
	return strings.Compare(b.name, a.name)
}

// readCoChangeLog reads one bounded history window with a single git log call,
// pinned to the resolved commit so a ref moving mid-query cannot mix windows.
// `--no-renames` lists both sides of a rename, so the window holds the names
// the file had rather than one path the repository's diff config chose.
func (st *state) readCoChangeLog(
	ctx context.Context, root, head string, commits int, spec windowSpec,
) ([]windowCommit, *core.RefusalError) {
	args := []string{
		"log", "--no-merges", "--no-renames", "--name-only", "-z", "-n", strconv.Itoa(commits),
		"--pretty=format:" + commitHeaderPrefix + "%H",
	}
	if spec.cutoff != "" {
		args = append(args, "--since="+spec.cutoff)
	}
	revision := spec.revRange
	if revision == "" {
		revision = head
	}
	out, exit, stderr, err := st.git(ctx, root, append(args, revision, "--")...)
	if err != nil {
		return nil, asRefusal(err)
	}
	if exit != 0 {
		return nil, &core.RefusalError{Code: codeIndexFailed, Message: "git log failed: " + firstLine(stderr)}
	}
	return parseCoChangeLog(out)
}

// parseCoChangeLog reads `git log --no-merges --name-only -z` records: each
// commit is a \x01<sha> header record followed by its NUL-terminated paths.
//
//nolint:revive // the record grammar reads as one pass
func parseCoChangeLog(out string) ([]windowCommit, *core.RefusalError) {
	var commits []windowCommit
	for record := range strings.SplitSeq(out, "\x00") {
		if record == "" {
			continue
		}
		if sha, first, ok := cutCommitHeader(record); ok {
			commits = append(commits, windowCommit{Commit: sha})
			if first != "" {
				commits[len(commits)-1].Paths = append(commits[len(commits)-1].Paths, first)
			}
			continue
		}
		if len(commits) == 0 {
			return nil, &core.RefusalError{Code: codeIndexCorrupt, Message: "git log returned a path outside a commit"}
		}
		commits[len(commits)-1].Paths = append(commits[len(commits)-1].Paths, record)
	}
	for i := range commits {
		slices.Sort(commits[i].Paths)
		commits[i].Paths = slices.Compact(commits[i].Paths)
	}
	return commits, nil
}

// cutCommitHeader splits one commit header record into its object id and the
// first path name git printed after the header's newline.
func cutCommitHeader(record string) (string, string, bool) {
	if !strings.HasPrefix(record, commitHeaderPrefix) {
		return "", "", false
	}
	body := record[len(commitHeaderPrefix):]
	header, first, ok := strings.Cut(body, newline)
	if !ok {
		header, first = body, ""
	}
	if !shaRe.MatchString(header) {
		return "", "", false
	}
	return header, first, true
}

// coChangePartners counts every path that moved with the target and orders the
// partners by co-change count, then path.
func coChangePartners(commits []windowCommit, target string, minCount int) ([]CoChangeRow, int) {
	partnerCommits, both, targetCommits := countCoChanges(commits, target)
	rows := partnerRows(both, partnerCommits, targetCommits, minCount)
	slices.SortFunc(rows, func(a, b CoChangeRow) int {
		if a.Commits != b.Commits {
			return b.Commits - a.Commits
		}
		return strings.Compare(a.Path, b.Path)
	})
	return rows, targetCommits
}

// countCoChanges counts every partner's commits and the commits it shares with
// the target, plus the target's own commit count.
func countCoChanges(commits []windowCommit, target string) (map[string]int, map[string]int, int) {
	partnerCommits := map[string]int{}
	both := map[string]int{}
	targetCommits := 0
	for i := range commits {
		targetCommits += countCommit(commits[i].Paths, target, partnerCommits, both)
	}
	return partnerCommits, both, targetCommits
}

// countCommit counts one commit's partners and reports whether the commit
// touched the target.
func countCommit(paths []string, target string, partnerCommits, both map[string]int) int {
	hasTarget := slices.Contains(paths, target)
	for _, p := range paths {
		if p == target {
			continue
		}
		partnerCommits[p]++
		if hasTarget {
			both[p]++
		}
	}
	if !hasTarget {
		return 0
	}
	return 1
}

// partnerRows builds one row per partner at or above the minimum count.
func partnerRows(both, partnerCommits map[string]int, targetCommits, minCount int) []CoChangeRow {
	var rows []CoChangeRow
	for p, co := range both {
		if co < minCount {
			continue
		}
		rows = append(rows, CoChangeRow{
			Path: p, Commits: co, Jaccard: jaccard(co, targetCommits, partnerCommits[p]),
		})
	}
	return rows
}

// coChangeHuman renders the partner header, one row per partner, and the
// truncation count.
func (st *state) coChangeHuman(
	target *Target, all, rows []CoChangeRow, truncated int, window Window, targetCommits int,
) {
	rangeText := ""
	if window.Commits > 0 {
		rangeText = ", " + short(window.First) + ".." + short(window.Last)
	}
	if len(all) == 0 {
		st.line(fmt.Sprintf("index: no co-changed partners of %s across %d commit(s)%s (target in %d)",
			quotePath(target.Path), window.Commits, rangeText, targetCommits))
		return
	}
	st.line(fmt.Sprintf("index: %d partner(s) of %s across %d commit(s)%s (target in %d)",
		len(all), quotePath(target.Path), window.Commits, rangeText, targetCommits))
	for _, row := range rows {
		st.line(fmt.Sprintf("%s %d commit(s) jaccard %s",
			quotePath(row.Path), row.Commits, strconv.FormatFloat(row.Jaccard, 'f', -1, floatBits)))
	}
	if truncated > 0 {
		st.line(fmt.Sprintf("index: showing %d of %d partners (%d not shown; raise --limit up to %d)",
			len(rows), len(all), truncated, MaxLimit))
	}
}

// jaccard is |A∩B|/|A∪B| for the target and one partner, rounded to four
// decimals so the published ratio is a literal a reader can re-derive.
func jaccard(both, targetCommits, partnerCommits int) float64 {
	union := targetCommits + partnerCommits - both
	if union <= 0 {
		return 0
	}
	return math.Round(float64(both)/float64(union)*jaccardDecimals) / jaccardDecimals
}

// windowFrom derives the echoed window from the parsed commits.
func windowFrom(commits []windowCommit) Window {
	if len(commits) == 0 {
		return Window{}
	}
	return Window{
		First:   commits[len(commits)-1].Commit,
		Last:    commits[0].Commit,
		Commits: len(commits),
	}
}

// coChangeResponse renders the partner list, truncating rows with a count.
func (st *state) coChangeResponse(
	doc *coChangeDoc, target *Target, rows []CoChangeRow, targetCommits, limit int,
) *Response {
	all := rows
	truncated := 0
	if len(rows) > limit {
		truncated = len(rows) - limit
		rows = rows[:limit]
	}
	window := windowFrom(doc.Changes)
	st.coChangeHuman(target, all, rows, truncated, window, targetCommits)
	next := "run verdict index tests-for " + quotePath(target.Path) + " to find candidate tests"
	if len(all) == 0 {
		next = "widen --commits or lower --min-count to read more history"
	}
	return &Response{
		At:   st.req.TimeUTC(),
		Next: next,
		Data: &Data{
			Kind: QueryCoChanged, Path: target.Path, Window: window,
			Partners: len(all), TargetCommits: targetCommits,
			CoChangeRows: rows, Truncated: truncated, Limit: limit, Target: target,
		},
	}
}
