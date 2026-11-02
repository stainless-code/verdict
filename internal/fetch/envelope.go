package fetch

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
)

// fetchTail ports fetch_tail: the human done/next lines plus the --json
// envelope. incBase is nullStr or the incremental base SHA.
func fetchTail(
	ctx2 *fetchRunCtx, req Request, f fetchFlags, budget *budgetState,
	fcap *fetchCap, chunkJSON any, incBase string,
) (int, error) {
	r, dir := ctx2.runner, ctx2.dir
	if r.JSONMode {
		return 0, writeFetchTailJSON(ctx2, req, f, budget, fcap, chunkJSON, incBase)
	}
	stats, err := readFetchStats(dir)
	if err != nil {
		return 0, err
	}
	writeFetchDone(r, dir, stats.filesStr)
	if f.showPrompts {
		return 0, writePromptDump(ctx2)
	}
	return 0, nil
}

// writeFetchTailJSON ports the --json tail: the staged dump, the progress
// lines, and the single envelope.
func writeFetchTailJSON(
	ctx2 *fetchRunCtx, req Request, f fetchFlags, budget *budgetState,
	fcap *fetchCap, chunkJSON any, incBase string,
) error {
	dump, hasDump, err := stageDump(ctx2, f.showPrompts)
	if err != nil {
		return err
	}
	stats, err := readFetchStats(ctx2.dir)
	if err != nil {
		return err
	}
	writeFetchDone(ctx2.runner, ctx2.dir, stats.filesStr)
	env := fetchEnvelope(ctx2, req, &stats, budget, fcap, chunkJSON, incBase, dump, hasDump)
	fmt.Fprint(ctx2.runner.Out, jq.PrettyOrdered(env))
	return nil
}

// stageDump renders the prompt dump before progress, so a dump refusal
// precedes the done/next lines like the shell's temp-file stage.
func stageDump(ctx2 *fetchRunCtx, show bool) (string, bool, error) {
	if !show {
		return "", false, nil
	}
	d, err := renderPromptDump(ctx2)
	if err != nil {
		return "", false, err
	}
	return d, true, nil
}

// writePromptDump prints the plain-mode prompt dump to stdout.
func writePromptDump(ctx2 *fetchRunCtx) error {
	d, err := renderPromptDump(ctx2)
	if err != nil {
		return err
	}
	fmt.Fprint(ctx2.runner.Out, d)
	return nil
}

// writeFetchDone ports the human done/next lines.
func writeFetchDone(r *cli.Runner, dir, filesStr string) {
	fmt.Fprintf(r.Human, "done: %s/PR.json %s/full.diff %s/toc.txt %s/roles.txt (%s files)\n", dir, dir, dir, dir, filesStr)
	fmt.Fprintf(r.Human, "next: feed SKILL.md + prompts/system.md + prompts/review.md + prompts/diagrams.md + "+
		"%s/PR.json + %s/full.diff (+ %s/roles.txt) to any agent; ask it to write %s/review.json\n", dir, dir, dir, dir)
}

// fetchStats carries the tail's bundle measurements.
type fetchStats struct {
	filesStr string
	files    any
	adds     any
	dels     any
	omitted  []any
	diffSize int64
	hunks    int
}

// readFetchStats reads the tail measurements, refusing fetch-failed when
// the record is undecodable (the shell's jq dies here with no envelope —
// the typed refusal is the documented divergence).
func readFetchStats(dir string) (fetchStats, error) {
	var st fetchStats
	raw, err := os.ReadFile(dir + suffixPRJSON)
	if err != nil {
		return st, &cli.RefusalError{Code: codeFetchFailed, Message: fmt.Sprintf("missing %s/PR.json (fetch the PR first)", dir)}
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return st, &cli.RefusalError{Code: codeFetchFailed, Message: fmt.Sprintf(msgUnreadablePR, dir)}
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return st, &cli.RefusalError{Code: codeFetchFailed, Message: fmt.Sprintf(msgUnreadablePR, dir)}
	}
	if err := statFiles(&st, dir, m); err != nil {
		return st, err
	}
	adds, ok := envelopeNum(m[keyAdditions])
	if !ok {
		return st, &cli.RefusalError{Code: codeFetchFailed, Message: fmt.Sprintf(msgUnreadablePR, dir)}
	}
	dels, ok := envelopeNum(m[keyDeletions])
	if !ok {
		return st, &cli.RefusalError{Code: codeFetchFailed, Message: fmt.Sprintf(msgUnreadablePR, dir)}
	}
	st.adds, st.dels = adds, dels
	st.hunks = countHunkLines(dir + suffixFullDiff)
	st.omitted = toJSONArray(joinLines(omittedPaths(prPaths(m), readDiffHunks(dir))))
	if fi, err := os.Stat(dir + suffixFullDiff); err == nil {
		st.diffSize = fi.Size()
	}
	return st, nil
}

