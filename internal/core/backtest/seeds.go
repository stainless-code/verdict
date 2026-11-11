package backtest

import (
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
)

// Skipped reasons. Every one is a data rule the manual publishes; a skip is
// counted, never a refusal.
const (
	reasonMergeSeed     = "merge-seed"
	reasonVendor        = "vendor"
	reasonNodeModules   = "node_modules"
	reasonThirdParty    = "third_party"
	reasonGenerated     = "generated"
	reasonBinary        = "binary"
	reasonDocsTest      = "docs-test-only"
	reasonWideFix       = "wide-fix"
	reasonAddedOnly     = "added-only"
	reasonBlankLine     = "blank-line"
	reasonNoPR          = "no-pr"
	reasonUnaligned     = "unaligned"
	reasonAncestorSince = "ancestor-of-since"
	reasonLargePR       = "large-pr-diff"
)

// Reconstruction values name how the PR head was recovered from the merge
// topology.
const (
	reconMergeParent = "merge-parent"
	reconSquash      = "squash-commit"
	reconFirstParent = "first-parent-merge"
)

// Seed rules. The subject rule accepts `fix:`, `fix(scope):`, and `fix!:`
// forms; the body rule accepts the closing keyword on a body line; the revert
// rule accepts `Revert "..."` but not a revert of a revert.
var (
	seedSubjectRe  = regexp.MustCompile(`(?i)^(fix|bugfix|hotfix)(\(|:|!)`)
	seedBodyRe     = regexp.MustCompile(`(?i)\b(fixes|closes|resolves) #[0-9]+`)
	squashNumberRe = regexp.MustCompile(`\(#([0-9]+)\)\s*$`)
	mergeNumberRe  = regexp.MustCompile(`^Merge pull request #([0-9]+)`)
)

// seedState accumulates candidates and skips across one window.
type seedState struct {
	st       *state
	since    Resolution
	until    Resolution
	units    map[string]prUnit
	prCaps   map[string]bool
	builders map[string]*candidateBuilder
	skipped  []Skipped
}

// candidateBuilder accumulates one PR's fixes and anchored bugs.
type candidateBuilder struct {
	unit  prUnit
	fixes []string
	bugs  []Bug
}

// prUnit is one PR recovered from an introducer commit. Skip names the reason
// this unit is not a candidate (empty when it is).
type prUnit struct {
	head           string
	base           string
	landed         string
	reconstruction string
	headAt         string
	mergedAt       string
	number         *int
	skip           string
}

// isSeed reports whether one commit is a seed by subject, body, or revert.
func isSeed(subject, body string) bool {
	if seedSubjectRe.MatchString(subject) {
		return true
	}
	if strings.HasPrefix(subject, `Revert "`) && !strings.HasPrefix(subject, `Revert "Revert`) {
		return true
	}
	return seedBodyRe.MatchString(body)
}

// consider checks one commit in the window: a seed is mined, a merge seed is
// counted, everything else is ignored.
func (ss *seedState) consider(ctx context.Context, c commit) *core.RefusalError {
	subject, body, ref := ss.st.subjectBody(ctx, c.sha)
	if ref != nil {
		return ref
	}
	if !isSeed(subject, body) {
		return nil
	}
	if len(c.parents) >= 2 {
		ss.skip(Skipped{Reason: reasonMergeSeed, Fix: c.sha})
		return nil
	}
	return ss.processFix(ctx, c.sha, subject)
}

// skip appends one skipped entry.
func (ss *seedState) skip(skip Skipped) { ss.skipped = append(ss.skipped, skip) }

// processFix mines one non-merge seed fix.
func (ss *seedState) processFix(ctx context.Context, fix, subject string) *core.RefusalError {
	stats, truncated, ref := ss.fixStats(ctx, fix)
	if ref != nil {
		return ref
	}
	if truncated {
		ss.skip(Skipped{Reason: reasonWideFix, Fix: fix, Detail: "diff exceeded the capture cap"})
		return nil
	}
	if reason := statExclusion(stats); reason != "" {
		ss.skip(Skipped{Reason: reason, Fix: fix})
		return nil
	}
	diff, truncated, ref := ss.fixDiff(ctx, fix)
	if ref != nil {
		return ref
	}
	if truncated {
		ss.skip(Skipped{Reason: reasonWideFix, Fix: fix, Detail: "diff exceeded the capture cap"})
		return nil
	}
	if diff.Binary {
		ss.skip(Skipped{Reason: reasonBinary, Fix: fix})
		return nil
	}
	if len(diff.Deleted) == 0 {
		ss.skip(Skipped{Reason: reasonAddedOnly, Fix: fix})
		return nil
	}
	kind := classifyKind(subject, diffTexts(diff.Deleted), diff.Added)
	return ss.anchorFix(ctx, fix, kind, ss.eligibleLines(fix, diff.Deleted))
}

