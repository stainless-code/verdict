package fetchcore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/core/threads"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// fetchTail writes the human done/next lines plus the typed
// data for the adapter's envelope. incBase is nullStr or the incremental
// base SHA. The dump rides in Data.PromptDump for the adapter to own on
// stdout (human mode) or in the envelope (JSON mode). The typed run
// bundle persists first, so the prompt dump already carries the combined
// plan and every model-facing export already withholds privacy paths.
func fetchTail(
	ctx context.Context, ctx2 *fetchRunCtx, req *core.FetchRequest, f *core.FetchFlags, budget *budgetState,
	fcap *fetchCap, chunkJSON any, incBase string,
) (*core.FetchData, string, error) {
	if ref := finalizeRemoteRun(ctx, ctx2, req, f); ref != nil {
		return nil, "", ref
	}
	in := impactInput{index: f.Index, head: readcommon.PrField(ctx2.dir, keyHeadSHA)}
	if err := writeEvidence(ctx, ctx2, in, f.Context, req.TimeUTC()); err != nil {
		return nil, "", err
	}
	writeTailReceipt(ctx2, budget)
	s, dir := ctx2.runner, ctx2.dir
	next := "feed SKILL.md + prompts/system.md + prompts/review.md + prompts/diagrams.md + " +
		dir + "/PR.json + " + diffFeed(dir, chunkJSON != nil, fullDiffFile) + impactFeed(dir, f.Index) + contextFeed(dir, f.Context) +
		" to any agent; ask it to write " + dir + "/review.json" +
		threadsHint(dir)
	if req.JSONMode {
		return writeFetchTailJSON(ctx2, f, budget, fcap, chunkJSON, incBase, next)
	}
	stats, err := readFetchStats(dir)
	if err != nil {
		return nil, "", err
	}
	writeFetchDone(s, dir, stats.filesStr, chunkJSON != nil, f.Index)
	data := fetchData(ctx2, &stats, budget, fcap, chunkJSON, incBase, nil)
	data.Capabilities = capabilityWire(f.Provider)
	if f.ShowPrompts {
		dump, err := renderPromptDump(ctx2)
		if err != nil {
			return nil, "", err
		}
		data.PromptDump = &dump
	}
	return data, next, nil
}

func writeFetchTailJSON(
	ctx2 *fetchRunCtx, f *core.FetchFlags, budget *budgetState,
	fcap *fetchCap, chunkJSON any, incBase, next string,
) (*core.FetchData, string, error) {
	dump, hasDump, err := stageDump(ctx2, f.ShowPrompts)
	if err != nil {
		return nil, "", err
	}
	stats, err := readFetchStats(ctx2.dir)
	if err != nil {
		return nil, "", err
	}
	writeFetchDone(ctx2.runner, ctx2.dir, stats.filesStr, chunkJSON != nil, f.Index)
	var dumpPtr *string
	if hasDump {
		dumpPtr = &dump
	}
	data := fetchData(ctx2, &stats, budget, fcap, chunkJSON, incBase, dumpPtr)
	data.Capabilities = capabilityWire(f.Provider)
	return data, next, nil
}

// stageDump renders the prompt dump before progress, so a dump refusal
// precedes the done/next lines.
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

func writeFetchDone(r *session, dir, filesStr string, chunked bool, index string) {
	shown := r.display(dir)
	fmt.Fprintf(r.Human, "done: %s/PR.json %s/full.diff %s/toc.txt %s/roles.txt (%s files)\n", shown, shown, shown, shown, filesStr)
	fmt.Fprintf(r.Human, "next: feed SKILL.md + prompts/system.md + prompts/review.md + prompts/diagrams.md + "+
		"%s/PR.json + %s%s (+ %s/roles.txt) to any agent; ask it to write %s/review.json%s\n",
		dir, diffFeed(dir, chunked, dir+"/full.diff"), impactFeed(dir, index), dir, dir, threadsHint(dir))
}

// threadsHint names the normalized conversation feed when the PR carries
// threads, so the driving agent reads prior feedback instead of reverse-
// engineering the raw provider files. Empty when there are no threads.
func threadsHint(dir string) string {
	if threadscore.CountFile(dir+suffixThreadsJSON) == 0 {
		return ""
	}
	return "; prior threads are in " + dir + "/threads.json (run verdict threads to refresh)"
}

// diffFeed names the diff input of the next step: the whole diff, or — once
// the diff was chunked past the cap — the manifest and the chunks it lists
// (feeding full.diff would defeat the cap).
func diffFeed(dir string, chunked bool, whole string) string {
	if !chunked {
		return whole
	}
	return dir + "/chunks/manifest.json and the chunks it lists"
}

