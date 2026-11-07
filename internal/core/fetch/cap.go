package fetchcore

import (
	"cmp"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
)

// DefaultDiffCap is the fetched-diff byte cap (512000 bytes, overridable
// for deliberate exceptions only).
const DefaultDiffCap = 512000

// fetchCap carries the resolved diff cap and its provenance.
type fetchCap struct {
	// json renders the envelope's cap summary (nil when default).
	json     any
	source   string
	maxBytes int64
}

// resolveFetchCap resolves the byte cap from the effective resolution:
// flags beat environment, which beats repository scopes and shared
// defaults, which beat personal review, which beats the 512000 built-in.
// A non-built-in cap prints the » cap line and arms the envelope
// summary; malformed values refuse at parse time, never here.
func resolveFetchCap(r *session, res *config.Resolved) fetchCap {
	fc := fetchCap{maxBytes: DefaultDiffCap}
	if res == nil {
		return fc
	}
	values, origins := res.Defaults.Values, res.Defaults.Origins
	fc.maxBytes = values.MaxBytes
	origin := origins["max_bytes"]
	if origin.Layer == "" || origin.Layer == config.LayerBuiltIn {
		return fc
	}
	fc.source = origin.Source
	fmt.Fprintf(r.Human, "» cap: %d bytes (from %s, default 512000)\n", fc.maxBytes, fc.source)
	fc.json = jq.NewObj("bytes", fc.maxBytes, keySource, fc.source)
	return fc
}

// chunkOptIn carries the resolved chunking opt-in and its source.
type chunkOptIn struct {
	source string
	active bool
}

// resolveChunkOptIn reads the chunking opt-in: an overridden cap wins
// outright (chunking disabled), else explicit --chunk wins, else the
// resolved chunk default (repository, personal review, built-in off).
// Malformed values refuse at parse time, never here.
func resolveChunkOptIn(res *config.Resolved, chunkFlag bool, capSource string) chunkOptIn {
	if capSource != "" {
		return chunkOptIn{}
	}
	if chunkFlag {
		return chunkOptIn{active: true, source: flagChunk}
	}
	if res != nil && res.Defaults.Values.Chunk {
		return chunkOptIn{active: true, source: res.Defaults.Origins["chunk"].Source}
	}
	return chunkOptIn{}
}

// checkDiffSize applies the byte cap: empty diffs warn-and-proceed; diffs
// over the cap arm chunking on the opted-in path (refusing only the
// single-file degenerate), else refuse diff-too-large with the advisory
// split candidates riding the envelope.
func checkDiffSize(r *session, res *config.Resolved, fullDiff string, lim fetchCap, chunkFlag bool) (chunkActive bool, err error) {
	if limits := groupLimits(res, chunkFlag); len(limits) > 0 {
		active, ref := checkGroupDiffSize(r, res, fullDiff, chunkFlag)
		if ref != nil {
			return false, ref
		}
		if allFilesGrouped(fullDiff, limits) {
			return active, nil
		}
		fallback, err := checkTotalDiffSize(r, res, fullDiff, lim, chunkFlag)
		return active || fallback, err
	}
	return checkTotalDiffSize(r, res, fullDiff, lim, chunkFlag)
}

// checkTotalDiffSize bounds a diff without complete group attribution.
func checkTotalDiffSize(r *session, res *config.Resolved, fullDiff string, lim fetchCap, chunkFlag bool) (chunkActive bool, err error) {
	fi, statErr := os.Stat(fullDiff)
	var total int64
	if statErr == nil {
		total = fi.Size()
	}
	if total > lim.maxBytes {
		active, ref := overCap(r, res, fullDiff, total, lim, chunkFlag)
		if ref != nil {
			return false, ref
		}
		return active, nil
	}
	if total == 0 {
		r.Warn("empty diff (0 bytes) — bundle kept, review proceeds on empty diff")
	}
	return false, nil
}