// anchorFix anchors each eligible deleted line; a line that does not anchor
// records its own skipped row. The context is checked per line so a cancel
// stops between blames instead of after the next git failure.
func (ss *seedState) anchorFix(ctx context.Context, fix, kind string, lines []diffLine) *core.RefusalError {
	for _, line := range lines {
		if ref := ss.st.canceled(ctx); ref != nil {
			return ref
		}
		if ref := ss.anchorLine(ctx, fix, kind, line); ref != nil {
			return ref
		}
	}
	return nil
}

// anchorLine blames one deleted line, maps the introducer to a PR, and anchors
// the text at the PR head. A line that fails at any step records one skipped
// row naming that step's reason and the line's own position.
func (ss *seedState) anchorLine(ctx context.Context, fix, kind string, line diffLine) *core.RefusalError {
	introducer, ok, ref := ss.st.blame(ctx, fix+"^", line.Path, line.Line)
	if ref != nil {
		return ref
	}
	if !ok {
		ss.skipLine(reasonUnaligned, fix, "", line)
		return nil
	}
	unit, ref := ss.prFor(ctx, introducer)
	if ref != nil {
		return ref
	}
	if unit.skip != "" {
		ss.skipLine(unit.skip, fix, unit.head, line)
		return nil
	}
	at, ok, ref := ss.st.anchor(ctx, &unit, line, introducer)
	if ref != nil {
		return ref
	}
	if !ok {
		ss.skipLine(reasonUnaligned, fix, unit.head, line)
		return nil
	}
	ss.add(&unit, fix, Bug{Path: line.Path, Line: at, Kind: kind, Fix: fix})
	return nil
}

// skipLine records one dropped deleted line: its reason, its fix, the PR it
// resolved to when it got that far, and its path:line in the fix's parent.
func (ss *seedState) skipLine(reason, fix, pr string, line diffLine) {
	ss.skip(Skipped{Reason: reason, Fix: fix, PR: pr, Detail: line.Path + colonSep + strconv.Itoa(line.Line)})
}

// eligibleLines keeps the deleted lines that can anchor and records one
// skipped row for every line it drops.
func (ss *seedState) eligibleLines(fix string, deleted []diffLine) []diffLine {
	eligible := make([]diffLine, 0, len(deleted))
	for _, line := range deleted {
		if reason := lineReason(line); reason != "" {
			ss.skipLine(reason, fix, "", line)
			continue
		}
		eligible = append(eligible, line)
	}
	return eligible
}

// lineReason names why one deleted line cannot anchor, or "" when it can.
func lineReason(line diffLine) string {
	if class := pathClass(line.Path); class != "" {
		return class
	}
	if blankLine(line.Text) {
		return reasonBlankLine
	}
	return ""
}

// diffTexts returns the deleted line texts for kind classification.
func diffTexts(lines []diffLine) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, line.Text)
	}
	return out
}

// add records one anchored bug under its PR.
func (ss *seedState) add(unit *prUnit, fix string, bug Bug) {
	builder := ss.builders[unit.head]
	if builder == nil {
		builder = &candidateBuilder{unit: *unit}
		ss.builders[unit.head] = builder
	}
	if !slices.Contains(builder.fixes, fix) {
		builder.fixes = append(builder.fixes, fix)
	}
	for _, existing := range builder.bugs {
		if existing.Path == bug.Path && existing.Line == bug.Line {
			return
		}
	}
	builder.bugs = append(builder.bugs, bug)
}

// finish renders the accumulated candidates and skips in their published
// order: candidates by merge date then head, bugs by path then line.
func (ss *seedState) finish() ([]Candidate, []Skipped) {
	candidates := make([]Candidate, 0, len(ss.builders))
	for _, builder := range ss.builders {
		candidates = append(candidates, builder.candidate())
	}
	slices.SortFunc(candidates, compareCandidates)
	slices.SortFunc(ss.skipped, compareSkipped)
	if ss.skipped == nil {
		ss.skipped = []Skipped{}
	}
	return candidates, ss.skipped
}