// impactFeed names toc.txt in the next step when an index was handed off,
// because its impact section lives there and the diff feed does not carry it.
func impactFeed(dir, index string) string {
	if index == "" {
		return ""
	}
	return " + " + dir + "/toc.txt"
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
// the record is undecodable (no envelope can be built —
// the typed refusal is the documented divergence).
func readFetchStats(dir string) (fetchStats, error) {
	var st fetchStats
	raw, err := os.ReadFile(dir + suffixPRJSON)
	if err != nil {
		return st, &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("missing %s/PR.json (fetch the PR first)", dir)}
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return st, &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgUnreadablePR, dir)}
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return st, &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgUnreadablePR, dir)}
	}
	if err := statFiles(&st, dir, m); err != nil {
		return st, err
	}
	adds, ok := envelopeNum(m[keyAdditions])
	if !ok {
		return st, &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgUnreadablePR, dir)}
	}
	dels, ok := envelopeNum(m[keyDeletions])
	if !ok {
		return st, &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgUnreadablePR, dir)}
	}
	st.adds, st.dels = adds, dels
	st.hunks = countHunkLines(dir + suffixFullDiff)
	st.omitted = toJSONArray(joinLines(omittedPaths(prPaths(m), readDiffHunks(dir))))
	if fi, err := os.Stat(dir + suffixFullDiff); err == nil {
		st.diffSize = fi.Size()
	}
	return st, nil
}

// envelopeNum shapes one top-level stat field: numbers pass, null
// and false read 0, and anything else refuses the envelope.
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

// countHunkLines counts the `@@` hunk headers in the diff.
func countHunkLines(fullDiff string) int {
	sections := hunks.Files(readOptional(fullDiff))
	n := 0
	for i := range sections {
		n += sections[i].HunkMarks
	}
	return n
}

func panelVal(ctx2 *fetchRunCtx) core.FetchPanel {
	panel := core.FetchPanel{Members: append([]string{}, ctx2.panelList...)}
	if ctx2.panelSrc != "" {
		source := ctx2.panelSrc
		panel.Source = &source
	}
	return panel
}

func sensitiveVal(dir string) any {
	if _, err := os.Stat(dir + "/sensitive.txt"); err == nil {
		return dir + "/sensitive.txt"
	}
	return nil
}

// fetchData builds the fetch --json data with shell key order.
func fetchData(
	ctx2 *fetchRunCtx, st *fetchStats, budget *budgetState,
	fcap *fetchCap, chunkJSON any, incBase string, dump *string,
) *core.FetchData {
	artifacts := jq.NewObj(
		"PR.json", ctx2.dir+suffixPRJSON,
		fullDiffFile, ctx2.dir+suffixFullDiff,
		"identity.json", ctx2.dir+suffixIdentity,
		"toc.txt", ctx2.dir+"/toc.txt",
		"roles.txt", ctx2.dir+"/roles.txt",
		"reviews.json", ctx2.dir+suffixReviewsJSON,
		"inline.json", ctx2.dir+suffixInlineJSON,
		"threads.json", ctx2.dir+suffixThreadsJSON,
		"sensitive.txt", sensitiveVal(ctx2.dir),
		"incremental.diff", incrementalVal(ctx2.dir, incBase),
	)
	if _, err := os.Stat(ctx2.dir + suffixKBTxt); err == nil {
		artifacts.Set("kb.txt", ctx2.dir+suffixKBTxt)
	}
	if _, err := os.Stat(ctx2.dir + suffixKBRecall); err == nil {
		artifacts.Set("kb-recall.json", ctx2.dir+suffixKBRecall)
	}
	addArtifactIfPresent(artifacts, ctx2.dir, "lenses.txt", suffixLensesTxt)
	impactArtifactRow(artifacts, ctx2.dir)
	contextArtifacts(artifacts, ctx2.dir)
	data := &core.FetchData{
		Sources:    readcommon.ContextSources(ctx2.dir),
		Dir:        ctx2.dir,
		HeadSHA:    readcommon.PrField(ctx2.dir, keyHeadSHA),
		BaseSHA:    readcommon.PrField(ctx2.dir, keyBaseSHA),
		Files:      st.files,
		Additions:  st.adds,
		Deletions:  st.dels,
		Hunks:      st.hunks,
		Omitted:    st.omitted,
		DiffBytes:  st.diffSize,
		Panel:      panelVal(ctx2),
		Controls:   controlsWire(ctx2),
		Artifacts:  artifacts,
		PromptDump: dump,
	}
	if incBase != nullStr {
		base := incBase
		data.IncrementalBase = &base
	}
	addFetchExtras(data, budget, fcap, chunkJSON)
	return data
}

func incrementalVal(dir, incBase string) any {
	if incBase == nullStr {
		return nil
	}
	return dir + "/incremental.diff"
}

// statFiles owns the file-count read: an array counts, a missing or
// null list reads zero, and a non-array list refuses (the shape gate).
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
	return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgUnreadablePR, dir)}
}

