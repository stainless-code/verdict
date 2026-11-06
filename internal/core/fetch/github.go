package fetchcore

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// cliFileCap is the hosting CLI's file-list cap: a list arriving exactly
// here triggers provider paging.
const cliFileCap = 100

// providerFileCeiling is the provider listing ceiling the paged fetch
// serves.
const providerFileCeiling = 3000

// upstreamFileLimit is the host's own diff refusal threshold in files:
// diffs over this count are refused upstream, never rendered.
const upstreamFileLimit = 300

func runGitHub(
	ctx context.Context, ctx2 *fetchRunCtx, req *core.FetchRequest, f core.FetchFlags, budget *budgetState,
) (*core.FetchData, string, error) {
	r, dir, repo, pr := ctx2.runner, ctx2.dir, req.Repo, req.Pr
	if err := r.needTool("gh"); err != nil {
		return nil, "", err
	}
	if ref := githubFetch(ctx, ctx2, r, dir, repo, pr); ref != nil {
		return nil, "", ref
	}
	if ref := resolveControls(ctx, ctx2, repo, readcommon.PrField(dir, keyBaseSHA), f.HeadControls); ref != nil {
		return nil, "", ref
	}
	if ref := phaseTwo(ctx, ctx2, req, f, dir); ref != nil {
		return nil, "", ref
	}
	fc := resolveFetchCap(r, ctx2.runCfg.resolved)
	chunkActive, ref := checkDiffSize(r, ctx2.runCfg.resolved, dir+suffixFullDiff, fc, f.ChunkFlag)
	if ref != nil {
		return nil, "", ref
	}
	chunkJSON, incBase, ref := githubBundle(ctx, ctx2, req, f, dir, repo, pr, fc, chunkActive)
	if ref != nil {
		return nil, "", ref
	}
	return fetchTail(ctx, ctx2, req, f, budget, &fc, chunkJSON, incBase)
}

func githubFetch(ctx context.Context, ctx2 *fetchRunCtx, r *session, dir, repo, pr string) error {
	if ref := githubView(ctx, r, dir, repo, pr); ref != nil {
		return ref
	}
	if ref := githubDiff(ctx, r, dir, repo, pr); ref != nil {
		return ref
	}
	return pageFileList(ctx, ctx2, dir, repo, pr)
}

// githubView owns the `gh pr view` read: PR.json lands beside the
// bundle, and gh's own stderr reaches the terminal (no redirect).
func githubView(ctx context.Context, r *session, dir, repo, pr string) *core.RefusalError {
	fmt.Fprintf(r.Human, "» gh pr view %s#%s\n", repo, pr)
	viewRes, err := r.ghFetch(ctx, ghCmdPR, "view", pr, "--repo", repo,
		flagJSON, "number,title,body,baseRefName,headRefName,headRefOid,baseRefOid,author,url,commits,"+
			"files,additions,deletions,isDraft,reviewDecision,state")
	fmt.Fprint(r.Human, viewRes.Stderr)
	if err != nil {
		if ref := truncateEmpty(dir + suffixPRJSON); ref != nil {
			return ref
		}
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(
			"gh pr view failed for %s#%s (auth? PR number?)", repo, pr)}
	}
	if werr := atomic.WriteFile(dir+suffixPRJSON, []byte(viewRes.Stdout)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write %s/PR.json: %v", dir, werr)}
	}
	return nil
}

// githubDiff owns the diff read with its API fallback: the CLI diff
// first, then the API diff, refusing only when both fail.
func githubDiff(ctx context.Context, r *session, dir, repo, pr string) *core.RefusalError {
	fmt.Fprintf(r.Human, "» gh pr diff -> %s/full.diff\n", dir)
	diffRes, err := r.ghFetch(ctx, ghCmdPR, gitCmdDiff, pr, "--repo", repo)
	fmt.Fprint(r.Human, diffRes.Stderr)
	if err != nil {
		return githubAPIDiff(ctx, r, dir, repo, pr, diffRes.Stderr)
	}
	if werr := atomic.WriteFile(dir+suffixFullDiff, []byte(diffRes.Stdout)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteFullDiff, dir, werr)}
	}
	return nil
}

