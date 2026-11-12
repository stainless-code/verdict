package fetchcore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/identity"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/provenance"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// runLocalTail owns the fetch-local block: worktree/staged/range/branch/
// stash diff modes sharing the cap/chunk/budget/prompt planes, returning
// the envelope data and next step for the adapter to render.
func runLocalTail(ctx context.Context, ctx2 *fetchRunCtx, req *core.FetchLocalRequest) (*core.FetchData, string, error) {
	r := ctx2.runner
	f := &req.Flags
	mode, dir, slug, ref := localSetup(ctx, ctx2, req, f)
	if ref != nil {
		return nil, "", ref
	}
	if !f.Worktree {
		return runLocalBuild(ctx, ctx2, req, f, r, dir, slug, &mode)
	}
	iso, created, iref := setupIsolation(ctx, r, slug, &mode)
	if iref != nil {
		return nil, "", iref
	}
	if !created {
		return runLocalBuild(ctx, ctx2, req, f, r, dir, slug, &mode)
	}
	r.iso = iso
	defer iso.cleanup(ctx, r)
	fmt.Fprintf(r.Human, "worktree: %s (temporary isolated checkout; removed on exit)\n", iso.dir)
	return runLocalBuild(ctx, ctx2, req, f, r, dir, slug, &mode)
}

func runLocalBuild(
	ctx context.Context, ctx2 *fetchRunCtx, req *core.FetchLocalRequest, f *core.FetchLocalFlags,
	r *session, dir, slug string, mode *localMode,
) (*core.FetchData, string, error) {
	if ref := localPolicy(ctx, ctx2, req, f, r, mode); ref != nil {
		return nil, "", ref
	}
	if ref := localDiff(ctx, r, dir, mode, f.Paths); ref != nil {
		return nil, "", ref
	}
	listed, numstat := localFileStats(ctx, r, mode, f.Paths)
	if ref := localResolve(ctx, ctx2, req, f, mode, listed); ref != nil {
		return nil, "", ref
	}
	budget, budgetErr := localBudgetGates(ctx2, f, req.RepoRootPath(), dir)
	if budgetErr != nil {
		return nil, "", budgetErr
	}
	fc, chunkActive, capErr := localCap(ctx2, dir, f.ChunkFlag)
	if capErr != nil {
		return nil, "", capErr
	}
	total, adds, dels, diffSHA, serr := localStats(ctx, r, dir, listed, numstat, f.Paths)
	if serr != nil {
		return nil, "", serr
	}
	if ref := localStores(ctx2, r, dir, slug, listed, numstat); ref != nil {
		return nil, "", ref
	}
	return localFinish(ctx, ctx2, req, f, r, dir, &budget, fc, chunkActive, mode, listed, total, adds, dels, diffSHA)
}

// localPolicy applies the fetch-local policy stack up to the trusted-base
// parse: head controls, phase one, and the warnings and privacy view. The
// full plan waits for the actual diff files, so no group-blind spend gate
// runs here.
func localPolicy(
	ctx context.Context, ctx2 *fetchRunCtx, req *core.FetchLocalRequest,
	f *core.FetchLocalFlags, r *session, mode *localMode,
) error {
	controlsRoot := req.RepoRootPath()
	if r.iso != nil {
		controlsRoot = r.iso.dir
	}
	if ref := resolveControls(ctx, ctx2, "", controlsRoot, mode.baseSHA, f.HeadControls); ref != nil {
		return ref
	}
	rc, ref := resolvePhaseOne(ctx2.env, req.SettingsPath, f.ChunkFlag)
	if ref != nil {
		return ref
	}
	ctx2.runCfg = rc
	rc.root = req.RepoRootPath()
	for _, warn := range rc.envWarns {
		r.Warn(warn)
	}
	r.privacy = rc.resolved.Privacy
	if ref := resolveLensPacks(ctx2); ref != nil {
		return ref
	}
	return nil
}

