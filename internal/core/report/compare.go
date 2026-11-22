package reportcore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/coverage"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Refusal codes for --compare. Every one is registered in core.RefusalCodes
// so the CLI hint table, the TS client union, and docs/troubleshooting.md
// carry the same vocabulary.
const (
	codeBadCompare        = "bad-compare"
	codeMissingCompare    = "missing-compare-bundle"
	codeUnsafeCompare     = "unsafe-compare-bundle"
	codeCorruptCompare    = "corrupt-compare-bundle"
	codeUnrecordedCompare = "unrecorded-compare-bundle"
)

// Compare bucket names: the four labels the human block prints.
const (
	bucketNew         = "new"
	bucketPersisting  = "persisting"
	bucketResolved    = "resolved"
	bucketNotReviewed = "not reviewed"
)

// Compare reasons are the typed rules that place a row. A missing finding
// reaches resolved only through covered-and-absent or removed-from-diff;
// every weaker proof names its own not-reviewed reason, so no absent finding
// can read resolved on an incremental, excluded, unattested, or partial run.
const (
	reasonNotInBaseline = "not-in-baseline"
	reasonInBoth        = "in-both"
	reasonCoveredAbsent = "covered-and-absent"
	reasonRemovedFrom   = "removed-from-diff"
	reasonUncoveredHunk = "uncovered-hunk"
	reasonExcluded      = "excluded"
	reasonNotInDelta    = "not-in-incremental-diff"
	reasonNoLocation    = "no-location"
	reasonPartialReview = "partial-review"
)

// Bundle files the comparison reads, relative to a bundle directory. They are
// relative names so every read joins them under one verified, physical
// directory.
const (
	compareFindingsFile  = "findings.json"
	compareReviewFile    = "review.json"
	compareExcludedFile  = "excluded.json"
	compareIncremental   = "incremental.diff"
	compareRecordFile    = "record.json"
	comparePostFile      = "post.json"
	compareManifestFile  = "chunks/manifest.json"
	compareChunksDir     = "chunks"
	compareFullDiffFile  = "full.diff"
	comparePercentFull   = 100
	compareManifestKey   = "chunks"
	compareChunkDiffExt  = ".diff"
	comparePRFile        = "PR.json"
	comparePathSeparator = "/"
)

// compareScope is what the reported bundle proves about the review that
// produced it. readDir is the bundle directory with OS ancestors resolved, so
// confined reads work under a macOS /var or /tmp ancestor symlink; reviewed is
// the hunk set the reviewer actually saw (the incremental delta when the run
// was incremental, the full diff otherwise); scopeKnown says the bundle
// carries enough evidence to prove what that set was.
type compareScope struct {
	readDir       string
	reviewed      map[string][]hunks.Hunk
	excluded      map[string]bool
	att           attestation
	providerPR    bool
	incremental   bool
	chunked       bool
	scopeKnown    bool
	excludedKnown bool
}

// repoIdentity is one bundle's repository claim: the project path a local
// source.json or a review URL names, and the host only a review URL names.
type repoIdentity struct {
	project string
	host    string
}

// loadCompare reads the baseline bundle, classifies every signature, and
// returns the typed comparison. It reads local files only.
func loadCompare(st *state) *core.RefusalError {
	baselineDir, ref := resolveCompareDir(st, st.flags.Compare)
	if ref != nil {
		return ref
	}
	if sameFile(st.dir, baselineDir) {
		return &core.RefusalError{
			Code: codeBadCompare,
			Message: fmt.Sprintf("report: --compare %s names the reported bundle itself; pass a different baseline",
				st.flags.Compare),
		}
	}
	baseline, ref := readBaseline(st, baselineDir)
	if ref != nil {
		return ref
	}
	target := readCompareScope(st)
	if ref := repoMismatch(st.flags.Compare, compareRepo(baselineDir, ""), compareRepo(st.dir, st.req.Repo)); ref != nil {
		return ref
	}
	report := classifyCompare(st, target, baseline)
	report.Baseline.Dir = displayCompareDir(st, baselineDir)
	report.Baseline.Name = readcommon.BundleName(baselineDir)
	report.Baseline.HeadSHA = nullableText(baseline.head)
	report.Baseline.BaseSHA = nullableText(baseline.base)
	report.Baseline.RecordedAt = nullableText(baseline.recordedAt)
	st.compare = report
	return nil
}

// compareRepo reads one bundle's repository identity. A PR bundle without a
// URL falls back to the reported positional for the target only, so a real
// fetched PR and a local bundle in the same repository still match.
func compareRepo(dir, fallback string) repoIdentity {
	project := readcommon.BundleProject(dir)
	if project == "" {
		project = fallback
	}
	return repoIdentity{project: strings.Trim(project, comparePathSeparator), host: readcommon.BundleHost(dir)}
}