// compareCandidates publishes candidates by merge date, then head.
//
//nolint:gocritic // the sort comparator takes value rows
func compareCandidates(a, b Candidate) int {
	if byDate := strings.Compare(a.MergedAt, b.MergedAt); byDate != 0 {
		return byDate
	}
	return strings.Compare(a.Head, b.Head)
}

// compareSkipped publishes skips by reason, fix, PR, then position.
func compareSkipped(a, b Skipped) int {
	if byReason := strings.Compare(a.Reason, b.Reason); byReason != 0 {
		return byReason
	}
	if byFix := strings.Compare(a.Fix, b.Fix); byFix != 0 {
		return byFix
	}
	if byPR := strings.Compare(a.PR, b.PR); byPR != 0 {
		return byPR
	}
	return comparePosition(a.Detail, b.Detail)
}

// comparePosition orders `path:line` details by path, then by line number as
// a number, so line 9 publishes before line 10.
func comparePosition(a, b string) int {
	pathA, lineA := splitPosition(a)
	pathB, lineB := splitPosition(b)
	if byPath := strings.Compare(pathA, pathB); byPath != 0 {
		return byPath
	}
	return lineA - lineB
}

// splitPosition splits a `path:line` detail; a detail with no line reads as
// line zero.
func splitPosition(detail string) (string, int) {
	at := strings.LastIndex(detail, colonSep)
	if at < 0 {
		return detail, 0
	}
	line, err := strconv.Atoi(detail[at+1:])
	if err != nil {
		return detail, 0
	}
	return detail[:at], line
}

// candidate renders one builder as a published candidate.
func (b *candidateBuilder) candidate() Candidate {
	fixes := slices.Clone(b.fixes)
	slices.Sort(fixes)
	bugs := slices.Clone(b.bugs)
	slices.SortFunc(bugs, func(a, b Bug) int {
		if byPath := strings.Compare(a.Path, b.Path); byPath != 0 {
			return byPath
		}
		return a.Line - b.Line
	})
	return Candidate{
		Head: b.unit.head, Base: b.unit.base, LandedBy: b.unit.landed, PR: b.unit.number,
		Reconstruction: b.unit.reconstruction, HeadSource: headSourceMerge,
		MergedAt: b.unit.mergedAt, HeadAt: b.unit.headAt, Fixes: fixes, Bugs: bugs,
	}
}

// fileStat is one file's numstat row.
type fileStat struct {
	Path    string
	Added   int
	Deleted int
	Binary  bool
}

// fixStats reads a fix's per-file line counts.
func (ss *seedState) fixStats(ctx context.Context, fix string) ([]fileStat, bool, *core.RefusalError) {
	out, exit, stderr, truncated, err := ss.st.gitCapped(ctx, commandOutputCap, ss.st.root,
		diffArgs("show", "--numstat", "--format=", "--no-renames", "-z", fix)...)
	if err != nil {
		return nil, false, ss.st.gitFailure(ctx, err.Error())
	}
	if exit != 0 {
		return nil, false, ss.st.gitFailure(ctx, "git show failed for "+short(fix)+": "+firstLine(stderr))
	}
	if truncated {
		return nil, true, nil
	}
	return parseNumstat(out), false, nil
}

// parseNumstat parses `git show --numstat -z` records.
func parseNumstat(out string) []fileStat {
	stats := make([]fileStat, 0)
	for record := range strings.SplitSeq(out, "\x00") {
		if stat, ok := parseStatRecord(record); ok {
			stats = append(stats, stat)
		}
	}
	return stats
}

// parseStatRecord parses one NUL-terminated numstat record.
func parseStatRecord(record string) (fileStat, bool) {
	fields := strings.SplitN(record, "\t", numStatFields)
	if len(fields) != numStatFields || fields[2] == "" {
		return fileStat{}, false
	}
	stat := fileStat{Path: fields[2]}
	if fields[0] == "-" || fields[1] == "-" {
		stat.Binary = true
		return stat, true
	}
	added, errAdd := strconv.Atoi(fields[0])
	deleted, errDel := strconv.Atoi(fields[1])
	if errAdd != nil || errDel != nil {
		return fileStat{}, false
	}
	stat.Added, stat.Deleted = added, deleted
	return stat, true
}