func addFetchExtras(data *core.FetchData, budget *budgetState, fcap *fetchCap, chunkJSON any) {
	if budget.hasBudget || budget.estimateJSON != nil {
		data.Estimate = budget.estimateJSON
	}
	if budget.hasBudget {
		data.Budget = budget.budgetJSON
	}
	if fcap.json != nil {
		data.Cap = fcap.json
	}
	if chunkJSON != nil {
		data.Chunk = chunkJSON
	}
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

func localFetchData(ctx2 *fetchRunCtx, mode *localMode, listed string, total, adds, dels int, dump *string) *core.FetchData {
	dir := ctx2.dir
	withHunks := readDiffHunks(dir)
	omitted := omittedLocal(listed, withHunks)
	var diffSize int64
	if fi, err := os.Stat(dir + suffixFullDiff); err == nil {
		diffSize = fi.Size()
	}
	return &core.FetchData{
		Sources:    readcommon.ContextSources(dir),
		Local:      true,
		Dir:        dir,
		Kind:       mode.kind,
		HeadSHA:    mode.headSHA,
		BaseSHA:    mode.baseSHA,
		Controls:   controlsWire(ctx2),
		Files:      total,
		Additions:  adds,
		Deletions:  dels,
		Hunks:      countHunkLines(dir + suffixFullDiff),
		Omitted:    toJSONArray(joinLines(omitted)),
		DiffBytes:  diffSize,
		Panel:      panelVal(ctx2),
		Artifacts:  localArtifacts(dir),
		PromptDump: dump,
	}
}

// localArtifacts owns the local artifact map, adding the recall stores
// only when present (fixed key order).
func localArtifacts(dir string) *jq.Obj {
	artifacts := jq.NewObj(
		"source.json", dir+"/source.json",
		fullDiffFile, dir+suffixFullDiff,
		"identity.json", dir+suffixIdentity,
		"toc.txt", dir+"/toc.txt",
		"roles.txt", dir+"/roles.txt",
		"sensitive.txt", sensitiveVal(dir),
	)
	addArtifactIfPresent(artifacts, dir, "kb.txt", suffixKBTxt)
	addArtifactIfPresent(artifacts, dir, "kb-recall.json", suffixKBRecall)
	addArtifactIfPresent(artifacts, dir, "lenses.txt", suffixLensesTxt)
	impactArtifactRow(artifacts, dir)
	contextArtifacts(artifacts, dir)
	return artifacts
}

func addArtifactIfPresent(obj *jq.Obj, dir, key, suffix string) {
	if _, err := os.Stat(dir + suffix); err == nil {
		obj.Set(key, dir+suffix)
	}
}

// fetchTailLocal owns the fetch-local tail: the typed data (carrying
// kind instead of incremental_base) and the next step for the adapter to
// render. Human lines already streamed from localFinish; the dump rides in
// Data.PromptDump for the adapter to own on stdout (human mode) or in the
// envelope (JSON mode).
func fetchTailLocal(
	ctx context.Context, ctx2 *fetchRunCtx, req *core.FetchLocalRequest, f *core.FetchLocalFlags, budget *budgetState,
	fcap *fetchCap, chunkJSON any, mode *localMode,
	listed string, total, adds, dels int,
) (*core.FetchData, string, error) {
	if ref := finalizeLocalRun(ctx, ctx2, req, f, mode.baseSHA, listed); ref != nil {
		return nil, "", ref
	}
	if err := writeEvidence(ctx, ctx2, impactInput{index: f.Index, head: mode.headSHA}, f.Context, req.TimeUTC()); err != nil {
		return nil, "", err
	}
	writeTailReceipt(ctx2, budget)
	next := "feed SKILL.md + prompts/system.md + prompts/review.md + prompts/diagrams.md + " +
		ctx2.dir + "/source.json + " + diffFeed(ctx2.dir, chunkJSON != nil, fullDiffFile) + impactFeed(ctx2.dir, f.Index) +
		contextFeed(ctx2.dir, f.Context) +
		" to any agent; ask it to write " + ctx2.dir + "/review.json"
	dump, hasDump, err := localDumpPtr(ctx2, req, f)
	if err != nil {
		return nil, "", err
	}
	var dumpPtr *string
	if hasDump {
		dumpPtr = &dump
	}
	data := localFetchData(ctx2, mode, listed, total, adds, dels, dumpPtr)
	addFetchExtras(data, budget, fcap, chunkJSON)
	return data, next, nil
}

// localDumpPtr stages the prompt dump for the local tail: JSON mode stages
// before progress, human mode renders only under --show-prompts.
func localDumpPtr(
	ctx2 *fetchRunCtx, req *core.FetchLocalRequest, f *core.FetchLocalFlags,
) (string, bool, error) {
	if !req.JSONMode {
		if !f.ShowPrompts {
			return "", false, nil
		}
		dump, err := renderPromptDump(ctx2)
		if err != nil {
			return "", false, err
		}
		return dump, true, nil
	}
	return stageDump(ctx2, f.ShowPrompts)
}

func writeEvidence(ctx context.Context, ctx2 *fetchRunCtx, in impactInput, inputs []string, at string) error {
	if err := writeImpact(ctx, ctx2, in); err != nil {
		return err
	}
	return writeContext(ctx2.dir, inputs, at)
}

func contextArtifacts(artifacts *jq.Obj, dir string) {
	addArtifactIfPresent(artifacts, dir, "context.json", "/context.json")
	addArtifactIfPresent(artifacts, dir, "context.txt", "/context.txt")
}
