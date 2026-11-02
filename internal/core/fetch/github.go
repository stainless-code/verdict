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
)

// cliFileCap is the hosting CLI's file-list cap: a list arriving exactly
// here triggers provider paging (audit 18).
const cliFileCap = 100

// providerFileCeiling is the provider listing ceiling the paged fetch
// serves (audit 18).
const providerFileCeiling = 3000

// upstreamFileLimit is the host's own diff refusal threshold in files:
// diffs over this count are refused upstream, never rendered (audit 20).
const upstreamFileLimit = 300

// runGitHub mirrors the GitHub fetch path.
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
	fc, ref := resolveFetchCap(r)
	if ref != nil {
		return nil, "", ref
	}
	chunkActive, ref := checkDiffSize(r, dir+suffixFullDiff, fc, f.ChunkFlag)
	if ref != nil {
		return nil, "", ref
	}
	chunkJSON, incBase, ref := githubBundle(ctx, ctx2, req, f, dir, repo, pr, fc, chunkActive)
	if ref != nil {
		return nil, "", ref
	}
	return fetchTail(ctx2, req, f, budget, &fc, chunkJSON, incBase)
}

// githubFetch ports the GitHub view/diff/file-list reads: PR.json, the
// full diff with its API fallback, and the audit-18 paged file listing.
func githubFetch(ctx context.Context, ctx2 *fetchRunCtx, r *session, dir, repo, pr string) error {
	if ref := githubView(ctx, r, dir, repo, pr); ref != nil {
		return ref
	}
	if ref := githubDiff(ctx, r, dir, repo, pr); ref != nil {
		return ref
	}
	return pageFileList(ctx, ctx2, dir, repo, pr)
}

// githubView ports the `gh pr view` read: PR.json lands beside the
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

// githubDiff ports the diff read with its API fallback: the CLI diff
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

// githubAPIDiff ports the API diff fallback after a CLI diff failure.
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

// githubBundle ports the post-cap bundle derivation: hunks, TOC/roles,
// context, store, sensitive manifest, recall, review threads, chunks, and
// the incremental diff. Returns the envelope chunk summary and base.
func githubBundle(
	ctx context.Context, ctx2 *fetchRunCtx, _ *core.FetchRequest, f core.FetchFlags,
	dir, repo, pr string, fc fetchCap, chunkActive bool,
) (any, string, error) {
	r := ctx2.runner
	if herr := buildHunksTSV(dir+suffixFullDiff, dir+"/hunks.tsv"); herr != nil {
		return nil, "", herr
	}
	if err := writeGitHubTOC(ctx, ctx2); err != nil {
		return nil, "", err
	}
	if ref := githubMiddle(ctx, ctx2, dir, repo, pr); ref != nil {
		return nil, "", ref
	}
	headSHA := prField(dir, keyHeadSHA)
	chunkJSON, ref := maybeWriteChunks(r.Diag, dir, fc.maxBytes, chunkActive, headSHA, prField(dir, keyBaseSHA), fc.source)
	if ref != nil {
		return nil, "", ref
	}
	incBase, ref := githubIncremental(ctx, r, dir, repo, pr, f)
	if ref != nil {
		return nil, "", ref
	}
	return chunkJSON, incBase, nil
}

// githubMiddle ports the bundle middle: context lines, the claim store,
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

// githubReviews ports the review-thread reads: reviews.json and
// inline.json land as raw API bodies, defaulting to [] on failure.
func githubReviews(ctx context.Context, r *session, dir, repo, pr string) *core.RefusalError {
	fmt.Fprintln(r.Human, "» reviews + inline comments")
	reviewsBody := emptyJSONArray
	if res, err := r.ghFetch(ctx, ghCmdAPI, apiReposPrefix+repo+"/pulls/"+pr+"/reviews", flagPaginate); err == nil {
		reviewsBody = res.Stdout
	}
	if werr := atomic.WriteFile(dir+suffixReviewsJSON, []byte(reviewsBody)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteReviews, dir, werr)}
	}
	inlineBody := emptyJSONArray
	if res, err := r.ghFetch(ctx, ghCmdAPI, apiReposPrefix+repo+"/pulls/"+pr+"/comments", flagPaginate); err == nil {
		inlineBody = res.Stdout
	}
	if werr := atomic.WriteFile(dir+suffixInlineJSON, []byte(inlineBody)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgWriteInline, dir, werr)}
	}
	return nil
}