// statExclusion applies the whole-fix exclusions in their published order:
// binary, wide, then documentation/test-only.
func statExclusion(stats []fileStat) string {
	paths := make([]string, 0, len(stats))
	lines := 0
	for i := range stats {
		if stats[i].Binary {
			return reasonBinary
		}
		paths = append(paths, stats[i].Path)
		lines += stats[i].Added + stats[i].Deleted
	}
	if len(stats) > maxFixFiles || lines > maxFixLines {
		return reasonWideFix
	}
	if docsTestOnly(paths) {
		return reasonDocsTest
	}
	return ""
}

// fixDiff reads and parses a fix's deleted and added lines.
func (ss *seedState) fixDiff(ctx context.Context, fix string) (fixDiff, bool, *core.RefusalError) {
	out, exit, stderr, truncated, err := ss.st.gitCapped(ctx, commandOutputCap, ss.st.root,
		diffArgs("show", "-U0", "--no-renames", "--format=", fix)...)
	if err != nil {
		return fixDiff{}, false, ss.st.gitFailure(ctx, err.Error())
	}
	if exit != 0 {
		return fixDiff{}, false, ss.st.gitFailure(ctx, "git show failed for "+short(fix)+": "+firstLine(stderr))
	}
	if truncated {
		return fixDiff{}, true, nil
	}
	return parseFixHunks(out), false, nil
}

// prFor resolves one introducer to a PR unit, cached by introducer.
func (ss *seedState) prFor(ctx context.Context, introducer string) (prUnit, *core.RefusalError) {
	if unit, ok := ss.units[introducer]; ok {
		return unit, nil
	}
	unit, ref := ss.resolvePR(ctx, introducer)
	if ref != nil {
		return prUnit{}, ref
	}
	ss.units[introducer] = unit
	return unit, nil
}

// resolvePR reconstructs the PR that introduced one commit.
func (ss *seedState) resolvePR(ctx context.Context, introducer string) (prUnit, *core.RefusalError) {
	parents, ref := ss.st.parents(ctx, introducer)
	if ref != nil {
		return prUnit{}, ref
	}
	subject, _, ref := ss.st.subjectBody(ctx, introducer)
	if ref != nil {
		return prUnit{}, ref
	}
	unit, ref := ss.prShape(ctx, introducer, subject, parents)
	if ref != nil || unit.skip != "" {
		return unit, ref
	}
	if ref := ss.prChecks(ctx, &unit); ref != nil {
		return prUnit{}, ref
	}
	return unit, nil
}

// prShape picks the reconstruction rule: merge parent, squash subject, or the
// first first-parent merge that carried the commit.
func (ss *seedState) prShape(
	ctx context.Context, introducer, subject string, parents []string,
) (prUnit, *core.RefusalError) {
	switch {
	case len(parents) >= 2:
		base := ss.st.mergeBase(ctx, parents[0], parents[1])
		if base == "" {
			return prUnit{skip: reasonNoPR, head: parents[1]}, nil
		}
		return prUnit{
			head: parents[1], base: base, landed: introducer, reconstruction: reconMergeParent,
			number: mergeNumber(subject),
		}, nil
	case squashNumberRe.MatchString(subject) && len(parents) == 0:
		return prUnit{skip: reasonNoPR}, nil
	case squashNumberRe.MatchString(subject):
		onLine, ref := ss.onFirstParentLine(ctx, introducer)
		if ref != nil {
			return prUnit{}, ref
		}
		if onLine {
			return prUnit{
				head: introducer, base: parents[0], landed: introducer, reconstruction: reconSquash,
				number: squashNumber(subject),
			}, nil
		}
		return ss.firstParentUnit(ctx, introducer)
	default:
		return ss.firstParentUnit(ctx, introducer)
	}
}

// onFirstParentLine reports whether introducer is on the first-parent line of
// until. The range is listed oldest first, so the first line is the commit
// nearest introducer; its first parent is introducer exactly when introducer
// is on that line. A title alone never makes a commit a squash landing.
func (ss *seedState) onFirstParentLine(ctx context.Context, introducer string) (bool, *core.RefusalError) {
	if introducer == ss.until.SHA {
		return true, nil
	}
	out, exit, stderr, _, err := ss.st.gitCapped(ctx, commandOutputCap, ss.st.root,
		"rev-list", "--first-parent", "--reverse", "--parents", introducer+".."+ss.until.SHA)
	if err != nil {
		return false, ss.st.gitFailure(ctx, err.Error())
	}
	if exit != 0 {
		return false, ss.st.gitFailure(ctx, "git rev-list failed: "+firstLine(stderr))
	}
	fields := strings.Fields(firstLine(out))
	return len(fields) >= 2 && fields[1] == introducer, nil
}