// localResolve completes the plan over the actual listed files before caps,
// chunking, or any model-facing export.
func localResolve(
	ctx context.Context, ctx2 *fetchRunCtx, req *core.FetchLocalRequest,
	f *core.FetchLocalFlags, mode *localMode, listed string,
) *core.RefusalError {
	rc := ctx2.runCfg
	if err := rc.resolveLocalPhaseTwo(
		ctx, ctx2.runner.commander, mode.baseSHA, f.ChunkFlag, listedInputs(ctx2.dir, listed), req.SettingsPath,
	); err != nil {
		return err
	}
	ctx2.runner.privacy = rc.resolved.Privacy
	return nil
}

// localSlugGate owns the fetch-local name gate: empty, numeric, and
// local-prefixed names refuse typed.
func localSlugGate(pr, slug string) *core.RefusalError {
	if slug == "" || slug == "local" {
		return &core.RefusalError{Code: codeBadSlug, Message: fmt.Sprintf("name '%s' sanitizes to nothing — pass a usable bundle name", pr)}
	}
	if readcommon.IsDigits(slug) {
		return &core.RefusalError{Code: codeBadSlug, Message: fmt.Sprintf("'%s' looks like a PR number — use fetch for PRs", slug)}
	}
	if strings.HasPrefix(slug, "local-") {
		return &core.RefusalError{Code: codeBadSlug, Message: "no local- prefix needed — the store adds it"}
	}
	return nil
}

// localModeCount counts the selected diff modes for the exclusivity gate.
func localModeCount(f *core.FetchLocalFlags) int {
	modes := 0
	if f.RangeSpec != "" {
		modes++
	}
	if f.BaseRef != "" {
		modes++
	}
	if f.Staged {
		modes++
	}
	if f.HasStash {
		modes++
	}
	return modes
}

// localSetup owns the fetch-local preamble: slug gate, worktree check,
// mode resolution, and the bundle directory.
func localSetup(
	ctx context.Context, ctx2 *fetchRunCtx, req *core.FetchLocalRequest, f *core.FetchLocalFlags,
) (localMode, string, string, error) {
	slug := localSlug(req.Pr)
	if ref := localSlugGate(req.Pr, slug); ref != nil {
		return localMode{}, "", "", ref
	}
	dir, ref := readcommon.ResolveBundleDir(req.RepoRootPath(), "local-"+slug)
	if ref != nil {
		//nolint:wrapcheck // typed refusal passes through untouched so the edge renders its code/message
		return localMode{}, "", "", ref
	}
	ctx2.dir = dir
	if res, err := ctx2.runner.run(ctx, gitCmdRevParse, "--is-inside-work-tree"); err != nil {
		_ = res
		return localMode{}, "", "", &core.RefusalError{Code: "not-a-repo", Message: "fetch-local runs inside a git worktree"}
	}
	branch := ctx2.runner.gitLineOrEmpty(ctx, modeBranch, "--show-current")
	if localModeCount(f) > 1 {
		return localMode{}, "", "", &core.RefusalError{Code: "bad-flags", Message: "--range/--base/--staged/--stash are mutually exclusive"}
	}
	mode, err := resolveLocalMode(ctx, ctx2.runner, f, branch)
	if err != nil {
		return localMode{}, "", "", err
	}
	if _, err := measuredUsage(ctx2.runner); err != nil {
		return localMode{}, "", "", err
	}
	if ref := ensureBundleState(req.RepoRootPath()); ref != nil {
		return localMode{}, "", "", ref
	}
	return mode, dir, slug, nil
}

// localBudgetGates runs the single spend gate over the full plan, then
// prepares the bundle directory. The same state rides the tail receipt, so
// the gate result is never a discarded local shadow.
func localBudgetGates(ctx2 *fetchRunCtx, f *core.FetchLocalFlags, root, dir string) (budgetState, error) {
	r := ctx2.runner
	resolved := ctx2.runCfg.resolved
	budget := resolveBudget(r, resolved)
	if err := budgetGate(r, &budget, resolved, f.BudgetConsent); err != nil {
		return budget, err
	}
	if ref := ensureBundleState(root); ref != nil {
		return budget, ref
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return budget, &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not create %s: %v", dir, err)}
	}
	return budget, nil
}

