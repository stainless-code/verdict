package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/provenance"
)

// runLocal mirrors the fetch-local block: worktree/staged/range/branch/
// stash diff modes sharing the cap/chunk/budget/prompt planes.
func runLocal(ctx context.Context, ctx2 *fetchRunCtx, req Request, f *localFlags) (int, error) {
	r := ctx2.runner
	mode, dir, slug, ref := localSetup(ctx, ctx2, req, f)
	if ref != nil {
		return 0, ref
	}
	budget, ref := localBudget(r, req, f, dir)
	if ref != nil {
		return 0, ref
	}
	if ref := localDiff(ctx, r, dir, &mode, f.paths); ref != nil {
		return 0, ref
	}
	fc, chunkActive, ref := localCap(r, req, dir, f.chunkFlag)
	if ref != nil {
		return 0, ref
	}
	listed, numstat, total, adds, dels, diffSHA, serr := localStats(ctx, r, dir, &mode, f.paths)
	if serr != nil {
		return 0, serr
	}
	if ref := localStores(ctx2, r, dir, slug, listed, numstat); ref != nil {
		return 0, ref
	}
	return localFinish(ctx, ctx2, req, f, r, dir, &budget, fc, chunkActive, &mode, listed, total, adds, dels, diffSHA)
}

// localSlugGate ports the fetch-local name gate: empty, numeric, and
// local-prefixed names refuse typed.
func localSlugGate(pr, slug string) *cli.RefusalError {
	if slug == "" || slug == "local" {
		return &cli.RefusalError{Code: codeBadSlug, Message: fmt.Sprintf("name '%s' sanitizes to nothing — pass a usable bundle name", pr)}
	}
	if isDigits(slug) {
		return &cli.RefusalError{Code: codeBadSlug, Message: fmt.Sprintf("'%s' looks like a PR number — use fetch for PRs", slug)}
	}
	if strings.HasPrefix(slug, "local-") {
		return &cli.RefusalError{Code: codeBadSlug, Message: "no local- prefix needed — the store adds it"}
	}
	return nil
}

// localModeCount counts the selected diff modes for the exclusivity gate.
func localModeCount(f *localFlags) int {
	modes := 0
	if f.rangeSpec != "" {
		modes++
	}
	if f.baseRef != "" {
		modes++
	}
	if f.staged {
		modes++
	}
	if f.hasStash {
		modes++
	}
	return modes
}

// localSetup ports the fetch-local preamble: slug gate, worktree check,
// mode resolution, and the bundle directory.
func localSetup(ctx context.Context, ctx2 *fetchRunCtx, req Request, f *localFlags) (localMode, string, string, error) {
	slug := localSlug(req.Pr)
	if ref := localSlugGate(req.Pr, slug); ref != nil {
		return localMode{}, "", "", ref
	}
	dir := ".review/local-" + slug
	ctx2.dir = dir
	if res, err := runCmd(ctx, binGit, gitCmdRevParse, "--is-inside-work-tree"); err != nil {
		_ = res
		return localMode{}, "", "", &cli.RefusalError{Code: "not-a-repo", Message: "fetch-local runs inside a git worktree"}
	}
	branch := gitLineOrEmpty(ctx, modeBranch, "--show-current")
	if localModeCount(f) > 1 {
		return localMode{}, "", "", &cli.RefusalError{Code: "bad-flags", Message: "--range/--base/--staged/--stash are mutually exclusive"}
	}
	mode, err := resolveLocalMode(ctx, f, branch)
	if err != nil {
		return localMode{}, "", "", err
	}
	return mode, dir, slug, nil
}

// localBudget ports the fetch-local budget preamble: resolution, gate,
// directory, and receipt.
func localBudget(r *cli.Runner, req Request, f *localFlags, dir string) (budgetState, error) {
	budget, err := resolveBudget(req.Env)
	if err != nil {
		return budget, err
	}
	if err := budgetGate(r, &budget, f.budgetConsent); err != nil {
		return budget, err
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return budget, &cli.RefusalError{Code: codeFetchFailed, Message: fmt.Sprintf("could not create %s: %v", dir, err)}
	}
	writeBudgetReceipt(r, &budget, dir, cli.NowUTC(r.Now()))
	return budget, nil
}

