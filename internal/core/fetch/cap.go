package fetchcore

import (
	"cmp"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
)

// defaultFetchCap is the fetched-diff byte cap (512000 bytes, overridable
// for deliberate exceptions only).
const defaultFetchCap = 512000

// fetchCap carries the resolved diff cap and its provenance.
type fetchCap struct {
	// json renders the envelope's cap summary (nil when default).
	json     any
	source   string
	maxBytes int64
}

// resolveFetchCap resolves the byte cap: REVIEW_MAX_BYTES wins, else the
// first usable line of .review/max_bytes, else the 512000 default. A set cap
// prints the » cap line and arms the envelope summary; garbage refuses typed.
func resolveFetchCap(r *session) (fetchCap, error) {
	env := r.env
	fc := fetchCap{maxBytes: defaultFetchCap}
	raw, src := "", ""
	if v, ok := env["REVIEW_MAX_BYTES"]; ok && v != "" {
		raw, src = strings.TrimSpace(v), "REVIEW_MAX_BYTES"
	} else if v, ok := readKnobFile(r, ".review/max_bytes"); ok {
		raw, src = v, ".review/max_bytes"
	}
	if raw == "" {
		return fc, nil
	}
	n, ok := parsePositiveInt(raw)
	if !ok {
		return fc, &core.RefusalError{Code: "bad-max-bytes", Message: fmt.Sprintf(
			"cap '%s' from %s is not a positive byte count (want e.g. REVIEW_MAX_BYTES=1000000 "+
				"or a .review/max_bytes file holding 1000000)", raw, src)}
	}
	fc.maxBytes, fc.source = n, src
	fmt.Fprintf(r.Human, "» cap: %d bytes (from %s, default 512000)\n", n, src)
	fc.json = jq.NewObj("bytes", n, keySource, src)
	return fc, nil
}

// parsePositiveInt parses a validated positive byte count.
func parsePositiveInt(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, numBase10, numBitSize)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// readKnobFile reads the first usable line of a knob file (comment and
// blank lines skipped, surrounding whitespace trimmed), reporting ok=false
// when the file is missing or holds no usable line.
func readKnobFile(r *session, path string) (string, bool) {
	raw, ok := r.readControl(path)
	if !ok {
		return "", false
	}
	for _, line := range splitLines(string(raw)) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		return trimmed, true
	}
	return "", false
}

// chunkOptIn carries the resolved chunking opt-in and its source.
type chunkOptIn struct {
	source string
	active bool
}

// resolveChunkOptIn reads the chunk opt-in: an overridden cap wins
// outright (chunking disabled), else explicit --chunk wins, else the first
// usable line of .review/chunk, else off. Garbage refuses typed.
func resolveChunkOptIn(r *session, chunkFlag bool, capSource string) (chunkOptIn, error) {
	if capSource != "" {
		return chunkOptIn{}, nil
	}
	if chunkFlag {
		return chunkOptIn{active: true, source: flagChunk}, nil
	}
	knob, ok := readKnobFile(r, ".review/chunk")
	if !ok {
		return chunkOptIn{}, nil
	}
	switch strings.ToLower(knob) {
	case "", "false", "off", "0", "no":
		return chunkOptIn{}, nil
	case strTrue, "on", "1", "yes":
		return chunkOptIn{active: true, source: ".review/chunk"}, nil
	default:
		return chunkOptIn{}, &core.RefusalError{Code: "bad-chunk-knob", Message: fmt.Sprintf(
			"chunk knob '%s' from .review/chunk is not an opt-in flag (want true|false, on|off, 1|0, yes|no — "+
				"first usable line wins, absent reads off)", knob)}
	}
}

// checkDiffSize applies the byte cap: empty diffs warn-and-proceed; diffs
// over the cap arm chunking on the opted-in path (refusing only the
// single-file degenerate), else refuse diff-too-large with the advisory
// split candidates riding the envelope. The chunk knob is consulted only on
// the over-cap path, so under-cap runs never see bad-chunk-knob.
func checkDiffSize(r *session, fullDiff string, lim fetchCap, chunkFlag bool) (chunkActive bool, err error) {
	fi, statErr := os.Stat(fullDiff)
	var total int64
	if statErr == nil {
		total = fi.Size()
	}
	if total > lim.maxBytes {
		active, ref := overCap(r, fullDiff, total, lim, chunkFlag)
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
func overCap(r *session, fullDiff string, total int64, lim fetchCap, chunkFlag bool) (bool, error) {
	optIn, ref := resolveChunkOptIn(r, chunkFlag, lim.source)
	if ref != nil {
		return false, ref
	}
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
			"REVIEW_MAX_BYTES=<n> or .review/max_bytes)", total, lim.maxBytes)}
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

// fileSlice is one file's byte size in diff order.
type fileSlice struct {
	path string
	size int64
}

func chunkFileTable(fullDiff string) []fileSlice {
	raw, err := os.ReadFile(fullDiff)
	if err != nil {
		return nil
	}
	sections := hunks.Files(string(raw))
	out := make([]fileSlice, 0, len(sections))
	for i := range sections {
		out = append(out, fileSlice{path: sections[i].Path, size: sections[i].Size})
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