// repoMismatch refuses a comparison across repositories or across hosts. The
// project must match exactly (a case-folded match could conflate two GitLab
// projects); the host is only compared when both bundles carry a review URL,
// because a local bundle names no host.
func repoMismatch(want string, baseline, target repoIdentity) *core.RefusalError {
	switch {
	case baseline.project != "" && target.project != "" && baseline.project != target.project:
		return &core.RefusalError{
			Code: codeBadCompare,
			Message: fmt.Sprintf("report: --compare %s reviews %s, not the reported bundle's %s",
				want, baseline.project, target.project),
		}
	case baseline.host != "" && target.host != "" && baseline.host != target.host:
		return &core.RefusalError{
			Code: codeBadCompare,
			Message: fmt.Sprintf("report: --compare %s is a %s review, not the reported bundle's %s review",
				want, baseline.host, target.host),
		}
	default:
		return nil
	}
}

// resolveCompareDir owns the operator's resolution rules: a value naming an
// existing directory is a bundle path (in the working directory first, then
// under the repository root); an absolute path or a value carrying a
// separator is a path and refuses when missing; anything else is a bundle
// identifier in the same repository state directory, resolved by the same
// rules as the positional. Trailing separators are stripped before any Lstat,
// so `<symlink>/` cannot slip past the symlink refusal.
func resolveCompareDir(st *state, want string) (string, *core.RefusalError) {
	if strings.TrimSpace(want) == "" {
		return "", &core.RefusalError{Code: codeBadCompare, Message: "report: --compare names no baseline bundle"}
	}
	cleaned := filepath.Clean(want)
	// An existing directory or file wins whatever its spelling: a bare token
	// naming a bundle path is still a path, exactly like the positional.
	if found, state := findComparePath(st, cleaned); state != pathMissing {
		return checkedComparePath(st, want, found, state)
	}
	if looksLikePath(cleaned) {
		return "", missingCompare(want)
	}
	dir, ref := readcommon.ResolveBundleDir(st.req.RepoRootPath(), cleaned)
	if ref != nil {
		return "", &core.RefusalError{
			Code:    codeBadCompare,
			Message: fmt.Sprintf("report: --compare %s is not a bundle path or a valid bundle name: %s", want, ref.Message),
		}
	}
	return checkedCompareDir(st, want, dir)
}

// comparePathState is what one path candidate turned out to be.
type comparePathState int

const (
	pathMissing comparePathState = iota
	pathDir
	pathSymlink
	pathNonDir
)

// findComparePath resolves a path spelling: the working directory first, then
// the repository root for a relative value. A symlink or a non-directory
// anywhere refuses, so a shadowed name never falls through to another
// directory.
func findComparePath(st *state, cleaned string) (string, comparePathState) {
	candidates := []string{cleaned}
	if !filepath.IsAbs(cleaned) && st.req.RepoRootPath() != "" {
		candidates = append(candidates, filepath.Join(st.req.RepoRootPath(), cleaned))
	}
	var found string
	state := pathMissing
	for _, candidate := range candidates {
		found, state = mergeCompareCandidate(found, state, candidate)
	}
	return found, state
}

// mergeCompareCandidate folds one candidate into the resolution: a missing
// path changes nothing, a symlink always wins, a directory beats a
// non-directory or an earlier miss, and any other existing path refuses.
func mergeCompareCandidate(found string, state comparePathState, candidate string) (string, comparePathState) {
	info, err := os.Lstat(candidate)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return found, state
		}
		return firstNonDir(found, state, candidate)
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return candidate, pathSymlink
	case info.IsDir():
		if state == pathMissing || state == pathNonDir {
			return candidate, pathDir
		}
		return found, state
	default:
		return firstNonDir(found, state, candidate)
	}
}

// firstNonDir remembers an existing non-directory candidate unless a symlink
// or a directory already decided the answer.
func firstNonDir(found string, state comparePathState, candidate string) (string, comparePathState) {
	if state == pathMissing {
		return candidate, pathNonDir
	}
	return found, state
}

// checkedComparePath refuses a non-directory candidate and returns the
// verified directory for a real one.
func checkedComparePath(st *state, want, found string, state comparePathState) (string, *core.RefusalError) {
	switch state {
	case pathSymlink:
		return "", unsafeCompare(found, "is a symlink")
	case pathNonDir:
		return "", unsafeCompare(found, "is not a directory")
	case pathDir:
		return checkedCompareDir(st, want, found)
	case pathMissing:
		return "", missingCompare(want)
	}
	return "", missingCompare(want)
}

// checkedCompareDir refuses a resolved directory that is missing, symlinked,
// not a directory, or reached through a symlink inside the managed state home.
func checkedCompareDir(st *state, want, dir string) (string, *core.RefusalError) {
	info, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", missingCompare(want)
	case err != nil:
		return "", unsafeCompare(dir, "cannot be inspected")
	case info.Mode()&os.ModeSymlink != 0:
		return "", unsafeCompare(dir, "is a symlink")
	case !info.IsDir():
		return "", unsafeCompare(dir, "is not a directory")
	}
	if ref := managedSymlinkRefusal(st, dir); ref != nil {
		return "", ref
	}
	return dir, nil
}