// localDiff owns the fetch-local diff read: the mode diff runs with its
// stderr sidecar, and full.diff lands (empty on failure, like `>`). Under
// an isolated checkout the explicit commit pair replaces the mode's
// live-tree refs, so the bytes come from the checkout, not the worktree.
func localDiff(ctx context.Context, r *session, dir string, mode *localMode, paths []string) error {
	diffArgs := append(mode.diffArgs(), paths...)
	if r.iso != nil {
		diffArgs = append([]string{gitCmdDiff, r.iso.base, r.iso.head}, paths...)
	}
	diffRes, err := r.git(ctx, diffArgs...)
	if werr := writeErrFile(dir+suffixDiffErr, diffRes.Stderr); werr != nil {
		return werr
	}
	if err != nil {
		return localDiffFail(dir, diffRes.Stderr)
	}
	if werr := atomic.WriteFile(dir+suffixFullDiff, []byte(diffRes.Stdout)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteFullDiff, dir, werr)}
	}
	return nil
}

// localDiffFail owns the failed-diff fork: the empty truncation lands
// first, then the typed refusal carries gh's excerpt.
func localDiffFail(dir, stderr string) error {
	if ref := truncateEmpty(dir + suffixFullDiff); ref != nil {
		return ref
	}
	return &core.RefusalError{Code: "diff-failed", Message: "git diff failed: " + headBytes(stderr, numExcerpt)}
}

// localCap owns the fetch-local cap gate: resolution and the over-cap
// fork (chunks armed or typed refusal with split candidates).
func localCap(ctx2 *fetchRunCtx, dir string, chunkFlag bool) (fetchCap, bool, error) {
	r := ctx2.runner
	resolved := ctx2.runCfg.resolved
	fc := resolveFetchCap(r, resolved)
	chunkActive, ref := checkDiffSize(r, resolved, dir+suffixFullDiff, fc, chunkFlag)
	if ref != nil {
		return fc, false, ref
	}
	return fc, chunkActive, nil
}

func localStats(
	ctx context.Context, r *session, dir, listed, numstat string, paths []string,
) (total, adds, dels int, diffSHA string, err error) {
	if herr := buildHunksTSV(dir+suffixFullDiff, dir+"/hunks.tsv"); herr != nil {
		return 0, 0, 0, "", herr
	}
	fi, statErr := os.Stat(dir + suffixFullDiff)
	if statErr == nil && fi.Size() > 0 && !hasHunkHeader(readOptional(dir+suffixFullDiff)) {
		r.Warn("no @@ hunks — binary/mode-only changes; bundle kept, anchors unchecked, everything kept")
	}
	diffSHA = sha256Hex(dir + suffixFullDiff)
	total, adds, dels = localCounts(listed, numstat)
	writeLocalUntrackedWarns(ctx, r, paths)
	writeLocalBinaryWarns(r, numstat)
	if len(paths) > 0 && !hasNonEmptyLine(listed) {
		shown := strings.Join(paths, spaceSep) + " "
		r.Warn(fmt.Sprintf("paths filter matched nothing: %s— bundle empty, review proceeds on empty diff", shown))
	}
	return total, adds, dels, diffSHA, nil
}

func localStores(ctx2 *fetchRunCtx, r *session, dir, slug, listed, numstat string) error {
	if err := writeLocalTOC(ctx2, slug, listed, numstat); err != nil {
		return err
	}
	fmt.Fprintf(r.Human, "» roles -> %s/roles.txt (heuristic file roles for triage)\n", r.display(dir))
	if err := atomic.WriteFile(dir+"/roles.txt", []byte(rolesForPathsText(listed))); err != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write %s/roles.txt: %v", dir, err)}
	}
	if ref := emitContext(ctx2); ref != nil {
		return ref
	}
	if err := ensureStore(dir); err != nil {
		return err
	}
	if ref := writeSensitiveManifest(r, dir, listed); ref != nil {
		return ref
	}
	writeKBRecall(ctx2, dir, listed)
	writeLensBodies(ctx2)
	return nil
}