// localDiff ports the fetch-local diff read: the mode diff runs with its
// stderr sidecar, and full.diff lands (empty on failure, like `>`).
func localDiff(ctx context.Context, _ *cli.Runner, dir string, mode *localMode, paths []string) error {
	diffArgs := append(mode.diffArgs(), paths...)
	diffRes, err := runCmd(ctx, binGit, diffArgs...)
	if werr := writeErrFile(dir+suffixDiffErr, diffRes.stderr); werr != nil {
		return werr
	}
	if err != nil {
		return localDiffFail(dir, diffRes.stderr)
	}
	if werr := atomic.WriteFile(dir+suffixFullDiff, []byte(diffRes.stdout)); werr != nil {
		return &cli.RefusalError{Code: codeFetchFailed, Message: fmt.Sprintf(msgWriteFullDiff, dir, werr)}
	}
	return nil
}

// localDiffFail ports the failed-diff fork: the empty truncation lands
// first, then the typed refusal carries gh's excerpt.
func localDiffFail(dir, stderr string) error {
	if ref := truncateEmpty(dir + suffixFullDiff); ref != nil {
		return ref
	}
	return &cli.RefusalError{Code: "diff-failed", Message: "git diff failed: " + headBytes(stderr, numExcerpt)}
}

// localCap ports the fetch-local cap gate: resolution and the over-cap
// fork (chunks armed or typed refusal with split candidates).
func localCap(r *cli.Runner, req Request, dir string, chunkFlag bool) (fetchCap, bool, error) {
	fc, ref := resolveFetchCap(r, req.Env)
	if ref != nil {
		return fc, false, ref
	}
	chunkActive, ref := checkDiffSize(r, dir+suffixFullDiff, fc, chunkFlag)
	if ref != nil {
		return fc, false, ref
	}
	return fc, chunkActive, nil
}

// localStats ports the fetch-local measurements: hunks, the binary/warn
// lines, the file stats, and the counts.
func localStats(
	ctx context.Context, r *cli.Runner, dir string, mode *localMode, paths []string,
) (listed, numstat string, total, adds, dels int, diffSHA string, err error) {
	if herr := buildHunksTSV(dir+suffixFullDiff, dir+"/hunks.tsv"); herr != nil {
		return "", "", 0, 0, 0, "", herr
	}
	fi, statErr := os.Stat(dir + suffixFullDiff)
	if statErr == nil && fi.Size() > 0 && !hasHunkHeader(readOptional(dir+suffixFullDiff)) {
		r.Warn("no @@ hunks — binary/mode-only changes; bundle kept, anchors unchecked, everything kept")
	}
	diffSHA = sha256Hex(dir + suffixFullDiff)
	listed, numstat = localFileStats(ctx, mode, paths)
	total, adds, dels = localCounts(listed, numstat)
	writeLocalUntrackedWarns(ctx, r, paths)
	writeLocalBinaryWarns(r, numstat)
	if len(paths) > 0 && !hasNonEmptyLine(listed) {
		shown := strings.Join(paths, spaceSep) + " "
		r.Warn(fmt.Sprintf("paths filter matched nothing: %s— bundle empty, review proceeds on empty diff", shown))
	}
	return listed, numstat, total, adds, dels, diffSHA, nil
}