// githubAPIDiff owns the API diff fallback after a CLI diff failure.
func githubAPIDiff(ctx context.Context, r *session, dir, repo, pr, cliStderr string) *core.RefusalError {
	fmt.Fprintln(r.Human, "gh pr diff failed, trying API diff")
	apiRes, apiErr := r.ghFetch(ctx, ghCmdAPI, apiReposPrefix+repo+"/pulls/"+pr, flagH, "Accept: application/vnd.github.diff")
	fmt.Fprint(r.Human, apiRes.Stderr)
	if apiErr != nil {
		if ref := truncateEmpty(dir + suffixFullDiff); ref != nil {
			return ref
		}
		if upErr := upstreamDiffRefusal(cliStderr + newline + apiRes.Stderr); upErr != nil {
			return upErr
		}
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(
			"could not fetch diff for %s#%s via CLI or API", repo, pr)}
	}
	if werr := atomic.WriteFile(dir+suffixFullDiff, []byte(apiRes.Stdout)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteFullDiff, dir, werr)}
	}
	return nil
}

func githubBundle(
	ctx context.Context, ctx2 *fetchRunCtx, _ *core.FetchRequest, f core.FetchFlags,
	dir, repo, pr string, fc fetchCap, chunkActive bool,
) (any, string, error) {
	r := ctx2.runner
	if herr := buildHunksTSV(dir+suffixFullDiff, dir+"/hunks.tsv"); herr != nil {
		return nil, "", herr
	}
	if _, err := writeIdentityFile(ctx, r.commander, dir,
		readcommon.PrField(dir, keyHeadSHA), readcommon.PrField(dir, keyBaseSHA)); err != nil {
		return nil, "", err
	}
	if err := writeGitHubTOC(ctx, ctx2); err != nil {
		return nil, "", err
	}
	if ref := githubMiddle(ctx, ctx2, dir, repo, pr); ref != nil {
		return nil, "", ref
	}
	headSHA := readcommon.PrField(dir, keyHeadSHA)
	chunkJSON, ref := maybeWriteChunks(r.Diag, dir, fc.maxBytes, chunkActive, headSHA, readcommon.PrField(dir, keyBaseSHA), fc.source, r.privacy.Exclusions)
	if ref != nil {
		return nil, "", ref
	}
	incBase, ref := githubIncremental(ctx, r, dir, repo, f)
	if ref != nil {
		return nil, "", ref
	}
	return chunkJSON, incBase, nil
}

// githubMiddle owns the bundle middle: context lines, the claim store,
// the sensitive manifest, recall, and the review threads.
func githubMiddle(ctx context.Context, ctx2 *fetchRunCtx, dir, repo, pr string) error {
	r := ctx2.runner
	if ref := emitContext(ctx2); ref != nil {
		return ref
	}
	if err := ensureStore(dir); err != nil {
		return err
	}
	extra := prExtraPaths(dir + suffixPRJSON)
	if ref := writeSensitiveManifest(r, dir, extra); ref != nil {
		return ref
	}
	writeKBRecall(r, dir, extra)
	if ref := githubReviews(ctx, r, dir, repo, pr); ref != nil {
		return ref
	}
	return nil
}

// githubReviews owns the review reads: reviews.json and inline.json land as
// raw API bodies, and threads.json comes from the authoritative GraphQL
// reviewThreads read (REST carries no resolution). A failed read writes []
// but warns: a silent empty list would read as "no prior threads" on a
// re-review.
func githubReviews(ctx context.Context, r *session, dir, repo, pr string) *core.RefusalError {
	fmt.Fprintln(r.Human, "» reviews + inline comments")
	truncated := false
	reviewsBody := emptyJSONArray
	if res, err := r.ghFetch(ctx, ghCmdAPI, apiReposPrefix+repo+"/pulls/"+pr+"/reviews", flagPaginate); err == nil {
		reviewsBody = res.Stdout
	} else {
		truncated = true
		r.Warn("github review read failed — reviews.json is empty; re-fetch after confirming gh auth")
	}
	if werr := atomic.WriteFile(dir+suffixReviewsJSON, []byte(reviewsBody)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteReviews, dir, werr)}
	}
	inlineBody := emptyJSONArray
	if res, err := r.ghFetch(ctx, ghCmdAPI, apiReposPrefix+repo+"/pulls/"+pr+"/comments", flagPaginate); err == nil {
		inlineBody = res.Stdout
	} else {
		truncated = true
		r.Warn("github inline-comment read failed — inline.json is empty; re-fetch after confirming gh auth")
	}
	if werr := atomic.WriteFile(dir+suffixInlineJSON, []byte(inlineBody)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteInline, dir, werr)}
	}
	feed, feedTruncated, feedErr := githubThreadFeed(ctx, r, repo, pr)
	threadWarning(r, providerGitHub, feed, feedTruncated, feedErr)
	return writeThreadsJSON(dir, providerGitHub, pr, readcommon.PrField(dir, keyHeadSHA), r.nowUTC(),
		truncated || feedTruncated, feed)
}