// managedSymlinkRefusal refuses a directory under the repository's managed
// state home when any component of that home is a symlink, matching the gate
// every bundle write goes through.
func managedSymlinkRefusal(st *state, dir string) *core.RefusalError {
	root := st.req.RepoRootPath()
	rel, ok := managedBundleRel(root, dir)
	if !ok {
		return nil
	}
	if err := config.NewLayout(root).RefuseBundleSymlink(rel); err != nil {
		return unsafeCompare(dir, "is a managed bundle path carrying a symlink")
	}
	return nil
}

// managedBundleRel names dir relative to the root's managed state home when
// it is exactly one bundle directory inside it. Paths outside the home, and
// paths on another volume, carry no managed-bundle claim of their own.
func managedBundleRel(root, dir string) (string, bool) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	absState, err := filepath.Abs(filepath.FromSlash(readcommon.StateDir(root)))
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(absState, absDir)
	if err != nil {
		return "", false
	}
	switch {
	case rel == ".", strings.HasPrefix(rel, ".."),
		strings.ContainsRune(rel, filepath.Separator), strings.ContainsRune(rel, '/'):
		return "", false
	}
	return rel, true
}

// looksLikePath reports whether a value was meant as a path: a dot-relative
// spelling or one carrying a separator. A bare token is a bundle name.
func looksLikePath(value string) bool {
	return strings.HasPrefix(value, ".") || strings.ContainsAny(value, `/\`)
}

func missingCompare(want string) *core.RefusalError {
	return &core.RefusalError{
		Code: codeMissingCompare,
		Message: fmt.Sprintf("report: --compare %s names no bundle (pass a bundle directory, a PR, or a local-<name> under .verdict/state)",
			want),
	}
}

func unsafeCompare(what, why string) *core.RefusalError {
	return &core.RefusalError{
		Code:    codeUnsafeCompare,
		Message: fmt.Sprintf("report: comparing %s is refused: the path %s", what, why),
	}
}

func sameFile(a, b string) bool {
	ai, aerr := os.Stat(a)
	bi, berr := os.Stat(b)
	return aerr == nil && berr == nil && os.SameFile(ai, bi)
}

// baselineBundle is the comparison side: the identity-bearing findings, the
// recorded run metadata, and the repository key the cross-repo guard reads.
type baselineBundle struct {
	head       string
	base       string
	recordedAt string
	findings   []any
}

// readBaseline loads the comparison bundle fail-closed: a missing or
// non-directory path, a symlinked file, an unrecorded store, and a corrupt
// store each refuse typed instead of reading as an empty comparison. Reads go
// through the bundle directory with its OS ancestors resolved.
func readBaseline(st *state, dir string) (*baselineBundle, *core.RefusalError) {
	readDir := evalBundleDir(dir)
	raw, read := readCompareFile(readDir, compareFindingsFile)
	switch read {
	case fileOK:
		// Decoded below.
	case fileMissing:
		return nil, &core.RefusalError{
			Code:    codeUnrecordedCompare,
			Message: fmt.Sprintf("report: comparison bundle %s has no findings.json (record or post that bundle first)", dir),
		}
	case fileUnsafe:
		return nil, unsafeCompare(filepath.Join(dir, compareFindingsFile), "must be a regular file, not a symlink or device")
	case fileUnreadable:
		return nil, corruptCompare(dir, "findings.json is unreadable")
	}
	doc, err := jq.DecodeOrdered(raw)
	entries, isArray := doc.([]any)
	switch {
	case err != nil:
		return nil, corruptCompare(dir, "findings.json is not readable JSON")
	case !isArray:
		return nil, corruptCompare(dir, "findings.json is not a findings array")
	case !readcommon.ValidEntries(entries):
		return nil, corruptCompare(dir, "findings.json carries an invalid finding entry")
	}
	out := &baselineBundle{findings: entries}
	out.head, out.base = bundleHeads(readDir)
	out.recordedAt = recordedAt(readDir)
	out.warnUnkeyed(st)
	return out, nil
}

func corruptCompare(dir, why string) *core.RefusalError {
	return &core.RefusalError{
		Code: codeCorruptCompare,
		Message: fmt.Sprintf("report: comparison bundle %s is corrupt: %s "+
			"(re-run record/post on that bundle, or restore the file)", dir, why),
	}
}

// warnUnkeyed names signatures the comparison could not read, so a bucket
// count never silently omits a finding the stores carry.
func (b *baselineBundle) warnUnkeyed(st *state) {
	missing := 0
	for _, entry := range b.findings {
		if _, ok := entrySigString(entry); !ok {
			missing++
		}
	}
	if missing > 0 {
		st.warn(fmt.Sprintf("compare: %d baseline finding(s) carry no signature — not compared", missing))
	}
}

// recordedAt reads the bundle receipt's timestamp: record.json first, then
// post.json, empty when neither carries one.
func recordedAt(dir string) string {
	if value := receiptTimestamp(dir, compareRecordFile, "recorded_at"); value != "" {
		return value
	}
	return receiptTimestamp(dir, comparePostFile, "posted_at")
}

// receiptTimestamp reads one timestamp field off a bundle receipt.
func receiptTimestamp(dir, file, field string) string {
	raw, read := readCompareFile(dir, file)
	if read != fileOK {
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
	return jq.StrOf(m[field])
}

// fileState is the three-way answer every comparison-bundle read keeps apart:
// absent is a missing bundle part, unsafe is a symlink or non-regular file,
// and unreadable is a regular file the read could not return.
type fileState int

const (
	fileOK fileState = iota
	fileMissing
	fileUnsafe
	fileUnreadable
)

// evalBundleDir resolves OS ancestors (macOS /var -> /private/var, /tmp ->
// /private/tmp) on an already-verified bundle directory, so a confined read
// starts below the scheme symlink instead of refusing it. The bundle's own
// components are still verified by the read itself.
func evalBundleDir(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}

// displayCompareDir renders the baseline path for output: repository-relative
// when it lives under root, native separators normalized to slash form on
// every OS.
func displayCompareDir(st *state, dir string) string {
	shown := readcommon.DisplayPath(st.req.RepoRootPath(), dir)
	return filepath.ToSlash(filepath.Clean(shown))
}

// readCompareFile reads one capped file below dir without following a symlink
// or opening a device. It Lstats first because a missing part must stay
// distinguishable from an unsafe one, and it keeps
// [readcommon.ErrSymlinkPath] distinct so a symlinked path refuses as unsafe
// rather than reading as corrupt.
func readCompareFile(dir, name string) ([]byte, fileState) {
	path := filepath.Join(dir, filepath.FromSlash(name))
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, fileMissing
	case err != nil:
		return nil, fileUnreadable
	case info.Mode()&os.ModeSymlink != 0:
		return nil, fileUnsafe
	case !info.Mode().IsRegular():
		return nil, fileUnsafe
	}
	raw, err := readConfinedUnsealed(path, jq.MaxFindingsBytes)
	switch {
	case errors.Is(err, readcommon.ErrSymlinkPath):
		return nil, fileUnsafe
	case err != nil:
		return nil, fileUnreadable
	default:
		return raw, fileOK
	}
}

// readConfinedUnsealed is [readcommon.ReadConfined] with the symlink sentinel
// intact: the shared reader seals its cause at the boundary, and this caller
// must tell an unsafe path from an unreadable one.
func readConfinedUnsealed(name string, capBytes int64) ([]byte, error) {
	root, leaf, err := readcommon.OpenParentWith(name, os.OpenRoot)
	if err != nil {
		return nil, err //nolint:wrapcheck // the symlink sentinel must stay matchable
	}
	defer root.Close()
	file, err := readcommon.OpenRegularNoSymlinkIn(root, leaf)
	if err != nil {
		return nil, err //nolint:wrapcheck // the symlink sentinel must stay matchable
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, capBytes+1))
	if err != nil {
		return nil, err //nolint:wrapcheck // the read cause is mapped, not surfaced
	}
	if int64(len(raw)) > capBytes {
		return nil, jq.NewErr("file exceeds read cap")
	}
	return raw, nil
}

// readCompareScope reads what the reported bundle proves about its review.
// Any doubt about the reviewed diff or the exclusion receipt leaves
// scopeKnown false, and then no absent finding may read resolved.
func readCompareScope(st *state) *compareScope {
	scope := &compareScope{readDir: evalBundleDir(st.dir)}
	scope.providerPR = isRegular(filepath.Join(scope.readDir, comparePRFile))
	scope.chunked = isRegular(filepath.Join(scope.readDir, filepath.FromSlash(compareManifestFile)))
	scope.excluded, scope.excludedKnown = readExcludedPaths(st, scope.readDir)
	scope.att = loadAttestation(st, scope.readDir)
	scope.reviewed, scope.scopeKnown = reviewedHunks(st, scope)
	return scope
}

// readExcludedPaths reads the reported bundle's excluded.json. An absent
// receipt excludes nothing; an unreadable one proves nothing, so it reports
// known=false and every absent finding stays not-reviewed.
func readExcludedPaths(st *state, readDir string) (map[string]bool, bool) {
	raw, read := readCompareFile(readDir, compareExcludedFile)
	if read == fileMissing {
		return map[string]bool{}, true
	}
	receipt, ok := parseExcludedReceipt(raw, read == fileOK)
	if !ok {
		st.warn("compare: excluded.json unreadable — missing findings read not-reviewed")
		return map[string]bool{}, false
	}
	return excludedPathSet(receipt), true
}

// parseExcludedReceipt decodes the receipt, reporting false for an unreadable
// file or a receipt that does not name its schema.
func parseExcludedReceipt(raw []byte, readable bool) (*core.ExcludedFile, bool) {
	if !readable {
		return nil, false
	}
	var receipt core.ExcludedFile
	if json.Unmarshal(raw, &receipt) != nil || receipt.SchemaVersion != "1" {
		return nil, false
	}
	return &receipt, true
}

// excludedPathSet is every path and old path the receipt removed.
func excludedPathSet(receipt *core.ExcludedFile) map[string]bool {
	out := map[string]bool{}
	for _, row := range receipt.Excluded {
		if row.Path != "" {
			out[row.Path] = true
		}
		if row.OldPath != "" {
			out[row.OldPath] = true
		}
	}
	return out
}

// reviewedHunks resolves the hunk set the target's reviewer saw. An
// incremental run's reviewed set is its delta, never the full PR diff; a
// missing or unreadable diff snapshot leaves scopeKnown false.
func reviewedHunks(st *state, scope *compareScope) (map[string][]hunks.Hunk, bool) {
	if _, err := os.Lstat(filepath.Join(scope.readDir, compareIncremental)); err == nil {
		raw, state := readCompareFile(scope.readDir, compareIncremental)
		if state != fileOK {
			st.warn("compare: incremental.diff unreadable — missing findings read not-reviewed")
			return nil, false
		}
		scope.incremental = true
		return indexHunks(hunks.Parse(string(raw))), true
	}
	rows, err := hunks.Load(scope.readDir)
	if err == nil && rows != nil {
		return indexHunks(rows), true
	}
	raw, state := readCompareFile(scope.readDir, compareFullDiffFile)
	if state != fileOK {
		st.warn("compare: reviewed diff snapshot unavailable — missing findings read not-reviewed")
		return nil, false
	}
	return indexHunks(hunks.Parse(string(raw))), true
}

func indexHunks(rows []hunks.Hunk) map[string][]hunks.Hunk {
	out := map[string][]hunks.Hunk{}
	for _, row := range rows {
		out[row.File] = append(out[row.File], row)
	}
	return out
}

// attestation is the reported bundle's coverage claim: the reviewed line
// ranges per file, the whole-file claims, the claimed chunk hunks, and the
// stored receipt that proves every scored hunk was attested.
type attestation struct {
	ranges   map[string][]coverage.Range
	whole    map[string]bool
	chunks   map[string][]hunks.Hunk
	complete bool
	// exempt is true when the complete receipt skipped generated files, so
	// `complete` proves nothing about those paths.
	exempt bool
}

// covers reports whether the reviewer attested the given file line and side.
// A line-less finding needs a whole-file claim or a complete receipt; a
// generated path needs a direct claim because the score skips it.
func (a *attestation) covers(path string, line int, side any) bool {
	if a.whole[path] {
		return true
	}
	if line > 0 && (inRanges(a.ranges[path], line) || lineInHunks(a.chunks[path], side, line)) {
		return true
	}
	if !a.complete {
		return false
	}
	return !a.exempt || !readcommon.Generated(path)
}

// inRanges reports whether line sits in one attested coverage range.
func inRanges(ranges []coverage.Range, line int) bool {
	for _, r := range ranges {
		if r.Start <= line && line <= r.End {
			return true
		}
	}
	return false
}

// loadAttestation reads the reported bundle's attestation. review.json is the
// authority: when it is absent, a stored receipt for this bundle's current
// head may stand in; when it is present but unusable, nothing does. A chunked
// bundle ignores coverage[] and reads only its claimed chunks, matching the
// evaluator.
func loadAttestation(st *state, readDir string) attestation {
	att := attestation{ranges: map[string][]coverage.Range{}, whole: map[string]bool{}, chunks: map[string][]hunks.Hunk{}}
	review, present := readCompareReview(st, readDir)
	if !present {
		att.readReceipt(st, readDir)
		return att
	}
	if review == nil {
		return att
	}
	if isRegular(filepath.Join(readDir, filepath.FromSlash(compareManifestFile))) {
		att.readChunks(readDir, review)
	} else {
		att.readCoverage(review)
	}
	return att
}

// readCompareReview reads the reported bundle's review.json. present reports
// whether the file exists at all: absent is the only state a stored receipt
// may stand in for, while a corrupt, unreadable, or symlinked file is present
// and attests nothing.
func readCompareReview(st *state, readDir string) (*jq.Obj, bool) {
	raw, read := readCompareFile(readDir, compareReviewFile)
	if read == fileMissing {
		return nil, false
	}
	if read != fileOK {
		st.warn("compare: review.json unreadable — coverage attestation unavailable; missing findings read not-reviewed")
		return nil, true
	}
	doc, err := jq.DecodeOrdered(raw)
	review, ok := doc.(*jq.Obj)
	if err != nil || !ok {
		st.warn("compare: review.json unreadable — coverage attestation unavailable; missing findings read not-reviewed")
		return nil, true
	}
	return review, true
}

// readCoverage folds review.json's coverage[] entries into per-file ranges.
func (a *attestation) readCoverage(review *jq.Obj) {
	items, ok := fieldValue(review, coverage.Field).([]any)
	if !ok {
		return
	}
	for _, item := range items {
		a.readCoverageEntry(item)
	}
}

// readCoverageEntry folds one coverage[] entry into the attestation. An
// omitted ranges key is a whole-file claim (the evaluator's rule); an explicit
// null or any other malformed value attests nothing.
func (a *attestation) readCoverageEntry(item any) {
	row, ok := item.(*jq.Obj)
	if !ok || row == nil {
		return
	}
	file := strings.TrimSpace(jq.StrOf(fieldValue(row, coverage.FieldFile)))
	if file == "" {
		return
	}
	raw, present := row.Get(coverage.FieldRanges)
	ranges, whole, ok := coverageClaim(raw, present)
	if !ok {
		return
	}
	if whole {
		a.whole[file] = true
		return
	}
	a.ranges[file] = append(a.ranges[file], ranges...)
}

// fieldValue reads one key off a decoded object.
func fieldValue(obj *jq.Obj, key string) any {
	value, _ := obj.Get(key)
	return value
}

// coverageClaim parses one claim's ranges field: an omitted key, an empty
// string, or "all" is a whole-file claim; a valid range list is that list; an
// explicit null or anything malformed attests nothing.
func coverageClaim(raw any, present bool) ([]coverage.Range, bool, bool) {
	if !present {
		return nil, true, true
	}
	text, isString := raw.(string)
	if !isString {
		return nil, false, false
	}
	text = strings.TrimSpace(text)
	if text == "" || text == coverage.RangesAll {
		return nil, true, true
	}
	ranges, err := coverage.ParseRanges(text)
	if err != nil {
		return nil, false, false
	}
	return ranges, false, true
}

// readChunks folds the chunked bundle's claimed chunks into reviewed hunks:
// each claimed chunk attests exactly the hunks its own diff carries.
func (a *attestation) readChunks(readDir string, review *jq.Obj) {
	claimed := claimedChunkNames(review)
	if len(claimed) == 0 {
		return
	}
	raw, state := readCompareFile(readDir, compareManifestFile)
	if state != fileOK {
		return
	}
	doc, err := jq.Decode(raw)
	man, ok := jq.AsMap(doc)
	if err != nil || !ok {
		return
	}
	for _, item := range jq.Elements(man[compareManifestKey]) {
		for _, row := range claimedChunkRows(readDir, item, claimed) {
			a.chunks[row.File] = append(a.chunks[row.File], row)
		}
	}
}

// claimedChunkRows reads one manifest chunk's diff when the review claimed the
// chunk, returning the hunks it attests. The diff path is derived from the
// chunk name under the bundle's own chunks/ directory, so a manifest can never
// point the read outside the bundle.
func claimedChunkRows(readDir string, item any, claimed map[string]bool) []hunks.Hunk {
	chunk, ok := jq.AsMap(item)
	if !ok {
		return nil
	}
	name := jq.StrOf(chunk["name"])
	if name == "" || !claimed[name] || filepath.Base(name) != name {
		return nil
	}
	body, state := readCompareFile(readDir, compareChunksDir+"/"+name+compareChunkDiffExt)
	if state != fileOK {
		return nil
	}
	return hunks.Parse(string(body))
}

// claimedChunkNames reads review.json's chunks[] claim set.
func claimedChunkNames(review *jq.Obj) map[string]bool {
	items, ok := fieldValue(review, coverage.FieldChunks).([]any)
	if !ok {
		return nil
	}
	out := map[string]bool{}
	for _, item := range items {
		if name, ok := item.(string); ok && name != "" {
			out[name] = true
		}
	}
	return out
}

// readReceipt folds the stored receipt for this bundle's current head into the
// attestation. review.json is absent here, so the receipt is the only claim;
// it must name the head (or worktree diff) the bundle now carries, match the
// bundle's chunked or line mode, and prove every scored hunk was attested.
func (a *attestation) readReceipt(st *state, readDir string) {
	cov := readStoredCoverage(st.dir, coverageIdentity(st))
	if isRegular(filepath.Join(readDir, filepath.FromSlash(compareManifestFile))) {
		if cov.chunks != nil && cov.chunks.Total > 0 && cov.chunks.Attested == cov.chunks.Total {
			a.complete = true
		}
		return
	}
	sum := cov.sum
	if sum == nil || sum.Percent == nil || *sum.Percent != comparePercentFull ||
		sum.Total <= 0 || sum.Gaps != 0 {
		return
	}
	a.complete = true
	a.exempt = sum.Exempt > 0
}

// classifyCompare places every signature in one of the four buckets.
func classifyCompare(st *state, target *compareScope, baseline *baselineBundle) *core.ReportCompare {
	out := &core.ReportCompare{
		New: []core.CompareFinding{}, Persisting: []core.CompareFinding{},
		Resolved: []core.CompareFinding{}, NotReviewed: []core.CompareFinding{},
	}
	current := identityFindings(st.entries)
	previous := identityFindings(baseline.findings)
	collectPresent(out, current, previous)
	collectAbsent(out, target, current, previous)
	out.Baseline.Findings = len(previous)
	out.Counts = core.CompareCounts{
		New: len(out.New), Persisting: len(out.Persisting),
		Resolved: len(out.Resolved), NotReviewed: len(out.NotReviewed),
	}
	sortCompareRows(out.New)
	sortCompareRows(out.Persisting)
	sortCompareRows(out.Resolved)
	sortCompareRows(out.NotReviewed)
	return out
}

// collectPresent fills new and persisting from the reported store.
func collectPresent(out *core.ReportCompare, current, previous map[string]any) {
	for sig, entry := range current {
		if base, ok := previous[sig]; ok {
			out.Persisting = append(out.Persisting, persistingRow(sig, entry, base))
			continue
		}
		out.New = append(out.New, currentRow(sig, entry, reasonNotInBaseline))
	}
}

// collectAbsent fills resolved and not-reviewed from the baseline-only
// signatures.
func collectAbsent(out *core.ReportCompare, target *compareScope, current, previous map[string]any) {
	for sig, entry := range previous {
		if _, ok := current[sig]; ok {
			continue
		}
		resolved, row := absentRow(target, sig, entry)
		if resolved {
			out.Resolved = append(out.Resolved, row)
			continue
		}
		out.NotReviewed = append(out.NotReviewed, row)
	}
}

// identityFindings indexes the signature-bearing entries in store order. The
// first entry wins a duplicate signature, which keeps the read deterministic
// without refusing a store report already displays.
func identityFindings(entries []any) map[string]any {
	out := make(map[string]any, len(entries))
	for _, entry := range entries {
		sig, ok := entrySigString(entry)
		if !ok {
			continue
		}
		if _, seen := out[sig]; seen {
			continue
		}
		out[sig] = entry
	}
	return out
}

// entrySigString reads an entry's full signature.
func entrySigString(entry any) (string, bool) {
	sig, ok := readcommon.FieldOf(entry, "sig").(string)
	return sig, ok && sig != ""
}

// currentRow renders a new or persisting finding from the reported store.
func currentRow(sig string, entry any, reason string) core.CompareFinding {
	return core.CompareFinding{
		Sig:      sig,
		Status:   readcommon.StatusOf(entry),
		Severity: nullableField(entry, "severity"),
		Path:     nullableField(entry, "path"),
		Line:     lineField(entry),
		Claim:    claimOf(entry),
		Reason:   reason,
	}
}

// persistingRow renders a signature present in both stores, naming the
// severity and status moves in a fixed order.
func persistingRow(sig string, current, previous any) core.CompareFinding {
	row := currentRow(sig, current, reasonInBoth)
	row.Changes = changeNotes(previous, current)
	return row
}

// absentRow classifies a baseline-only finding: resolved only when the
// reported bundle proves the reviewer saw the location and the bug is gone,
// otherwise not-reviewed with the typed reason that withheld the proof.
func absentRow(scope *compareScope, sig string, entry any) (bool, core.CompareFinding) {
	row := currentRow(sig, entry, "")
	resolved, reason := absentOutcome(scope, &row, entry)
	row.Reason = reason
	return resolved, row
}

// absentOutcome applies the proof rules in order, returning the resolved
// verdict and the reason that decided it.
func absentOutcome(scope *compareScope, row *core.CompareFinding, entry any) (bool, string) {
	path := pointerText(row.Path)
	if path == "" {
		return false, reasonNoLocation
	}
	if scope.excluded[path] {
		return false, reasonExcluded
	}
	if !scope.scopeKnown || !scope.excludedKnown {
		return false, reasonPartialReview
	}
	fileReviewed := len(scope.reviewed[path]) > 0
	switch {
	case scope.incremental && !fileReviewed:
		return false, reasonNotInDelta
	case !fileReviewed && !scope.providerPR:
		// A local bundle's diff can be path-filtered or range-limited by
		// construction, so a path missing from it proves nothing.
		return false, reasonPartialReview
	case !fileReviewed:
		return true, reasonRemovedFrom
	}
	return coveredOutcome(scope, row, entry, path)
}

// coveredOutcome decides the reviewed-file branches. A line-less finding needs
// a whole-file or complete attestation and can never read removed-from-diff
// while its file is still in the diff. An anchor outside every reviewed hunk
// is the reverted-change proof only on a full provider PR diff; under an
// incremental delta it means the delta did not touch that line, and a local
// bundle's narrowed diff proves nothing either way.
func coveredOutcome(scope *compareScope, row *core.CompareFinding, entry any, path string) (bool, string) {
	side := readcommon.FieldOf(entry, "side")
	line := lineValue(row.Line)
	if line <= 0 {
		if scope.att.covers(path, 0, side) {
			return true, reasonCoveredAbsent
		}
		return false, reasonUncoveredHunk
	}
	if !lineInHunks(scope.reviewed[path], side, line) {
		switch {
		case scope.incremental:
			return false, reasonNotInDelta
		case scope.providerPR:
			return true, reasonRemovedFrom
		default:
			return false, reasonPartialReview
		}
	}
	if scope.att.covers(path, line, side) {
		return true, reasonCoveredAbsent
	}
	return false, reasonUncoveredHunk
}

// lineInHunks reports whether the finding's own side/time anchor sits inside
// one reviewed hunk. A line-less finding never matches.
func lineInHunks(rows []hunks.Hunk, side any, line int) bool {
	if line <= 0 {
		return false
	}
	name, ok := side.(string)
	if !ok {
		name = ""
	}
	for _, row := range rows {
		if row.Start <= line && line <= row.End && sideMatches(row.Side, name) {
			return true
		}
	}
	return false
}

// sideMatches keeps a LEFT/OLD anchor out of NEW hunks and vice versa; an
// absent or unknown side accepts either.
func sideMatches(hunkSide, findingSide string) bool {
	switch strings.ToUpper(findingSide) {
	case "LEFT", "OLD":
		return hunkSide == "OLD"
	case "RIGHT", "NEW":
		return hunkSide == "NEW"
	default:
		return true
	}
}

// changeNotes names the persisting finding's severity then status move.
func changeNotes(previous, current any) []string {
	var out []string
	from, to := nullableField(previous, "severity"), nullableField(current, "severity")
	if compareText(from) != compareText(to) {
		out = append(out, "severity: "+compareText(from)+"→"+compareText(to))
	}
	if fromStatus, toStatus := readcommon.StatusOf(previous), readcommon.StatusOf(current); fromStatus != toStatus {
		out = append(out, "status: "+compareText(nullableText(fromStatus))+"→"+compareText(nullableText(toStatus)))
	}
	return out
}

func compareText(value *string) string {
	if value == nil || *value == "" {
		return "none"
	}
	return *value
}

// claimOf reads the finding's claim: the stored body, else its heading.
func claimOf(entry any) string {
	if body := fieldText(entry, "body"); body != "" {
		return body
	}
	return fieldText(entry, "heading")
}

func fieldText(entry any, key string) string {
	value, ok := readcommon.FieldOf(entry, key).(string)
	if !ok {
		return ""
	}
	return value
}

// nullableField renders one nullable string field.
func nullableField(entry any, key string) *string {
	value := fieldText(entry, key)
	if value == "" {
		return nil
	}
	return &value
}

func nullableText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// lineField reads the finding's line as a positive integer, nil when it
// carries none.
func lineField(entry any) *int {
	value, ok := readcommon.NumVal(readcommon.FieldOf(entry, "line"))
	if !ok || value <= 0 {
		return nil
	}
	line := int(value)
	return &line
}

// sortCompareRows orders rows by path, then line, then sig: deterministic
// across map iteration order and stable across runs.
func sortCompareRows(rows []core.CompareFinding) {
	slices.SortStableFunc(rows, func(a, b core.CompareFinding) int {
		if c := strings.Compare(pointerText(a.Path), pointerText(b.Path)); c != 0 {
			return c
		}
		if al, bl := lineValue(a.Line), lineValue(b.Line); al != bl {
			return al - bl
		}
		return strings.Compare(a.Sig, b.Sig)
	})
}

func lineValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func pointerText(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// compareHuman renders the four-bucket block: one summary line, then each
// non-empty bucket with one row per finding and its reason.
func compareHuman(st *state) []string {
	c := st.compare
	if c == nil {
		return nil
	}
	head := "unrecorded"
	if c.Baseline.HeadSHA != nil {
		head = readcommon.ShortSHA(*c.Baseline.HeadSHA)
	}
	lines := []string{fmt.Sprintf(
		"compare: %s @ %s — new=%d persisting=%d resolved=%d not-reviewed=%d",
		c.Baseline.Dir, head, c.Counts.New, c.Counts.Persisting, c.Counts.Resolved, c.Counts.NotReviewed)}
	for _, bucket := range []struct {
		name string
		rows []core.CompareFinding
	}{
		{bucketNew, c.New},
		{bucketPersisting, c.Persisting},
		{bucketResolved, c.Resolved},
		{bucketNotReviewed, c.NotReviewed},
	} {
		if len(bucket.rows) == 0 {
			continue
		}
		lines = append(lines, bucket.name+" ("+strconv.Itoa(len(bucket.rows))+")")
		for i := range bucket.rows {
			lines = append(lines, compareRowLine(&bucket.rows[i]))
		}
	}
	return lines
}

// compareRowLine renders one bucket row: locator, sig prefix, severity, the
// typed reason, any persisting move, and the claim on one line.
func compareRowLine(row *core.CompareFinding) string {
	locator := "?"
	if row.Path != nil {
		locator = *row.Path
		if row.Line != nil {
			locator += ":" + strconv.Itoa(*row.Line)
		}
	}
	line := "- [" + row.Status + "] " + locator + " (" + readcommon.Prefix(row.Sig) + ", " + compareText(row.Severity) + ") — " + row.Reason
	if len(row.Changes) > 0 {
		line += " — " + strings.Join(row.Changes, ", ")
	}
	if row.Claim != "" {
		line += " — " + strings.NewReplacer("\n", " ", "\r", " ").Replace(row.Claim)
	}
	return line
}