// localFinish owns the fetch-local tail: source.json, the done/next
// lines, the opt-in chunks, and the typed data for the adapter's envelope.
func localFinish(
	ctx context.Context, ctx2 *fetchRunCtx, req *core.FetchLocalRequest, f *core.FetchLocalFlags, r *session,
	dir string, budget *budgetState, fc fetchCap, chunkActive bool, mode *localMode,
	listed string, total, adds, dels int, diffSHA string,
) (*core.FetchData, string, error) {
	slug := localSlug(req.Pr)
	toolVersion := provenance.ToolVersion(ctx2.root)
	now := core.NowUTC(r.Now())
	model, _ := ctx2.env["REVIEW_MODEL"]
	ident, err := writeIdentityFile(ctx, r.commander, dir, mode.headSHA, mode.baseSHA)
	if err != nil {
		return nil, "", err
	}
	if err := writeSourceJSON(dir, req.Repo, mode, slug, diffSHA, total, adds, dels, toolVersion, model, now, ident); err != nil {
		return nil, "", err
	}
	shown := r.display(dir)
	fmt.Fprintf(r.Human, "done: %s/source.json %s/full.diff %s/toc.txt %s/roles.txt (%d files)\n", shown, shown, shown, shown, total)
	fmt.Fprintf(r.Human, "next: feed SKILL.md + prompts/system.md + prompts/review.md + prompts/diagrams.md + "+
		"%s/source.json + %s to any agent; ask it to write %s/review.json\n", dir, diffFeed(dir, chunkActive, fullDiffFile), dir)
	chunkJSON, ref := maybeWriteChunks(r.Diag, dir, fc.maxBytes, chunkActive,
		groupLimits(ctx2.runCfg.resolved, f.ChunkFlag), mode.headSHA, mode.baseSHA, fc.source, r.privacy.Exclusions)
	if ref != nil {
		return nil, "", ref
	}
	return fetchTailLocal(ctx, ctx2, req, f, budget, &fc, chunkJSON, mode, listed, total, adds, dels)
}

// localSlug owns the fetch-local slug pipeline on bytes: ASCII-lowercase,
// non-[a-z0-9-] runs to one dash, trim dashes, first 48 bytes.
func localSlug(pr string) string {
	lower := readcommon.ASCIILower(pr)
	var sb strings.Builder
	sb.Grow(len(lower))
	dash := false
	for i := range len(lower) {
		c := lower[i]
		if slugKeep(c) {
			sb.WriteByte(c)
			dash = false
			continue
		}
		if !dash {
			sb.WriteByte('-')
			dash = true
		}
	}
	slug := strings.Trim(sb.String(), "-")
	if len(slug) > numSlugMax {
		slug = slug[:numSlugMax]
	}
	return slug
}

// slugKeep owns the slug character class: ASCII alnum and dash survive.
func slugKeep(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-'
}

// localMode is one resolved fetch-local diff source.
type localMode struct {
	kind     string
	baseSHA  string
	headSHA  string
	headRef  string
	rangeVal string
	dirty    bool
}

// diffArgs owns the per-kind diff argv (paths appended by the caller).
func (m *localMode) diffArgs() []string {
	switch m.kind {
	case modeStash:
		return []string{gitCmdDiff, m.baseSHA, m.headSHA}
	case modeRange:
		return []string{gitCmdDiff, m.baseSHA, m.headSHA}
	case modeBranch:
		// `git diff <base> HEAD` — HEAD resolves at run time; the stored
		// head SHA equals it, so the bytes match.
		return []string{gitCmdDiff, m.baseSHA, gitHeadRef}
	case modeStaged:
		return []string{gitCmdDiff, flagCached}
	default:
		return []string{gitCmdDiff, gitHeadRef}
	}
}