// githubIncremental owns the --base-sha compare: the incremental diff
// and its note land beside the bundle, or the run continues without one.
func githubIncremental(ctx context.Context, r *session, dir, repo string, f core.FetchFlags) (string, error) {
	incBase := nullStr
	if !f.HasBase || f.BaseSHA == "" {
		return incBase, nil
	}
	headSHA := readcommon.PrField(dir, keyHeadSHA)
	fmt.Fprintf(r.Human, "» compare %s...%s -> %s/incremental.diff\n", f.BaseSHA, headSHA, dir)
	res, err := r.ghFetch(ctx, ghCmdAPI, apiReposPrefix+repo+"/compare/"+f.BaseSHA+gitRange3+headSHA,
		flagH, "Accept: application/vnd.github.diff")
	if err != nil {
		return incBase, githubIncrementalFail(r, dir, res.Stderr)
	}
	if werr := writeErrFile(dir+"/.compare.err", ""); werr != nil {
		return incBase, werr
	}
	if werr := atomic.WriteFile(dir+"/incremental.diff", []byte(res.Stdout)); werr != nil {
		return incBase, &core.RefusalError{
			Code:    CodeFetchFailed,
			Message: fmt.Sprintf("could not write %s/incremental.diff: %v", dir, werr),
		}
	}
	return f.BaseSHA, nil
}

// githubIncrementalFail owns the failed-compare fork: the stderr
// sidecar lands, then the unavailability note names the cause.
func githubIncrementalFail(r *session, dir, stderr string) error {
	if werr := writeErrFile(dir+"/.compare.err", stderr); werr != nil {
		return werr
	}
	if nref := githubIncrementalNote(r, dir, stderr); nref != nil {
		return nref
	}
	return nil
}

func githubIncrementalNote(r *session, dir, stderr string) *core.RefusalError {
	head := stderr
	if len(head) > compareErrHeadBytes {
		head = head[:compareErrHeadBytes]
	}
	note := "incrementalUnavailable: " + strings.TrimRight(head, newline)
	if werr := atomic.WriteFile(dir+"/incremental.diff.note", []byte(note+newline)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write %s/incremental.diff.note: %v", dir, werr)}
	}
	fmt.Fprintln(r.Human, note)
	_ = os.Remove(dir + "/incremental.diff")
	return nil
}

// writeGitHubTOC owns the GitHub write_toc_roles call: the PR title line,
// the gh files stat, and the coverage manifest.
func writeGitHubTOC(ctx context.Context, ctx2 *fetchRunCtx) error {
	total, statLines := githubStat(ctx, ctx2)
	withHunks, derr := diffFiles(ctx2.dir+suffixFullDiff, ctx2.dir)
	if derr != nil {
		return derr
	}
	listed := prFilePaths(ctx2.dir + suffixPRJSON)
	omitted := omittedPaths(listed, withHunks)
	return writeTocRolesGitHub(ctx2, total, statLines, withHunks, omitted)
}

func githubStat(ctx context.Context, ctx2 *fetchRunCtx) (string, []string) {
	total := "?"
	if raw, err := os.ReadFile(ctx2.dir + suffixPRJSON); err == nil {
		if doc, derr := jq.Decode(raw); derr == nil {
			if m, ok := jq.AsMap(doc); ok {
				total = strconv.Itoa(len(jq.Elements(m[keyFiles])))
			}
		} else {
			total = "?"
		}
	}
	res, err := ctx2.runner.ghFetch(ctx, ghCmdPR, "view", ctx2.pr, "--repo", ctx2.repo,
		flagJSON, keyFiles, "--jq", `.files[] | "\(.path) (+\(.additions)/-\(.deletions))"`)
	if err != nil {
		return total, nil
	}
	lines := append([]string{}, splitLines(res.Stdout)...)
	return total, lines
}