// overCap handles the over-cap fork: opted-in chunking slices on file
// boundaries (the single-file degenerate keeps the advisory refusal with
// no chunks written); otherwise the refusal carries split candidates.
func overCap(r *session, res *config.Resolved, fullDiff string, total int64, lim fetchCap, chunkFlag bool) (bool, error) {
	optIn := resolveChunkOptIn(res, chunkFlag, lim.source)
	if optIn.active {
		// The opt-in notices own stderr in both modes (`>&2`).

		if chunkPlanOK(fullDiff, lim.maxBytes) {
			fmt.Fprintf(r.Diag, "» chunking opted in (from %s) — slicing over-cap diff (%d bytes, cap %d) into cap-sized chunks\n",
				optIn.source, total, lim.maxBytes)
			return true, nil
		}
		fmt.Fprintf(r.Diag, "» chunking opted in (from %s) — single file over cap, keeping today's advisory refusal "+
			"(split the range or narrow, or override the cap deliberately)\n",
			optIn.source)
	}
	cands := suggestSplits(fullDiff, total)
	printSplitCandidates(r, cands)
	refusal := &core.RefusalError{Code: "diff-too-large", Message: fmt.Sprintf(
		"diff %d bytes exceeds cap %d bytes — split the range (fetch-local --range/--base, fetch --base-sha) "+
			"or narrow with a paths filter, then re-fetch (override deliberate exceptions only: "+
			"REVIEW_MAX_BYTES=<n> or the typed max_bytes default)", total, lim.maxBytes)}
	refusal.Candidates = cands
	return false, refusal
}

func printSplitCandidates(r *session, cands []any) {
	if len(cands) == 0 {
		return
	}
	fmt.Fprintln(r.Diag, "candidates (suggested scopes — re-run narrowed, nothing auto-narrowed):")
	for _, c := range cands {
		if o, ok := c.(*jq.Obj); ok {
			kind, _ := o.Get(keyKind)
			sugg, _ := o.Get(keySuggestion)
			fmt.Fprintf(r.Diag, "- [%v] %v\n", kind, sugg)
		}
	}
}

// fileSlice is one file's byte size in diff order. oldPath carries the
// pre-rename path when the section renamed it, so privacy matching sees
// both endpoints; every other consumer reads path and size only.
type fileSlice struct {
	path    string
	oldPath string
	size    int64
}

func chunkFileTable(fullDiff string) []fileSlice {
	raw, err := os.ReadFile(fullDiff)
	if err != nil {
		return nil
	}
	sections := hunks.Files(string(raw))
	out := make([]fileSlice, 0, len(sections))
	for i := range sections {
		out = append(out, fileSlice{path: sections[i].Path, oldPath: sections[i].OldPath, size: sections[i].Size})
	}
	return out
}

// chunkPlanOK reports whether every file slice fits the cap.
func chunkPlanOK(fullDiff string, maxBytes int64) bool {
	table := chunkFileTable(fullDiff)
	if len(table) == 0 {
		return false
	}
	for _, s := range table {
		if s.size > maxBytes {
			return false
		}
	}
	return true
}

// suggestSplits offers the largest file alone,
// everything-but-largest (up to 10 files named), and a range half-split,
// with byte estimates. Empty when the diff holds no parseable slices.
func suggestSplits(fullDiff string, total int64) []any {
	table := chunkFileTable(fullDiff)
	if len(table) == 0 {
		return []any{}
	}
	sorted := append([]fileSlice{}, table...)
	// Largest first: numeric reverse on the size, whole-line comparison
	// breaking ties — equal sizes order by path bytes ascending.
	slices.SortFunc(sorted, compareFileSlices)
	top := sorted[0]
	restBytes := max(total-top.size, 0)
	var rest []string
	for _, s := range sorted[1:] {
		rest = append(rest, s.path)
	}
	half := (total + 1) / 2
	out := []any{
		jq.NewObj(
			keyKind, "paths", keyFiles, []any{top.path}, keyEstimated, top.size,
			keySuggestion, fmt.Sprintf("re-fetch narrowed to %s (~%d bytes)", top.path, top.size)),
	}
	if len(rest) > 0 {
		out = append(out, restSplit(top, rest, restBytes))
	}
	out = append(out, jq.NewObj(
		keyKind, modeRange, keyFiles, []any{}, keyEstimated, half,
		keySuggestion, fmt.Sprintf("split the range in half (~%d bytes each): fetch-local --range/--base, fetch --base-sha/--since", half)))
	return out
}

func compareFileSlices(a, b fileSlice) int {
	if a.size != b.size {
		return cmp.Compare(b.size, a.size)
	}
	return strings.Compare(a.path, b.path)
}

func restSplit(top fileSlice, rest []string, restBytes int64) any {
	shown := rest
	if len(shown) > numShownPaths {
		shown = shown[:numShownPaths]
	}
	files := make([]any, len(shown))
	for i, p := range shown {
		files[i] = p
	}
	suggestion := fmt.Sprintf("re-fetch excluding %s (~%d bytes across %d file(s))", top.path, restBytes, len(rest))
	if len(rest) > numShownPaths {
		suggestion = fmt.Sprintf("re-fetch excluding %s: first 10 of %d remaining files (~%d bytes total)", top.path, len(rest), restBytes)
	}
	return jq.NewObj(
		keyKind, "paths", keyFiles, files, keyEstimated, restBytes,
		keySuggestion, suggestion)
}