// resolveLocalMode owns the fetch-local mode resolution.
func resolveLocalMode(ctx context.Context, s *session, f *core.FetchLocalFlags, branch string) (localMode, error) {
	switch {
	case f.HasStash:
		return resolveStashMode(ctx, s, f)
	case f.RangeSpec != "":
		return resolveRangeMode(ctx, s, f.RangeSpec)
	case f.BaseRef != "":
		return resolveBaseMode(ctx, s, f, branch)
	case f.Staged:
		return resolveStagedMode(ctx, s)
	default:
		return resolveWorktreeMode(ctx, s, branch), nil
	}
}

func resolveStashMode(ctx context.Context, s *session, f *core.FetchLocalFlags) (localMode, error) {
	if _, err := s.run(ctx, gitCmdRevParse, "-q", flagVerify, stashRefPrefix+f.StashN+"}"); err != nil {
		return localMode{}, &core.RefusalError{
			Code:    "bad-stash",
			Message: fmt.Sprintf("no such stash: stash@{%s} (see git stash list)", f.StashN),
		}
	}
	head := s.gitLineOrEmpty(ctx, gitCmdRevParse, stashRefPrefix+f.StashN+"}^{commit}")
	base := s.gitLineOrEmpty(ctx, gitCmdRevParse, stashRefPrefix+f.StashN+"}^")
	ref := "stash@{" + f.StashN + "}"
	return localMode{kind: modeStash, baseSHA: base, headSHA: head, headRef: ref, rangeVal: ref}, nil
}

// resolveBaseMode owns the base mode: the merge-base with HEAD bounds a
// branch-style diff.
func resolveBaseMode(ctx context.Context, s *session, f *core.FetchLocalFlags, branch string) (localMode, error) {
	mb, err := s.gitLine(ctx, "merge-base", f.BaseRef, gitHeadRef)
	if err != nil {
		return localMode{}, &core.RefusalError{Code: "bad-base", Message: fmt.Sprintf("no merge-base with '%s'", f.BaseRef)}
	}
	head := s.gitLineOrEmpty(ctx, gitCmdRevParse, gitHeadRef)
	headRef := branch
	if headRef == "" {
		headRef = gitHeadRef
	}
	return localMode{kind: modeBranch, baseSHA: mb, headSHA: head, headRef: headRef, rangeVal: f.BaseRef + "...HEAD"}, nil
}

func resolveStagedMode(ctx context.Context, s *session) (localMode, error) {
	base := s.gitLineOrEmpty(ctx, gitCmdRevParse, gitHeadRef)
	head := s.gitLineOrEmpty(ctx, "write-tree")
	return localMode{kind: modeStaged, baseSHA: base, headSHA: head, headRef: keyIndex, rangeVal: "HEAD:index"}, nil
}

func resolveWorktreeMode(ctx context.Context, s *session, branch string) localMode {
	base := s.gitLineOrEmpty(ctx, gitCmdRevParse, gitHeadRef)
	headRef := branch
	if headRef == "" {
		headRef = "worktree"
	}
	return localMode{kind: "worktree", baseSHA: base, headRef: headRef, rangeVal: "HEAD:worktree", dirty: true}
}

// resolveRangeMode owns the range split: three-dot wins when present,
// merging at the merge-base; otherwise a straight two-sided diff.
func resolveRangeMode(ctx context.Context, s *session, spec string) (localMode, error) {
	if strings.Contains(spec, gitRange3) {
		return resolveThreeDot(ctx, s, spec)
	}
	return resolveTwoDot(ctx, s, spec)
}