func fetchPrepare(ctx2 *fetchRunCtx, req *core.FetchRequest, f core.FetchFlags) (*budgetState, string, error) {
	rc, ref := resolvePhaseOne(ctx2.env, req.SettingsPath, f.ChunkFlag)
	if ref != nil {
		return nil, "", ref
	}
	ctx2.runCfg = rc
	rc.root = req.RootPath()
	for _, warn := range rc.envWarns {
		ctx2.runner.Warn(warn)
	}
	ctx2.runner.privacy = rc.resolved.Privacy
	budget := resolveBudget(ctx2.runner, rc.resolved)
	if err := budgetGate(ctx2.runner, &budget, rc.resolved, f.BudgetConsent); err != nil {
		return nil, "", err
	}
	dir := ".review/" + req.Pr
	ctx2.dir = dir
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, "", &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not create %s: %v", dir, err)}
	}
	return &budget, dir, nil
}

// phaseTwo resolves the full repository policy once the base commit is
// known and re-gates the spend with the repository layer in force: an
// early provisional gate cannot see repository policy, so the full gate
// repeats here before the bundle builds. Without base policy the
// phase-two resolution replays the phase-one layers exactly, so the
// re-gate is skipped (one gate, one estimate line, one tail receipt);
// with policy the authoritative gate repeats and the tail records the
// single fresh receipt.
func phaseTwo(ctx context.Context, ctx2 *fetchRunCtx, req *core.FetchRequest, f core.FetchFlags, dir string) *core.RefusalError {
	baseSHA := readcommon.PrField(dir, keyBaseSHA)
	if ref := ctx2.runCfg.resolvePhaseTwo(ctx, ctx2.runner.commander, req.Repo, baseSHA, f.ChunkFlag, nil, req.SettingsPath, legacyInputs(ctx2.runner)); ref != nil {
		return ref
	}
	resolved := ctx2.runCfg.resolved
	ctx2.runner.privacy = resolved.Privacy
	if ctx2.runCfg.base == nil || !ctx2.runCfg.base.Present {
		return nil
	}
	budget := resolveBudget(ctx2.runner, resolved)
	if gateErr := budgetGate(ctx2.runner, &budget, resolved, f.BudgetConsent); gateErr != nil {
		return budgetRefusal(gateErr)
	}
	return nil
}

func prExtraPaths(prJSON string) string {
	raw, err := os.ReadFile(prJSON)
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
	var out []string
	for _, f := range jq.Elements(m[keyFiles]) {
		out = append(out, extraPathsOf(f)...)
	}
	return strings.Join(out, newline)
}

// extraPathsOf owns the per-file extra-path read: the path plus any
// previous filename (renames), skipped when the entry is not an object.
func extraPathsOf(f any) []string {
	fm, ok := f.(map[string]any)
	if !ok {
		return nil
	}
	out := []string{jq.R(fm[keyPath])}
	if prev, has := fm["previous_filename"]; has && prev != nil {
		out = append(out, jq.R(prev))
	}
	return out
}

var upstreamRefusalRe = regexp.MustCompile(`(?i)too large|not rendered|300 files?|maximum.*?files?`)

// upstreamDiffRefusal names an upstream the host refused: when both diff
// reads fail and the
// host names its own refusal, surface the typed upstream refusal with the
// range-narrowing recovery hint — never the byte-cap refusal and never a
// generic fetch failure. Anything else reads nil (the caller refuses
// fetch-failed).
func upstreamDiffRefusal(combined string) *core.RefusalError {
	if !upstreamRefusalRe.MatchString(combined) {
		return nil
	}
	return &core.RefusalError{Code: "upstream-diff-too-large", Message: fmt.Sprintf(
		"upstream refused the diff over %d files (host will not render it) — re-fetch narrowed to a range "+
			"via --base-sha/--since, or narrow with a paths filter, then re-run", upstreamFileLimit)}
}
