package fetchcore

import (
	"fmt"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// groupLimit is one plan group's actual resolved byte cap and increment
// preference, read from the group's captured file resolutions. Source is the
// origin the cap came from; an empty/built-in source means the group may
// chunk when the opt-in is on. Files names the group's members so a diff
// section is attributable without re-resolving.
type groupLimit struct {
	Files    map[string]bool
	ID       string
	Source   string
	MaxBytes int64
	chunk    bool
}

// groupLimits resolves the per-group byte caps and chunk eligibility from the
// per-file defaults config.Resolve already folded (env, flags, scopes). No
// layer is re-read here, and a group with no member file is never invented.
func groupLimits(res *config.Resolved, chunkFlag bool) []groupLimit {
	if res == nil {
		return nil
	}
	out := make([]groupLimit, 0, len(res.Plan.Groups))
	for i := range res.Plan.Groups {
		out = append(out, limitForGroup(res, &res.Plan.Groups[i], chunkFlag))
	}
	return out
}

// limitForGroup reads one group's cap, origin, and chunk eligibility from its
// first member file resolution: every member shares the same effective
// defaults because the group key hashes them.
func limitForGroup(res *config.Resolved, group *config.PlanGroup, chunkFlag bool) groupLimit {
	limit := groupLimit{ID: group.ID, MaxBytes: DefaultDiffCap, Files: map[string]bool{}}
	for _, path := range group.Files {
		limit.Files[path] = true
	}
	for j := range res.Files {
		if file := memberFile(&res.Files[j], limit.Files); file != nil {
			applyDefaults(&limit, file, chunkFlag)
			break
		}
	}
	return limit
}

// memberFile matches one resolution to the group by either path.
func memberFile(file *config.FileResolution, files map[string]bool) *config.FileResolution {
	if files[file.Path] || (file.OldPath != "" && files[file.OldPath]) {
		return file
	}
	return nil
}

// applyDefaults copies one member's resolved cap, origin, and chunk
// eligibility; an explicit (non-built-in) cap disables chunking for the group.
func applyDefaults(limit *groupLimit, file *config.FileResolution, chunkFlag bool) {
	limit.MaxBytes = file.Defaults.Values.MaxBytes
	limit.Source = file.Defaults.Origins["max_bytes"].Source
	explicit := limit.Source != "" && limit.Source != config.LayerBuiltIn
	limit.chunk = !explicit && (chunkFlag || file.Defaults.Values.Chunk)
}

// limitForPath finds the group owning a diff section.
func limitForPath(limits []groupLimit, path string) (groupLimit, bool) {
	for i := range limits {
		if limits[i].Files[path] {
			return limits[i], true
		}
	}
	return groupLimit{}, false
}

// groupBytes totals the diff sections attributable to each group by index.
func groupBytes(limits []groupLimit, table []fileSlice) []int64 {
	out := make([]int64, len(limits))
	for i := range table {
		for g := range limits {
			if limits[g].Files[table[i].path] {
				out[g] += table[i].size
				break
			}
		}
	}
	return out
}

// allFilesGrouped reports whether every parsed diff section belongs to a
// resolved group. An unattributable section (a fixture or a file the file
// list missed) falls back to the whole-diff cap instead of silently escaping
// every group cap.
func allFilesGrouped(fullDiff string, limits []groupLimit) bool {
	for _, slice := range chunkFileTable(fullDiff) {
		if _, ok := limitForPath(limits, slice.path); !ok {
			return false
		}
	}
	return true
}

// checkGroupDiffSize applies each group's own cap. A run with no resolved
// groups falls back to the whole-diff cap (nil decision, caller keeps its
// existing path). A group over its cap refuses on its own terms; only a
// group whose own files all fit may chunk, and only when that group's cap is
// built-in/implicit and chunking is opted in. Untouched groups never
// authorize chunking another group.
func checkGroupDiffSize(
	r *session, res *config.Resolved, fullDiff string, chunkFlag bool,
) (bool, *core.RefusalError) {
	limits := groupLimits(res, chunkFlag)
	if len(limits) == 0 {
		return false, nil
	}
	table := chunkFileTable(fullDiff)
	bytes := groupBytes(limits, table)
	chunkActive := false
	for i := range limits {
		active, ref := applyGroupCap(r, &limits[i], bytes[i], table)
		if ref != nil {
			return false, ref
		}
		chunkActive = chunkActive || active
	}
	return chunkActive, nil
}

// applyGroupCap prints one group's explicit cap origin and applies its own
// limit, reporting whether that group armed chunking.
func applyGroupCap(r *session, limit *groupLimit, groupBytes int64, table []fileSlice) (bool, *core.RefusalError) {
	if limit.Source != "" && limit.Source != config.LayerBuiltIn {
		fmt.Fprintf(r.Human, "» cap: group %s %d bytes (from %s, default 512000)\n",
			limit.ID, limit.MaxBytes, limit.Source)
	}
	if groupBytes <= limit.MaxBytes {
		return false, nil
	}
	return overGroupCap(r, limit, table, groupBytes)
}

// overGroupCap resolves one over-cap group: an ineligible group refuses on
// its own cap; an eligible group chunks only when every member section fits,
// so the single-file degenerate keeps today's advisory refusal.
func overGroupCap(r *session, limit *groupLimit, table []fileSlice, total int64) (bool, *core.RefusalError) {
	eligible := limit.chunk && chunkPlanFit(table, limit.Files, limit.MaxBytes)
	if !eligible {
		if limit.chunk {
			fmt.Fprintf(r.Diag, "» chunking opted in for group %s — single file over cap, keeping today's advisory "+
				"refusal (split the range or narrow, or override the cap deliberately)\n", limit.ID)
		}
		ref := groupCapRefusal(limit, total, table)
		printSplitCandidates(r, ref.Candidates)
		return false, ref
	}
	fmt.Fprintf(r.Diag, "» chunking opted in for group %s — slicing that group's over-cap diff (%d bytes, cap %d)\n",
		limit.ID, total, limit.MaxBytes)
	return true, nil
}

// chunkPlanFit reports whether every member section fits the group cap.
func chunkPlanFit(table []fileSlice, files map[string]bool, maxBytes int64) bool {
	seen := false
	for i := range table {
		if !files[table[i].path] {
			continue
		}
		seen = true
		if table[i].size > maxBytes {
			return false
		}
	}
	return seen
}

// groupCapRefusal names the group and its own cap, with split candidates.
func groupCapRefusal(limit *groupLimit, total int64, table []fileSlice) *core.RefusalError {
	cands := splitCandidatesFor(table, limit.Files, total)
	refusal := &core.RefusalError{Code: "diff-too-large", Message: fmt.Sprintf(
		"group %s diff %d bytes exceeds its cap %d bytes (from %s) — split the range "+
			"(fetch-local --range/--base, fetch --base-sha) or narrow with a paths filter, then re-fetch "+
			"(override deliberate exceptions only: REVIEW_MAX_BYTES=<n> or the typed max_bytes default)",
		limit.ID, total, limit.MaxBytes, orEmptyCapSource(limit.Source))}
	refusal.Candidates = cands
	return refusal
}

// orEmptyCapSource names the built-in default when no explicit origin exists.
func orEmptyCapSource(source string) string {
	if source == "" {
		return config.LayerBuiltIn
	}
	return source
}

// splitCandidatesFor offers the largest member file, everything but the
// largest, and a range half-split for one group, with byte estimates.
func splitCandidatesFor(table []fileSlice, files map[string]bool, total int64) []any {
	members := make([]fileSlice, 0, len(table))
	for i := range table {
		if files[table[i].path] {
			members = append(members, table[i])
		}
	}
	if len(members) == 0 {
		return []any{}
	}
	sorted := append([]fileSlice{}, members...)
	slices.SortFunc(sorted, compareFileSlices)
	top := sorted[0]
	restBytes := max(total-top.size, 0)
	rest := make([]string, 0, len(sorted)-1)
	for _, member := range sorted[1:] {
		rest = append(rest, member.path)
	}
	half := (total + 1) / 2
	out := []any{jq.NewObj(
		keyKind, "paths", keyFiles, []any{top.path}, keyEstimated, top.size,
		keySuggestion, fmt.Sprintf("re-fetch narrowed to %s (~%d bytes)", top.path, top.size))}
	if len(rest) > 0 {
		out = append(out, restSplit(top, rest, restBytes))
	}
	out = append(out, jq.NewObj(
		keyKind, modeRange, keyFiles, []any{}, keyEstimated, half,
		keySuggestion, fmt.Sprintf("split the range in half (~%d bytes each): fetch-local --range/--base, fetch --base-sha/--since", half)))
	return out
}

// capSummarySource joins the distinct group cap origins for the chunk
// manifest, so the capture names the exact scopes that governed the run.
func capSummarySource(limits []groupLimit) string {
	sources := make([]string, 0, len(limits))
	for i := range limits {
		source := limits[i].Source
		if source == "" || source == config.LayerBuiltIn {
			continue
		}
		if !slices.Contains(sources, source) {
			sources = append(sources, source)
		}
	}
	if len(sources) == 0 {
		return "per-group: built-in defaults"
	}
	return "per-group: " + strings.Join(sources, ", ")
}