// resolveThreeDot owns the three-dot range: the merge-base bounds the
// diff, with both ends resolving.
func resolveThreeDot(ctx context.Context, s *session, spec string) (localMode, error) {
	ra, _, _ := strings.Cut(spec, gitRange3)
	_, rb, _ := strings.CutLast(spec, gitRange3)
	base, err := s.gitLine(ctx, gitCmdRevParse, flagVerify, ra)
	if err != nil {
		return localMode{}, &core.RefusalError{Code: codeBadRange, Message: fmt.Sprintf("cannot resolve range base '%s'", ra)}
	}
	// merge-base takes the resolved base: the rev-parse above both
	// validates the ref (its refusal fires first) and pins the SHA.
	mb, err := s.gitLine(ctx, "merge-base", base, rb)
	if err != nil {
		return localMode{}, &core.RefusalError{Code: codeBadRange, Message: fmt.Sprintf("no merge-base for '%s'", spec)}
	}
	head, err := s.gitLine(ctx, gitCmdRevParse, flagVerify, rb+"^{commit}")
	if err != nil {
		return localMode{}, &core.RefusalError{Code: codeBadRange, Message: fmt.Sprintf("cannot resolve range head '%s'", rb)}
	}
	return localMode{kind: modeRange, baseSHA: mb, headSHA: head, headRef: rb, rangeVal: spec}, nil
}

// resolveTwoDot owns the two-dot range: a straight two-sided diff, with
// both ends resolving.
func resolveTwoDot(ctx context.Context, s *session, spec string) (localMode, error) {
	ra, rb := spec, spec
	if strings.Contains(spec, "..") {
		ra, _, _ = strings.Cut(spec, "..")
		_, rb, _ = strings.CutLast(spec, "..")
	}
	base, err := s.gitLine(ctx, gitCmdRevParse, flagVerify, ra+"^{commit}")
	if err != nil {
		return localMode{}, &core.RefusalError{Code: codeBadRange, Message: fmt.Sprintf("cannot resolve range base '%s'", ra)}
	}
	head, err := s.gitLine(ctx, gitCmdRevParse, flagVerify, rb+"^{commit}")
	if err != nil {
		return localMode{}, &core.RefusalError{Code: codeBadRange, Message: fmt.Sprintf("cannot resolve range head '%s'", rb)}
	}
	return localMode{kind: modeRange, baseSHA: base, headSHA: head, headRef: rb, rangeVal: spec}, nil
}

// gitLine runs git, returning stdout stripped of trailing newlines. Stderr
// is discarded.
func (s *session) gitLine(ctx context.Context, args ...string) (string, error) {
	res, err := s.run(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(res.Stdout, newline), nil
}

// gitLineOrEmpty runs git, reading "" on failure with stdout captured.
func (s *session) gitLineOrEmpty(ctx context.Context, args ...string) string {
	line, err := s.gitLine(ctx, args...)
	if err != nil {
		return ""
	}
	return line
}

// truncateEmpty empties the file before a guarded failure: the empty residue
// lands first so a bundle reads the same through every gate. A write error
// itself refuses (unreachable on a writable bundle).
func truncateEmpty(path string) *core.RefusalError {
	if err := atomic.WriteFile(path, []byte{}); err != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write %s: %v", path, err)}
	}
	return nil
}

// localFileStats owns the file-list and numstat reads for the mode. Under
// an isolated checkout the explicit commit pair replaces every mode's
// live-tree refs (a three-dot A...B list is its merge-base-to-head pair,
// so the file sets match the classic refs exactly).
func localFileStats(ctx context.Context, s *session, mode *localMode, paths []string) (string, string) {
	var listRef, statRef []string
	if s.iso != nil {
		listRef = []string{s.iso.base, s.iso.head}
		statRef = []string{s.iso.base, s.iso.head}
	} else {
		switch mode.kind {
		case modeStaged:
			listRef = []string{flagCached}
			statRef = []string{flagCached}
		case modeRange:
			listRef = []string{mode.rangeVal}
			statRef = []string{mode.rangeVal}
		case modeBranch:
			listRef = []string{mode.rangeBaseRef() + "...HEAD"}
			statRef = []string{mode.rangeBaseRef() + "...HEAD"}
		case modeStash:
			listRef = []string{mode.baseSHA, mode.headSHA}
			statRef = []string{mode.baseSHA, mode.headSHA}
		default:
			listRef = []string{gitHeadRef}
			statRef = []string{gitHeadRef}
		}
	}
	if len(paths) > 0 {
		listRef = append(listRef, "--")
		listRef = append(listRef, paths...)
		statRef = append(statRef, "--")
		statRef = append(statRef, paths...)
	}
	fileList := ""
	if res, err := s.git(ctx, append([]string{gitCmdDiff, "--no-renames", "--name-only"}, listRef...)...); err == nil {
		fileList = strings.TrimRight(res.Stdout, newline)
	} else if res, err := s.git(ctx, append([]string{gitCmdDiff, "--name-only"}, listRef...)...); err == nil {
		fileList = strings.TrimRight(res.Stdout, newline)
	}
	numstat := ""
	if res, err := s.git(ctx, append([]string{gitCmdDiff, "--numstat"}, statRef...)...); err == nil {
		numstat = strings.TrimRight(res.Stdout, newline)
	}
	return fileList, numstat
}