// githubIncremental ports the --base-sha compare: the incremental diff
// and its note land beside the bundle, or the run continues without one.
func githubIncremental(ctx context.Context, r *session, dir, repo string, _ string, f core.FetchFlags) (string, error) {
	incBase := nullStr
	if !f.HasBase || f.BaseSHA == "" {
		return incBase, nil
	}
	headSHA := prHeadRefOid(dir)
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

// githubIncrementalFail ports the failed-compare fork: the stderr
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

// githubIncrementalNote ports the failed-compare note: the first 300
// stderr bytes (command substitution, no tr) name the unavailability.
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

func prHeadRefOid(dir string) string {
	raw, err := os.ReadFile(dir + suffixPRJSON)
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
	return jq.R(m["headRefOid"])
}

// writeGitHubTOC ports the GitHub write_toc_roles call: the PR title line,
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

// githubStat ports the toc stat block: total plus the gh files stat lines
// (a failing gh call degrades to no stat lines, like `|| true`).
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

// prField ports pr_field for the tail's head/base reads.
func prField(dir, field string) string {
	raw, err := os.ReadFile(dir + suffixPRJSON)
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
	prov := jq.StrOf(m[keyProvider])
	if prov == "" {
		return githubPrField(m, field)
	}
	return neutralPrField(m, field)
}

// githubPrField reads the GitHub-shaped record field.
func githubPrField(m map[string]any, field string) string {
	switch field {
	case keyHeadSHA:
		return jq.R(m["headRefOid"])
	case keyBaseSHA:
		return jq.R(m["baseRefOid"])
	case keyState:
		return asciiLower(jq.R(m[keyState]))
	case keyAuthor:
		if a, ok := m[keyAuthor].(map[string]any); ok {
			return jq.R(a[keyLogin])
		}
		return ""
	default:
		return ""
	}
}

// neutralPrField reads the normalized non-GitHub record field.
func neutralPrField(m map[string]any, field string) string {
	switch field {
	case keyHeadSHA, keyBaseSHA, keyState:
		return jq.R(m[field])
	case keyAuthor:
		return jq.R(m[keyAuthor])
	default:
		return ""
	}
}

// prExtraPaths ports `jq -r '.files[] | .path, (.previous_filename //
// empty)'` for the sensitive/recall extra inputs.
// fetchPrepare ports the fetch preamble: budget resolution and gate,
// the bundle directory, and the budget receipt.
func fetchPrepare(ctx2 *fetchRunCtx, req *core.FetchRequest, f core.FetchFlags) (*budgetState, string, error) {
	budget, ref := resolveBudget(ctx2.env)
	if ref != nil {
		return nil, "", ref
	}
	if err := budgetGate(ctx2.runner, &budget, f.BudgetConsent); err != nil {
		return nil, "", err
	}
	dir := ".review/" + req.Pr
	ctx2.dir = dir
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, "", &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not create %s: %v", dir, err)}
	}
	writeBudgetReceipt(ctx2.runner, &budget, dir, core.NowUTC(ctx2.runner.Now()))
	return &budget, dir, nil
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

// extraPathsOf ports the per-file extra-path read: the path plus any
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

// upstreamRefusalRe matches the host's own over-file-count diff refusal
// (audit 20): the upstream will not render the diff at all, distinct from
// the tool's byte cap. The 300-file upstream limit rides in the message the
// caller reports.
var upstreamRefusalRe = regexp.MustCompile(`(?i)too large|not rendered|300 files?|maximum.*?files?`)

// upstreamDiffRefusal ports audit 20: when both diff reads fail and the
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