// envelopeNum ports `--argjson` for top-level stats: numbers pass, null
// and false read 0, anything else fails the envelope like the shell's jq.
func envelopeNum(v any) (any, bool) {
	if v == nil || v == false {
		return 0, true
	}
	switch t := v.(type) {
	case jq.Number:
		return t, true
	case json.Number:
		return jq.Number(t.String()), true
	case float64:
		return t, true
	case int, int64:
		return t, true
	default:
		return nil, false
	}
}

// prPaths reads the PR.json file paths ("" for undecodable entries is
// handled by the caller refusing first).
func prPaths(m map[string]any) []string {
	var out []string
	for _, f := range jq.Elements(m[keyFiles]) {
		if fm, ok := f.(map[string]any); ok {
			out = append(out, jq.R(fm[keyPath]))
		} else if om, ok := f.(*jq.Obj); ok {
			if v, has := om.Get(keyPath); has {
				out = append(out, jq.R(v))
			}
		}
	}
	return out
}

// countHunkLines ports `{ grep '^@@' ...; } | wc -l`.
func countHunkLines(fullDiff string) int {
	n := 0
	for _, line := range splitLines(readOptional(fullDiff)) {
		if strings.HasPrefix(line, "@@") {
			n++
		}
	}
	return n
}

// panelObj ports the envelope's panel summary.
func panelObj(ctx2 *fetchRunCtx) *jq.Obj {
	source := any(nil)
	if ctx2.panelSrc != "" {
		source = ctx2.panelSrc
	}
	members := make([]any, len(ctx2.panelList))
	for i, p := range ctx2.panelList {
		members[i] = p
	}
	return jq.NewObj(keySource, source, "members", members)
}

// sensitiveVal ports the sensitive.txt artifact (path or null).
func sensitiveVal(dir string) any {
	if _, err := os.Stat(dir + "/sensitive.txt"); err == nil {
		return dir + "/sensitive.txt"
	}
	return nil
}

// fetchEnvelope builds the fetch --json envelope with shell key order.
func fetchEnvelope(
	ctx2 *fetchRunCtx, req Request, st *fetchStats, budget *budgetState,
	fcap *fetchCap, chunkJSON any, incBase, dump string, hasDump bool,
) *jq.Obj {
	artifacts := jq.NewObj(
		"PR.json", ctx2.dir+suffixPRJSON,
		"full.diff", ctx2.dir+suffixFullDiff,
		"toc.txt", ctx2.dir+"/toc.txt",
		"roles.txt", ctx2.dir+"/roles.txt",
		"reviews.json", ctx2.dir+suffixReviewsJSON,
		"inline.json", ctx2.dir+suffixInlineJSON,
		"sensitive.txt", sensitiveVal(ctx2.dir),
		"incremental.diff", incrementalVal(ctx2.dir, incBase),
	)
	if _, err := os.Stat(ctx2.dir + suffixKBTxt); err == nil {
		artifacts.Set("kb.txt", ctx2.dir+suffixKBTxt)
	}
	if _, err := os.Stat(ctx2.dir + suffixKBRecall); err == nil {
		artifacts.Set("kb-recall.json", ctx2.dir+suffixKBRecall)
	}
	data := jq.NewObj(
		"dir", ctx2.dir,
		keyHeadSHA, prField(ctx2.dir, keyHeadSHA),
		keyBaseSHA, prField(ctx2.dir, keyBaseSHA),
		keyFiles, st.files,
		keyAdditions, st.adds,
		keyDeletions, st.dels,
		"hunks", st.hunks,
		"omitted", st.omitted,
		"diff_bytes", st.diffSize,
		"panel", panelObj(ctx2),
		"artifacts", artifacts,
		"incremental_base", incrementalBase(incBase),
	)
	addEnvelopeExtras(data, budget, fcap, chunkJSON)
	if hasDump {
		data.Set("prompt_dump", dump)
	}
	return jq.NewObj(
		keySchema, schemaV1,
		"verb", "fetch",
		"repo", req.Repo,
		keyPR, cli.PrJSON(req.Pr),
		"at", cli.NowUTC(ctx2.runner.Now()),
		"next", "feed SKILL.md + prompts/system.md + prompts/review.md + prompts/diagrams.md + "+
			ctx2.dir+"/PR.json + full.diff to any agent; ask it to write "+ctx2.dir+"/review.json",
		"warnings", cliStrings(ctx2.runner.Warnings),
		keyData, data,
		"error", nil,
	)
}

// incrementalVal ports the incremental.diff artifact (path or null).
func incrementalVal(dir, incBase string) any {
	if incBase == nullStr {
		return nil
	}
	return dir + "/incremental.diff"
}

// incrementalBase ports the incremental_base datum.
func incrementalBase(incBase string) any {
	if incBase == nullStr {
		return nil
	}
	return incBase
}