// localStores ports the fetch-local bundle stores: TOC, roles, context,
// claim store, sensitive manifest, and recall.
func localStores(ctx2 *fetchRunCtx, r *cli.Runner, dir, slug, listed, numstat string) error {
	if err := writeLocalTOC(ctx2, slug, listed, numstat); err != nil {
		return err
	}
	fmt.Fprintf(r.Human, "» roles -> %s/roles.txt (heuristic file roles for triage)\n", dir)
	if err := atomic.WriteFile(dir+"/roles.txt", []byte(rolesForPathsText(listed))); err != nil {
		return &cli.RefusalError{Code: codeFetchFailed, Message: fmt.Sprintf("could not write %s/roles.txt: %v", dir, err)}
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
	writeKBRecall(r, dir, listed)
	return nil
}

// localFinish ports the fetch-local tail: source.json, the done/next
// lines, the opt-in chunks, and the envelope.
func localFinish(
	_ context.Context, ctx2 *fetchRunCtx, req Request, f *localFlags, r *cli.Runner,
	dir string, budget *budgetState, fc fetchCap, chunkActive bool, mode *localMode,
	listed string, total, adds, dels int, diffSHA string,
) (int, error) {
	slug := localSlug(req.Pr)
	toolVersion := provenance.ToolVersion(req.Root)
	now := cli.NowUTC(r.Now())
	model, _ := req.Env["REVIEW_MODEL"]
	if err := writeSourceJSON(dir, req, mode, slug, diffSHA, total, adds, dels, toolVersion, model, now); err != nil {
		return 0, err
	}
	fmt.Fprintf(r.Human, "done: %s/source.json %s/full.diff %s/toc.txt %s/roles.txt (%d files)\n", dir, dir, dir, dir, total)
	fmt.Fprintf(r.Human, "next: feed SKILL.md + prompts/system.md + prompts/review.md + prompts/diagrams.md + "+
		"%s/source.json + full.diff to any agent; ask it to write %s/review.json\n", dir, dir)
	chunkJSON, ref := maybeWriteChunks(r.Diag, dir, fc.maxBytes, chunkActive, mode.headSHA, mode.baseSHA, fc.source)
	if ref != nil {
		return 0, ref
	}
	return fetchTailLocal(ctx2, req, f, budget, &fc, chunkJSON, slug, mode, listed, total, adds, dels, model, now)
}

// localSlug ports the fetch-local slug pipeline on bytes: ASCII-lowercase,
// non-[a-z0-9-] runs to one dash, trim dashes, first 48 bytes.
func localSlug(pr string) string {
	lower := asciiLower(pr)
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

// slugKeep ports the slug character class: ASCII alnum and dash survive.
func slugKeep(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-'
}

// isDigits ports `[[ "$slug" =~ ^[0-9]+$ ]]`.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
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

// diffArgs ports the per-kind diff argv (paths appended by the caller).
func (m *localMode) diffArgs() []string {
	switch m.kind {
	case modeStash:
		return []string{gitCmdDiff, m.baseSHA, m.headSHA}
	case modeRange:
		return []string{gitCmdDiff, m.baseSHA, m.headSHA}
	case modeBranch:
		// `git diff $mb HEAD` — the shell resolves HEAD late; the stored
		// head SHA equals it, so the bytes match.
		return []string{gitCmdDiff, m.baseSHA, gitHeadRef}
	case modeStaged:
		return []string{gitCmdDiff, flagCached}
	default:
		return []string{gitCmdDiff, gitHeadRef}
	}
}

// resolveLocalMode ports the fetch-local mode resolution.
func resolveLocalMode(ctx context.Context, f *localFlags, branch string) (localMode, error) {
	switch {
	case f.hasStash:
		return resolveStashMode(ctx, f)
	case f.rangeSpec != "":
		return resolveRangeMode(ctx, f.rangeSpec)
	case f.baseRef != "":
		return resolveBaseMode(ctx, f, branch)
	case f.staged:
		return resolveStagedMode(ctx)
	default:
		return resolveWorktreeMode(ctx, branch), nil
	}
}

// resolveStashMode ports the stash mode: the stash ref must verify, then
// its commit and parent bound the diff.
func resolveStashMode(ctx context.Context, f *localFlags) (localMode, error) {
	if _, err := runCmd(ctx, binGit, gitCmdRevParse, "-q", flagVerify, stashRefPrefix+f.stashN+"}"); err != nil {
		return localMode{}, &cli.RefusalError{
			Code:    "bad-stash",
			Message: fmt.Sprintf("no such stash: stash@{%s} (see git stash list)", f.stashN),
		}
	}
	head := gitLineOrEmpty(ctx, gitCmdRevParse, stashRefPrefix+f.stashN+"}^{commit}")
	base := gitLineOrEmpty(ctx, gitCmdRevParse, stashRefPrefix+f.stashN+"}^")
	ref := "stash@{" + f.stashN + "}"
	return localMode{kind: modeStash, baseSHA: base, headSHA: head, headRef: ref, rangeVal: ref}, nil
}

// resolveBaseMode ports the base mode: the merge-base with HEAD bounds a
// branch-style diff.
func resolveBaseMode(ctx context.Context, f *localFlags, branch string) (localMode, error) {
	mb, err := gitLine(ctx, "merge-base", f.baseRef, gitHeadRef)
	if err != nil {
		return localMode{}, &cli.RefusalError{Code: "bad-base", Message: fmt.Sprintf("no merge-base with '%s'", f.baseRef)}
	}
	head := gitLineOrEmpty(ctx, gitCmdRevParse, gitHeadRef)
	headRef := branch
	if headRef == "" {
		headRef = gitHeadRef
	}
	return localMode{kind: modeBranch, baseSHA: mb, headSHA: head, headRef: headRef, rangeVal: f.baseRef + "...HEAD"}, nil
}

// resolveStagedMode ports the staged mode: HEAD bounds the index diff.
func resolveStagedMode(ctx context.Context) (localMode, error) {
	base := gitLineOrEmpty(ctx, gitCmdRevParse, gitHeadRef)
	head := gitLineOrEmpty(ctx, "write-tree")
	return localMode{kind: modeStaged, baseSHA: base, headSHA: head, headRef: keyIndex, rangeVal: "HEAD:index"}, nil
}

// resolveWorktreeMode ports the default worktree mode: uncommitted HEAD
// changes with the branch (or worktree) as head ref.
func resolveWorktreeMode(ctx context.Context, branch string) localMode {
	base := gitLineOrEmpty(ctx, gitCmdRevParse, gitHeadRef)
	headRef := branch
	if headRef == "" {
		headRef = "worktree"
	}
	return localMode{kind: "worktree", baseSHA: base, headRef: headRef, rangeVal: "HEAD:worktree", dirty: true}
}

// resolveRangeMode ports the range split: three-dot wins when present,
// merging at the merge-base; otherwise a straight two-sided diff.
func resolveRangeMode(ctx context.Context, spec string) (localMode, error) {
	if strings.Contains(spec, gitRange3) {
		return resolveThreeDot(ctx, spec)
	}
	return resolveTwoDot(ctx, spec)
}

// resolveThreeDot ports the three-dot range: the merge-base bounds the
// diff, with both ends resolving.
func resolveThreeDot(ctx context.Context, spec string) (localMode, error) {
	ra, _, _ := strings.Cut(spec, gitRange3)
	_, rb, _ := strings.CutLast(spec, gitRange3)
	base, err := gitLine(ctx, gitCmdRevParse, flagVerify, ra)
	if err != nil {
		return localMode{}, &cli.RefusalError{Code: codeBadRange, Message: fmt.Sprintf("cannot resolve range base '%s'", ra)}
	}
	_ = base
	mb, err := gitLine(ctx, "merge-base", ra, rb)
	if err != nil {
		return localMode{}, &cli.RefusalError{Code: codeBadRange, Message: fmt.Sprintf("no merge-base for '%s'", spec)}
	}
	head, err := gitLine(ctx, gitCmdRevParse, flagVerify, rb+"^{commit}")
	if err != nil {
		return localMode{}, &cli.RefusalError{Code: codeBadRange, Message: fmt.Sprintf("cannot resolve range head '%s'", rb)}
	}
	return localMode{kind: modeRange, baseSHA: mb, headSHA: head, headRef: rb, rangeVal: spec}, nil
}

// resolveTwoDot ports the two-dot range: a straight two-sided diff, with
// both ends resolving.
func resolveTwoDot(ctx context.Context, spec string) (localMode, error) {
	ra, rb := spec, spec
	if strings.Contains(spec, "..") {
		ra, _, _ = strings.Cut(spec, "..")
		_, rb, _ = strings.CutLast(spec, "..")
	}
	base, err := gitLine(ctx, gitCmdRevParse, flagVerify, ra+"^{commit}")
	if err != nil {
		return localMode{}, &cli.RefusalError{Code: codeBadRange, Message: fmt.Sprintf("cannot resolve range base '%s'", ra)}
	}
	head, err := gitLine(ctx, gitCmdRevParse, flagVerify, rb+"^{commit}")
	if err != nil {
		return localMode{}, &cli.RefusalError{Code: codeBadRange, Message: fmt.Sprintf("cannot resolve range head '%s'", rb)}
	}
	return localMode{kind: modeRange, baseSHA: base, headSHA: head, headRef: rb, rangeVal: spec}, nil
}

// gitLine runs git, returning stdout stripped of trailing newlines. Stderr
// is discarded, like the shell's 2>/dev/null guards.
func gitLine(ctx context.Context, args ...string) (string, error) {
	res, err := runCmd(ctx, binGit, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(res.stdout, newline), nil
}

// gitLineOrEmpty runs git, reading "" on failure (the shell's `|| true`
// with captured output).
func gitLineOrEmpty(ctx context.Context, args ...string) string {
	line, err := gitLine(ctx, args...)
	if err != nil {
		return ""
	}
	return line
}

// truncateEmpty ports the shell's `> file` truncation before a guarded
// failure: the empty residue lands first so bundles match through every
// gate. A write error itself refuses (unreachable on a writable bundle).
func truncateEmpty(path string) *cli.RefusalError {
	if err := atomic.WriteFile(path, []byte{}); err != nil {
		return &cli.RefusalError{Code: codeFetchFailed, Message: fmt.Sprintf("could not write %s: %v", path, err)}
	}
	return nil
}

// localFileStats ports the file-list and numstat reads for the mode.
func localFileStats(ctx context.Context, mode *localMode, paths []string) (string, string) {
	var listRef, statRef []string
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
	if len(paths) > 0 {
		listRef = append(listRef, "--")
		listRef = append(listRef, paths...)
		statRef = append(statRef, "--")
		statRef = append(statRef, paths...)
	}
	fileList := ""
	if res, err := runCmd(ctx, binGit, append([]string{gitCmdDiff, "--no-renames", "--name-only"}, listRef...)...); err == nil {
		fileList = strings.TrimRight(res.stdout, newline)
	} else if res, err := runCmd(ctx, binGit, append([]string{gitCmdDiff, "--name-only"}, listRef...)...); err == nil {
		fileList = strings.TrimRight(res.stdout, newline)
	}
	numstat := ""
	if res, err := runCmd(ctx, binGit, append([]string{gitCmdDiff, "--numstat"}, statRef...)...); err == nil {
		numstat = strings.TrimRight(res.stdout, newline)
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

// localCounts ports the total/adds/dels derivation.
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

// countNonEmptyLines ports `grep -c .`.
func countNonEmptyLines(s string) int {
	n := 0
	for _, line := range splitLines(s) {
		if line != "" {
			n++
		}
	}
	return n
}

// intOrZero ports the numstat awk: plain-digit fields sum, anything else
// reads zero.
func intOrZero(s string) int {
	n := 0
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return 0
		}
		n = n*numBase10 + int(s[i]-'0')
	}
	return n
}

// hasNonEmptyLine ports `grep -q .`.
func hasNonEmptyLine(s string) bool {
	for _, line := range splitLines(s) {
		if line != "" {
			return true
		}
	}
	return false
}

// writeLocalUntrackedWarns ports the untracked-files advisory warning.
func writeLocalUntrackedWarns(ctx context.Context, r *cli.Runner, paths []string) {
	args := []string{"ls-files", "--others", "--exclude-standard"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	res, err := runCmd(ctx, binGit, args...)
	if err != nil {
		return
	}
	kept := untrackedKept(res.stdout)
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

// untrackedKept ports the untracked filter: blanks and the bundle
// directory never warn.
func untrackedKept(stdout string) []string {
	var kept []string
	for _, line := range splitLines(strings.TrimRight(stdout, newline)) {
		if line == "" || strings.HasPrefix(line, ".review/") {
			continue
		}
		kept = append(kept, line)
	}
	return kept
}

// untrackedShown ports the five-path display cap: words counted before
// each append, like the shell's wc -w gate.
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

// writeLocalBinaryWarns ports the binary-files advisory warning.
func writeLocalBinaryWarns(r *cli.Runner, numstat string) {
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

// binarySkipped ports the numstat dash-dash scan: binary files have no
// hunks and leave the anchors unchecked.
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

// binaryKept ports the non-empty filter: the shell counts `grep -c .`
// over the skipped list.
func binaryKept(numstat string) []string {
	var kept []string
	for _, s := range binarySkipped(numstat) {
		if s != "" {
			kept = append(kept, s)
		}
	}
	return kept
}

// writeLocalTOC ports the fetch-local toc.txt: the local title line, the
// numstat stat block, and the coverage manifest over file_list.
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

// omittedLocal ports the local omitted computation: file_list paths absent
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

// writeSourceJSON ports the source.json writer: the local bundle record.
func writeSourceJSON(
	dir string, req Request, mode *localMode, slug, diffSHA string,
	total, adds, dels int, toolVersion, model, now string,
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
		"repo", req.Repo,
		"slug", slug,
		keyBaseRef, nullStr(mode.baseRef()),
		keyHeadRef, mode.headRef,
		keyBaseSHA, nullStr(mode.baseSHA),
		keyHeadSHA, nullStr(mode.headSHA),
		modeRange, nullStr(mode.rangeVal),
		"dirty", mode.dirty,
		"diff_sha256", diffSHA,
		keyFiles, total,
		keyAdditions, adds,
		keyDeletions, dels,
		"tool_version", toolVersion,
		"model", modelVal,
		"created_at", now,
	)
	if err := atomic.WriteFile(dir+"/source.json", []byte(jq.PrettyOrdered(doc))); err != nil {
		return &cli.RefusalError{Code: codeFetchFailed, Message: fmt.Sprintf("could not write %s/source.json: %v", dir, err)}
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

// sha256Hex ports `shasum -a 256 | cut -d' ' -f1`.
func sha256Hex(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