// firstParentUnit finds the first first-parent merge after introducer that
// brings it to the first-parent line.
func (ss *seedState) firstParentUnit(ctx context.Context, introducer string) (prUnit, *core.RefusalError) {
	out, exit, stderr, err := ss.st.git(ctx, ss.st.root,
		"rev-list", "--merges", "--first-parent", "--reverse", introducer+".."+ss.until.SHA)
	if err != nil {
		return prUnit{}, ss.st.gitFailure(ctx, err.Error())
	}
	if exit != 0 {
		return prUnit{}, ss.st.gitFailure(ctx, "git rev-list failed: "+firstLine(stderr))
	}
	for merge := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		unit, ok, ref := ss.mergeCandidate(ctx, introducer, merge)
		if ref != nil {
			return prUnit{}, ref
		}
		if ok {
			return unit, nil
		}
	}
	return prUnit{skip: reasonNoPR}, nil
}

// mergeCandidate reports whether one first-parent merge is the PR that
// introduced introducer.
//
//nolint:revive // one refusal check per merge-topology rule
func (ss *seedState) mergeCandidate(ctx context.Context, introducer, merge string) (prUnit, bool, *core.RefusalError) {
	if merge == "" {
		return prUnit{}, false, nil
	}
	parents, ref := ss.st.parents(ctx, merge)
	if ref != nil {
		return prUnit{}, false, ref
	}
	if len(parents) < 2 {
		return prUnit{}, false, nil
	}
	byFirst, ref := ss.st.isAncestor(ctx, introducer, parents[0])
	if ref != nil {
		return prUnit{}, false, ref
	}
	if byFirst {
		return prUnit{}, false, nil
	}
	introduced, ref := ss.st.isAncestor(ctx, introducer, merge)
	if ref != nil || !introduced {
		return prUnit{}, false, ref
	}
	base := ss.st.mergeBase(ctx, parents[0], parents[1])
	if base == "" {
		return prUnit{skip: reasonNoPR}, true, nil
	}
	subject, _, ref := ss.st.subjectBody(ctx, merge)
	if ref != nil {
		return prUnit{}, false, ref
	}
	return prUnit{
		head: parents[1], base: base, landed: merge, reconstruction: reconFirstParent,
		number: mergeNumber(subject),
	}, true, nil
}

// prChecks applies the PR-level exclusions and fills the dates. The since
// ancestor check runs first: it is a comparison of two commit ids, while the
// size check reads the whole PR diff.
func (ss *seedState) prChecks(ctx context.Context, unit *prUnit) *core.RefusalError {
	ancestor, ref := ss.st.isAncestor(ctx, unit.landed, ss.since.SHA)
	if ref != nil {
		return ref
	}
	if ancestor {
		unit.skip = reasonAncestorSince
		return nil
	}
	over, ref := ss.prOverCap(ctx, unit.head, unit.base)
	if ref != nil {
		return ref
	}
	if over {
		unit.skip = reasonLargePR
		return nil
	}
	headAt, ref := ss.st.commitDate(ctx, unit.head)
	if ref != nil {
		return ref
	}
	mergedAt, ref := ss.st.commitDate(ctx, unit.landed)
	if ref != nil {
		return ref
	}
	unit.headAt, unit.mergedAt = headAt, mergedAt
	return nil
}

// prOverCap reports a PR diff above fetch's 512,000-byte cap.
func (ss *seedState) prOverCap(ctx context.Context, head, base string) (bool, *core.RefusalError) {
	if over, ok := ss.prCaps[head]; ok {
		return over, nil
	}
	out, exit, stderr, truncated, err := ss.st.gitCapped(ctx, fetchDiffCapBytes+1, ss.st.root,
		diffArgs("diff", "--no-renames", base, head)...)
	if err != nil {
		return false, ss.st.gitFailure(ctx, err.Error())
	}
	if exit != 0 {
		return false, ss.st.gitFailure(ctx, "git diff failed: "+firstLine(stderr))
	}
	over := truncated || len(out) > fetchDiffCapBytes
	ss.prCaps[head] = over
	return over, nil
}