// statFiles ports the file-count read: an array counts, a missing or
// null list reads zero, and a non-array list refuses (the shell's jq
// shape gate).
func statFiles(st *fetchStats, dir string, m map[string]any) error {
	filesVal, hasFiles := m[keyFiles]
	if arr, ok := filesVal.([]any); hasFiles && filesVal != nil && ok {
		st.filesStr = strconv.Itoa(len(arr))
		st.files = len(arr)
		return nil
	}
	if !hasFiles || filesVal == nil {
		st.filesStr, st.files = "0", 0
		return nil
	}
	return &cli.RefusalError{Code: codeFetchFailed, Message: fmt.Sprintf(msgUnreadablePR, dir)}
}

// addEnvelopeExtras ports the conditional estimate/budget/cap/chunk keys.
func addEnvelopeExtras(data *jq.Obj, budget *budgetState, fcap *fetchCap, chunkJSON any) {
	if budget.hasBudget {
		data.Set("estimate", budget.estimateJSON)
		data.Set("budget", budget.budgetJSON)
	}
	if fcap.json != nil {
		data.Set("cap", fcap.json)
	}
	if chunkJSON != nil {
		data.Set("chunk", chunkJSON)
	}
}

// cliStrings converts warnings to JSON string values.
func cliStrings(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

// readDiffHunks reads the coverage-manifest hunked set from .difffiles
// (written by the toc step; no rewrite, just the read).
func readDiffHunks(dir string) []string {
	raw := readOptional(dir + "/.difffiles")
	var out []string
	for _, line := range splitLines(raw) {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// localEnvelopeData ports the local envelope's data block.
func localEnvelopeData(ctx2 *fetchRunCtx, mode *localMode, listed string, total, adds, dels int) *jq.Obj {
	dir := ctx2.dir
	withHunks := readDiffHunks(dir)
	omitted := omittedLocal(listed, withHunks)
	var diffSize int64
	if fi, err := os.Stat(dir + suffixFullDiff); err == nil {
		diffSize = fi.Size()
	}
	return jq.NewObj(
		"dir", dir,
		keyKind, mode.kind,
		keyHeadSHA, mode.headSHA,
		keyBaseSHA, mode.baseSHA,
		keyFiles, total,
		keyAdditions, adds,
		keyDeletions, dels,
		"hunks", countHunkLines(dir+suffixFullDiff),
		"omitted", toJSONArray(joinLines(omitted)),
		"diff_bytes", diffSize,
		"panel", panelObj(ctx2),
		"artifacts", localArtifacts(dir),
	)
}

// localArtifacts ports the local artifact map, adding the recall stores
// only when present (fixed key order, like the shell's jq object).
func localArtifacts(dir string) *jq.Obj {
	artifacts := jq.NewObj(
		"source.json", dir+"/source.json",
		"full.diff", dir+suffixFullDiff,
		"toc.txt", dir+"/toc.txt",
		"roles.txt", dir+"/roles.txt",
		"sensitive.txt", sensitiveVal(dir),
	)
	addArtifactIfPresent(artifacts, dir, "kb.txt", suffixKBTxt)
	addArtifactIfPresent(artifacts, dir, "kb-recall.json", suffixKBRecall)
	return artifacts
}

// addArtifactIfPresent adds one optional artifact path.
func addArtifactIfPresent(obj *jq.Obj, dir, key, suffix string) {
	if _, err := os.Stat(dir + suffix); err == nil {
		obj.Set(key, dir+suffix)
	}
}

// fetchTailLocal ports the fetch-local tail: done/next lines print before
// chunking, and the envelope carries kind instead of incremental_base.
func fetchTailLocal(
	ctx2 *fetchRunCtx, req Request, f *localFlags, budget *budgetState,
	fcap *fetchCap, chunkJSON any, slug string, mode *localMode,
	listed string, total, adds, dels int, _, now string,
) (int, error) {
	r := ctx2.runner
	if !r.JSONMode {
		if !f.showPrompts {
			return 0, nil
		}
		return 0, writePromptDump(ctx2)
	}
	dump, hasDump, err := stageDump(ctx2, f.showPrompts)
	if err != nil {
		return 0, err
	}
	data := localEnvelopeData(ctx2, mode, listed, total, adds, dels)
	addEnvelopeExtras(data, budget, fcap, chunkJSON)
	if hasDump {
		data.Set("prompt_dump", dump)
	}
	env := jq.NewObj(
		keySchema, schemaV1,
		"verb", "fetch-local",
		"repo", req.Repo,
		keyPR, cli.PrJSON(slug),
		"at", now,
		"next", "feed SKILL.md + prompts/system.md + prompts/review.md + prompts/diagrams.md + "+
			ctx2.dir+"/source.json + full.diff to any agent; ask it to write "+ctx2.dir+"/review.json",
		"warnings", cliStrings(r.Warnings),
		keyData, data,
		"error", nil,
	)
	fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	return 0, nil
}