// rangeBaseRef recovers the --base ref from the stored range spec.
func (m *localMode) rangeBaseRef() string {
	if idx := strings.Index(m.rangeVal, gitRange3); idx >= 0 {
		return m.rangeVal[:idx]
	}
	return m.rangeVal
}

// localCounts owns the total/adds/dels derivation.
func localCounts(listed, numstat string) (total, adds, dels int) {
	total = countNonEmptyLines(listed)
	for _, line := range splitLines(numstat) {
		fields := strings.Fields(line)
		if len(fields) != numStatFields {
			continue
		}
		adds += intOrZero(fields[0])
		dels += intOrZero(fields[1])
	}
	return total, adds, dels
}

// countNonEmptyLines counts the non-empty lines.
func countNonEmptyLines(s string) int {
	n := 0
	for _, line := range splitLines(s) {
		if line != "" {
			n++
		}
	}
	return n
}

func intOrZero(s string) int {
	if !readcommon.IsDigits(s) {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// hasNonEmptyLine reports whether any line is non-empty.
func hasNonEmptyLine(s string) bool {
	for _, line := range splitLines(s) {
		if line != "" {
			return true
		}
	}
	return false
}

// writeLocalUntrackedWarns owns the untracked-files advisory warning.
func writeLocalUntrackedWarns(ctx context.Context, r *session, paths []string) {
	args := []string{"ls-files", "--others", "--exclude-standard"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	res, err := r.run(ctx, args...)
	if err != nil {
		return
	}
	kept := untrackedKept(res.Stdout)
	if len(kept) == 0 {
		return
	}
	shown := untrackedShown(kept)
	if len(kept) > numShownHits {
		r.Warn(fmt.Sprintf("untracked files excluded from bundle (%d): %sand %d more "+
			"(stage or commit to include)", len(kept), shown, len(kept)-numShownHits))
	} else {
		r.Warn(fmt.Sprintf("untracked files excluded from bundle (%d): %s(stage or commit to include)", len(kept), shown))
	}
}

func untrackedKept(stdout string) []string {
	var kept []string
	for _, line := range splitLines(strings.TrimRight(stdout, newline)) {
		if line == "" || strings.HasPrefix(line, ".verdict/") {
			continue
		}
		kept = append(kept, line)
	}
	return kept
}

func untrackedShown(kept []string) string {
	shown := ""
	for _, k := range kept {
		if len(strings.Fields(shown)) >= numShownHits {
			break
		}
		shown += k + spaceSep
	}
	return shown
}

// writeLocalBinaryWarns owns the binary-files advisory warning.
func writeLocalBinaryWarns(r *session, numstat string) {
	kept := binaryKept(numstat)
	if len(kept) == 0 {
		return
	}
	shown := strings.Join(kept[:min(len(kept), numShownHits)], spaceSep) + " "
	if len(kept) > numShownHits {
		r.Warn(fmt.Sprintf("binary files skipped, no hunks (%d): %sand %d more", len(kept), shown, len(kept)-numShownHits))
	} else {
		r.Warn(fmt.Sprintf("binary files skipped, no hunks (%d): %s", len(kept), shown))
	}
}

func binarySkipped(numstat string) []string {
	var skipped []string
	for _, line := range splitLines(numstat) {
		fields := strings.Split(line, "\t")
		if len(fields) == numStatFields && fields[0] == "-" && fields[1] == "-" {
			skipped = append(skipped, fields[2])
		}
	}
	return skipped
}

// binaryKept drops empty entries from the skipped list.
func binaryKept(numstat string) []string {
	var kept []string
	for _, s := range binarySkipped(numstat) {
		if s != "" {
			kept = append(kept, s)
		}
	}
	return kept
}

func writeLocalTOC(ctx2 *fetchRunCtx, slug, listed, numstat string) error {
	withHunks, derr := diffFiles(ctx2.dir+suffixFullDiff, ctx2.dir)
	if derr != nil {
		return derr
	}
	omitted := omittedLocal(listed, withHunks)
	var sb strings.Builder
	fmt.Fprintf(&sb, "# TOC for local %s (%s) — read ranges as coverage checklist\n\n", slug, ctx2.repo)
	sb.WriteString(diffHeaderIndex(ctx2.dir + suffixFullDiff))
	sb.WriteString("\n## stat\n")
	for _, line := range splitLines(numstat) {
		fields := strings.Fields(line)
		if len(fields) != numStatFields {
			continue
		}
		fmt.Fprintf(&sb, "%s (+%s/-%s)\n", fields[2], fields[0], fields[1])
	}
	sb.WriteString("\n## coverage manifest\n")
	fmt.Fprintf(&sb, "files changed: %d | files with diff hunks: %d\n", countNonEmptyLines(listed), len(withHunks))
	if len(omitted) > 0 {
		sb.WriteString("omitted (no hunks — binary, renamed without changes, or mode-only):\n")
		for _, p := range omitted {
			sb.WriteString("- " + p + newline)
		}
	} else {
		sb.WriteString("omitted: (none)\n")
	}
	return atomic.WriteFile(ctx2.dir+"/toc.txt", []byte(sb.String()))
}

// omittedLocal owns the local omitted computation: file_list paths absent
// from the hunked set (blank lines skipped).
func omittedLocal(listed string, withHunks []string) []string {
	inHunks := map[string]bool{}
	for _, p := range withHunks {
		inHunks[p] = true
	}
	var out []string
	for _, p := range splitLines(listed) {
		if p == "" {
			continue
		}
		if !inHunks[p] {
			out = append(out, p)
		}
	}
	return out
}

// writeSourceJSON owns the source.json writer: the local bundle record.
// The bundle patch identity rides along additively as patch_id and
// patch_id_kind, stamped from the identity just built over the bundle's
// own bytes; a null patch id reads null for the empty diff.
func writeSourceJSON(
	dir, repo string, mode *localMode, slug, diffSHA string,
	total, adds, dels int, toolVersion, model, now string, ident *identity.Identity,
) error {
	nullStr := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	modelVal := any(nil)
	if model != "" {
		modelVal = model
	}
	doc := jq.NewObj(
		keySchema, "1",
		keyKind, mode.kind,
		"repo", repo,
		"slug", slug,
		keyBaseRef, nullStr(mode.baseRef()),
		keyHeadRef, mode.headRef,
		keyBaseSHA, nullStr(mode.baseSHA),
		keyHeadSHA, nullStr(mode.headSHA),
		modeRange, nullStr(mode.rangeVal),
		"dirty", mode.dirty,
		"diff_sha256", diffSHA,
		"patch_id", core.Wire(ident.PatchID),
		"patch_id_kind", ident.PatchIDKind,
		keyFiles, total,
		keyAdditions, adds,
		keyDeletions, dels,
		"tool_version", toolVersion,
		"model", modelVal,
		"created_at", now,
	)
	if err := atomic.WriteFile(dir+"/source.json", []byte(jq.PrettyOrdered(doc))); err != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write %s/source.json: %v", dir, err)}
	}
	return nil
}

// baseRef returns the mode's base ref ("" for non-branch modes).
func (m *localMode) baseRef() string {
	if m.kind != modeBranch {
		return ""
	}
	return m.rangeBaseRef()
}

// sha256Hex renders the lowercase hex SHA-256 of data.
func sha256Hex(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