// mergeNumber reads a GitHub merge subject's PR number.
func mergeNumber(subject string) *int {
	match := mergeNumberRe.FindStringSubmatch(subject)
	if match == nil {
		return nil
	}
	return atoiPtr(match[1])
}

// squashNumber reads a squash subject's trailing (#N).
func squashNumber(subject string) *int {
	match := squashNumberRe.FindStringSubmatch(subject)
	if match == nil {
		return nil
	}
	return atoiPtr(match[1])
}

// atoiPtr parses a decimal PR number.
func atoiPtr(raw string) *int {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return nil
	}
	return &n
}

// blame resolves the commit that last touched one line at rev.
func (st *state) blame(ctx context.Context, rev, path string, line int) (string, bool, *core.RefusalError) {
	out, exit, stderr, err := st.git(ctx, st.root, "blame", "--porcelain", "--no-ignore-revs-file",
		"-L", strconv.Itoa(line)+","+strconv.Itoa(line), rev, "--", path)
	if err != nil {
		return "", false, st.gitFailure(ctx, err.Error())
	}
	if exit != 0 {
		if unreadableLine(stderr) {
			return "", false, nil
		}
		return "", false, st.gitFailure(ctx, "git blame failed: "+firstLine(stderr))
	}
	fields := strings.Fields(firstLine(out))
	if len(fields) == 0 {
		return "", false, nil
	}
	sha := strings.TrimPrefix(fields[0], "^")
	if strings.Trim(sha, "0") == "" || !hexRe.MatchString(sha) {
		return "", false, nil
	}
	return sha, true, nil
}

// unreadableLine reports a blame whose line is outside the file's history.
func unreadableLine(stderr string) bool {
	return strings.Contains(stderr, "has only") || strings.Contains(stderr, "no such path")
}

// anchor greps the deleted text at the PR head and returns the one line it
// identifies. A unique hit is accepted; multiple hits must all blame to the
// introducer.
func (st *state) anchor(ctx context.Context, unit *prUnit, line diffLine, introducer string) (int, bool, *core.RefusalError) {
	out, exit, stderr, err := st.git(ctx, st.root, "grep", "--no-color", "-n", "-F", "-e", line.Text, unit.head, "--", line.Path)
	if err != nil {
		return 0, false, st.gitFailure(ctx, err.Error())
	}
	if exit == 1 {
		return 0, false, nil
	}
	if exit != 0 {
		return 0, false, st.gitFailure(ctx, "git grep failed: "+firstLine(stderr))
	}
	hits := grepHits(out, unit.head, line)
	if len(hits) == 0 {
		return 0, false, nil
	}
	if len(hits) == 1 {
		return hits[0], true, nil
	}
	aligned, ref := st.allBlameTo(ctx, unit.head, line.Path, hits, introducer)
	if ref != nil {
		return 0, false, ref
	}
	if !aligned {
		return 0, false, nil
	}
	return hits[0], true, nil
}

// allBlameTo reports whether every duplicate hit blames to the introducer.
func (st *state) allBlameTo(
	ctx context.Context, rev, path string, hits []int, introducer string,
) (bool, *core.RefusalError) {
	for _, at := range hits {
		got, ok, ref := st.blame(ctx, rev, path, at)
		if ref != nil {
			return false, ref
		}
		if !ok || got != introducer {
			return false, nil
		}
	}
	return true, nil
}

// grepHits parses `<rev>:<path>:<line>:<text>` output, keeping exact whole-line
// matches only.
func grepHits(out, rev string, line diffLine) []int {
	prefix := rev + colonSep + line.Path + colonSep
	var hits []int
	for raw := range strings.SplitSeq(out, "\n") {
		if at, ok := grepHit(raw, prefix, line.Text); ok {
			hits = append(hits, at)
		}
	}
	slices.Sort(hits)
	return hits
}

// grepHit parses one grep output line.
func grepHit(raw, prefix, text string) (int, bool) {
	rest, ok := strings.CutPrefix(raw, prefix)
	if !ok {
		return 0, false
	}
	num, content, ok := strings.Cut(rest, colonSep)
	if !ok || content != text {
		return 0, false
	}
	at, err := strconv.Atoi(num)
	if err != nil || at <= 0 {
		return 0, false
	}
	return at, true
}
