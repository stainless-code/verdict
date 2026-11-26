package fetchcore

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/noblock"
)

const chunkSchemaVersion = "1"

type chunkEntry struct {
	name    string
	members []fileSlice
	estSize int64
}

func maybeWriteChunks(
	human io.Writer, dir string, maxBytes int64, chunkActive bool, limits []groupLimit,
	headSHA, baseSHA, capSrc string, exclusions []string,
) (any, error) {
	if !chunkActive {
		return nil, nil //nolint:nilnil // (nil, nil) is the gate-not-armed signal, never an error
	}
	fullDiff := dir + suffixFullDiff
	kept, kerr := chunkSourceDiff(fullDiff, exclusions)
	if kerr != nil {
		return nil, kerr
	}
	table := chunkFileTableText(kept)
	cdir := dir + "/chunks"
	_ = os.RemoveAll(cdir)
	if merr := os.MkdirAll(cdir, dirPerm); merr != nil {
		return nil, internalErrorf("could not create %s: %v", cdir, merr)
	}
	total := diffBytes(fullDiff)
	plan, maxBytes, capSrc := chunkPlanFor(table, limits, maxBytes, capSrc, chunkOrderAllowed(kept))
	if err := slicePlan(fullDiff, kept, cdir, plan); err != nil {
		return nil, err
	}
	n := len(plan)
	for i, ch := range plan {
		if err := writeChunkFiles(dir, cdir, i+1, n, ch); err != nil {
			return nil, err
		}
	}
	return writeChunkManifest(human, dir, plan, maxBytes, total, n, headSHA, baseSHA, capSrc)
}

// chunkSourceDiff reads the bundle diff and applies the privacy exclusions:
// the table, the order decision, and the sliced payloads all read this one
// text, because the model-facing chunk payloads never carry withheld content.
// full.diff itself keeps every file for patch identity.
func chunkSourceDiff(fullDiff string, exclusions []string) (string, error) {
	raw, err := noblock.ReadFile(fullDiff)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", internalErrorf("could not read %s: %v", fullDiff, err)
	}
	return config.WithoutExcludedSections(string(raw), exclusions), nil
}

// diffBytes reports the bundle diff's size, zero when it is absent: the
// manifest publishes the diff's bytes, not the payload bytes a filter leaves.
func diffBytes(fullDiff string) int64 {
	if fi, err := os.Stat(fullDiff); err == nil {
		return fi.Size()
	}
	return 0
}

// chunkOrderAllowed reports whether the diff may be reordered for packing:
// every section must end its own line. A hand-made diff whose last section is
// unterminated keeps the diff's own order, so that section stays at the end of
// the final chunk instead of gluing its last line to the next chunk's first
// header.
func chunkOrderAllowed(diffText string) bool {
	_, sections := hunks.Sections(diffText)
	return terminatedSections(sections)
}

// slicePlan owns the plan slice with its typed failure: a missing chunk
// diff refuses chunk-write-failed, anything else passes through.
func slicePlan(fullDiff, kept, cdir string, plan []chunkEntry) error {
	if serr := sliceChunks(kept, cdir, plan); serr != nil {
		cwe, _ := errors.AsType[*chunkWriteError](serr)
		if cwe != nil {
			return &core.RefusalError{Code: "chunk-write-failed", Message: core.Msgf(
				"chunk %s missing after slicing %s (re-run the fetch)", cwe.name, fullDiff)}
		}
		return serr
	}
	return nil
}

// writeChunkManifest owns the manifest write: the versioned JSON lands
// and the chunked notice owns stderr.
func writeChunkManifest(
	human io.Writer, dir string, plan []chunkEntry, maxBytes, total int64,
	n int, headSHA, baseSHA, capSrc string,
) (any, error) {
	headVal, baseVal := any(nil), any(nil)
	if headSHA != "" {
		headVal = headSHA
	}
	if baseSHA != "" {
		baseVal = baseSHA
	}
	capSrcVal := any(nil)
	if capSrc != "" {
		capSrcVal = capSrc
	}
	manifest := jq.NewObj(
		keySchema, chunkSchemaVersion,
		"bundle", dir,
		keyHeadSHA, headVal,
		keyBaseSHA, baseVal,
		"cap", jq.NewObj("bytes", maxBytes, keySource, capSrcVal),
		"total_bytes", total,
		"chunk_count", n,
		"chunks", chunksJSON(dir, plan),
	)
	exclusionManifest(dir, manifest)
	cdir := dir + "/chunks"
	if werr := atomic.WriteFile(cdir+"/manifest.json", []byte(jq.PrettyOrdered(manifest))); werr != nil {
		return nil, internalErrorf("could not write %s/manifest.json: %v", cdir, werr)
	}
	fmt.Fprint(human, core.Msgf("» chunked: %d chunk(s) at or under %d bytes + %s/chunks/manifest.json\n", n, maxBytes, dir))
	return jq.NewObj("count", n, "manifest", dir+suffixChunkManifest, "total_bytes", total), nil
}

// chunkPlanFor selects the packer: per-group when limits exist, whole-diff
// otherwise, and reports the effective manifest cap and source. reorder asks
// for the semantic file order, which keeps a package's files and their tests
// together whenever the cap allows it; a caller whose diff cannot be reordered
// without gluing sections passes false and keeps the diff's own order.
func chunkPlanFor(
	table []fileSlice, limits []groupLimit, maxBytes int64, capSrc string, reorder bool,
) ([]chunkEntry, int64, string) {
	if reorder {
		table = semanticOrder(table)
	}
	if len(limits) == 0 {
		return packChunks(table, maxBytes), maxBytes, capSrc
	}
	return packChunksWithLimits(table, limits, maxBytes, reorder), maxGroupCap(limits), capSummarySource(limits)
}

// packChunksWithLimits packs one group at a time at that group's own cap and
// never mixes groups in one chunk: a small group's cap cannot be bypassed by a
// larger neighboring group. hoist asks for each group's members to be gathered
// together, which semantic order may interleave; a table that must keep its
// own order packs contiguous runs instead, so no section moves past an
// intervening group's.
func packChunksWithLimits(table []fileSlice, limits []groupLimit, fallback int64, hoist bool) []chunkEntry {
	if !hoist {
		return packChunkRuns(table, limits, fallback)
	}
	groups := chunkGroups(table, limits, fallback)
	var plan []chunkEntry
	for i := range groups {
		plan = packGroup(plan, groups[i])
	}
	return plan
}

// packChunkRuns packs each contiguous run of one group at that group's cap, in
// table order, so an ordered diff keeps its own section order even when its
// groups alternate: the run boundary always opens a new chunk.
func packChunkRuns(table []fileSlice, limits []groupLimit, fallback int64) []chunkEntry {
	runs := chunkRuns(table, limits, fallback)
	var plan []chunkEntry
	for i := range runs {
		plan = packGroup(plan, runs[i])
	}
	return plan
}

// chunkRuns splits an ordered table into the contiguous runs of one group,
// each at that group's cap.
func chunkRuns(table []fileSlice, limits []groupLimit, fallback int64) []chunkGroup {
	var runs []chunkGroup
	runID := ""
	for _, slice := range table {
		capBytes, id := sliceCap(slice, limits, fallback)
		if len(runs) == 0 || id != runID {
			runs = append(runs, chunkGroup{capBytes: capBytes})
			runID = id
		}
		runs[len(runs)-1].members = append(runs[len(runs)-1].members, slice)
	}
	return runs
}

// chunkGroup is one config group's members, in semantic order, and its cap.
type chunkGroup struct {
	members  []fileSlice
	capBytes int64
}

// chunkGroups splits the table into its config groups in first-appearance
// order. An unattributable section forms its own group at the fallback cap,
// never a neighboring group's cap.
func chunkGroups(table []fileSlice, limits []groupLimit, fallback int64) []chunkGroup {
	byID := map[string]int{}
	var groups []chunkGroup
	for _, slice := range table {
		capBytes, id := sliceCap(slice, limits, fallback)
		at, seen := byID[id]
		if !seen {
			at = len(groups)
			byID[id] = at
			groups = append(groups, chunkGroup{capBytes: capBytes})
		}
		groups[at].members = append(groups[at].members, slice)
	}
	return groups
}

// packGroup appends one group's next-fit chunks to the plan, continuing the
// plan's numbering so the manifest stays one ordered list. The pack is greedy
// and next-fit: seal the chunk when the next file would not fit.
func packGroup(plan []chunkEntry, group chunkGroup) []chunkEntry {
	var curBytes int64
	open := false
	for _, slice := range group.members {
		if open && curBytes+slice.size > group.capBytes {
			open = false
		}
		if !open {
			plan = append(plan, chunkEntry{name: fmt.Sprintf("chunk-%02d", len(plan)+1)})
			curBytes = 0
			open = true
		}
		last := len(plan) - 1
		plan[last].members = append(plan[last].members, slice)
		plan[last].estSize += slice.size
		curBytes += slice.size
	}
	return plan
}

// sliceCap names one section's group cap: the fallback for an unattributable
// section, never a neighboring group's larger cap.
func sliceCap(slice fileSlice, limits []groupLimit, fallback int64) (int64, string) {
	group, known := limitForPath(limits, slice.path)
	if !known {
		return fallback, ""
	}
	return group.MaxBytes, group.ID
}

// maxGroupCap reports the largest per-group cap, the manifest's cap summary.
func maxGroupCap(limits []groupLimit) int64 {
	largest := int64(0)
	for i := range limits {
		if limits[i].MaxBytes > largest {
			largest = limits[i].MaxBytes
		}
	}
	if largest == 0 {
		return DefaultDiffCap
	}
	return largest
}

// packChunks owns the greedy next-fit pack over the whole-diff cap, in the
// order the table arrives in (semantic for chunkPlanFor, the diff's own for a
// caller that already ordered it). Deterministic: the same table always packs
// the same way.
func packChunks(table []fileSlice, maxBytes int64) []chunkEntry {
	return packGroup(nil, chunkGroup{members: table, capBytes: maxBytes})
}

func sliceChunks(diffText, cdir string, plan []chunkEntry) error {
	if len(plan) == 0 {
		// Every section was withheld, so there is nothing to slice: the
		// manifest still publishes an empty chunk list.
		return nil
	}
	byChunk, ok := chunkSections(diffText, plan)
	if !ok {
		chunkOf := chunkIndex(plan)
		byChunk = sliceLines(diffText, chunkOf, len(plan))
	}
	for i, ch := range plan {
		if err := writeChunkFile(cdir, ch, byChunk[i]); err != nil {
			return err
		}
	}
	return nil
}

// chunkSections groups the diff's own file sections by the chunk each planned
// member belongs to, in the member order the manifest publishes, so a chunk
// reads in the order its checklist lists. Every section is emitted exactly once
// and byte for byte; the bytes before the first header ride at the front of the
// first chunk. A plan that does not account for every section reports false and
// the caller keeps the line-ordered slicing.
func chunkSections(raw string, plan []chunkEntry) ([][]string, bool) {
	preamble, sections := hunks.Sections(raw)
	if len(plan) == 0 || !terminatedSections(sections) {
		return nil, false
	}
	byChunk, count, ok := assignSections(plan, sections, sectionsByPath(sections))
	if !ok || count != len(sections) {
		return nil, false
	}
	if preamble != "" {
		byChunk[0] = append([]string{preamble}, byChunk[0]...)
	}
	return byChunk, true
}

// terminatedSections reports whether every section ends its own line: a
// section that does not would merge with whatever follows it once the order
// changed, so such a diff keeps the line-ordered slicing.
func terminatedSections(sections []string) bool {
	for i := range sections {
		if !strings.HasSuffix(sections[i], newline) {
			return false
		}
	}
	return true
}

// sectionsByPath indexes each section's position by the path its header names,
// keeping every duplicate.
func sectionsByPath(sections []string) map[string][]int {
	byPath := make(map[string][]int, len(sections))
	for i := range sections {
		_, newPath := hunks.HeaderPaths(firstLine(sections[i]))
		byPath[newPath] = append(byPath[newPath], i)
	}
	return byPath
}

// assignSections places each planned member's section in its chunk, in member
// order. It reports the assigned chunks, how many members it placed, and
// whether every member found an unused section.
func assignSections(plan []chunkEntry, sections []string, byPath map[string][]int) ([][]string, int, bool) {
	used := make([]bool, len(sections))
	byChunk := make([][]string, len(plan))
	count := 0
	for i := range plan {
		for _, member := range plan[i].members {
			at := claimSection(byPath[member.path], used)
			if at < 0 {
				return nil, 0, false
			}
			used[at] = true
			byChunk[i] = append(byChunk[i], sections[at])
			count++
		}
	}
	return byChunk, count, true
}

// claimSection takes the next unused section of one path, so a path that
// appears twice in a diff is emitted twice rather than copied from one entry.
func claimSection(candidates []int, used []bool) int {
	for _, at := range candidates {
		if !used[at] {
			return at
		}
	}
	return -1
}

// firstLine returns the first line of a section, its `diff --git` header.
func firstLine(section string) string {
	header, _, _ := strings.Cut(section, newline)
	return header
}

// chunkIndex maps each planned file to its chunk number.
func chunkIndex(plan []chunkEntry) map[string]int {
	chunkOf := map[string]int{}
	for i, ch := range plan {
		for _, m := range ch.members {
			chunkOf[m.path] = i
		}
	}
	return chunkOf
}

func sliceLines(raw string, chunkOf map[string]int, n int) [][]string {
	byChunk := make([][]string, n)
	current := 0
	for line := range strings.SplitAfterSeq(raw, "\n") {
		if line == "" {
			continue
		}
		current = chunkForLine(line, chunkOf, current)
		byChunk[current] = append(byChunk[current], line)
	}
	return byChunk
}

func writeChunkFile(cdir string, ch chunkEntry, lines []string) error {
	name := cdir + pathSep + ch.name + diffSuffix
	if len(lines) == 0 {
		return &chunkWriteError{name: ch.name}
	}
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l)
	}
	if err := atomic.WriteFile(name, []byte(sb.String())); err != nil {
		return internalErrorf("could not write %s: %v", name, err)
	}
	return nil
}

// chunkWriteError marks a missing chunk diff after slicing.
type chunkWriteError struct {
	name string
}

// Error implements error.
func (e *chunkWriteError) Error() string { return "chunk " + e.name + " missing" }

// writeChunkFiles owns the per-chunk checklist and roles files.
func writeChunkFiles(dir, cdir string, i, n int, ch chunkEntry) error {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Chunk %d/%d for %s — read ranges as coverage checklist\n\n", i, n, dirBase(dir))
	for _, l := range chunkHeaderLines(cdir + pathSep + ch.name + diffSuffix) {
		sb.WriteString(l + newline)
	}
	sb.WriteString("\n## files\n")
	for _, m := range ch.members {
		fmt.Fprintf(&sb, "- %s (%d bytes)\n", m.path, m.size)
	}
	if werr := atomic.WriteFile(cdir+pathSep+ch.name+".toc.txt", []byte(sb.String())); werr != nil {
		return internalErrorf("could not write chunk toc: %v", werr)
	}
	paths := make([]string, len(ch.members))
	for j, m := range ch.members {
		paths[j] = m.path
	}
	return atomic.WriteFile(cdir+pathSep+ch.name+".roles.txt", []byte(rolesForPaths(paths)))
}

// chunkHeaderLines numbers the `diff --git` headers of a chunk diff, or
// reports the fallback line.
func chunkHeaderLines(path string) []string {
	raw, err := noblock.ReadFile(path)
	if err != nil {
		return []string{noChunkHeaders}
	}
	sections := hunks.Files(string(raw))
	out := make([]string, 0, len(sections))
	for i := range sections {
		out = append(out, sections[i].HeaderIndex())
	}
	if len(out) == 0 {
		return []string{noChunkHeaders}
	}
	return out
}

// noChunkHeaders is the per-chunk header-index fallback.
const noChunkHeaders = "(no diff headers?)"

// dirBase returns the last path element of dir.
func dirBase(dir string) string {
	if _, after, ok := strings.CutLast(dir, pathSep); ok {
		return after
	}
	return dir
}

// chunksJSON owns the manifest chunk entries in index order.
func chunksJSON(dir string, plan []chunkEntry) []any {
	out := make([]any, 0, len(plan))
	total := 0
	for _, ch := range plan {
		total += len(ch.members)
	}
	offset := 0
	for i, ch := range plan {
		files := make([]any, len(ch.members))
		for j, m := range ch.members {
			files[j] = m.path
		}
		entry := jq.NewObj(
			keyIndex, i+1,
			"name", ch.name,
			keyFiles, files,
			"estimated_bytes", ch.estSize,
			"bytes", chunkActualBytes(dir, ch.name),
			gitCmdDiff, dir+suffixChunks+ch.name+diffSuffix,
			"toc", dir+suffixChunks+ch.name+".toc.txt",
			"roles", dir+suffixChunks+ch.name+".roles.txt",
		)
		offset += len(files)
		chunkReceipt(len(plan) > 1, len(files), total, offset).Apply(entry)
		out = append(out, entry)
	}
	return out
}

// chunkActualBytes counts the bytes of the sliced chunk diff.
func chunkActualBytes(dir, name string) int64 {
	fi, err := os.Stat(dir + suffixChunks + name + diffSuffix)
	if err != nil {
		return 0
	}
	return fi.Size()
}

func chunkReceipt(cut bool, returned, total, offset int) *core.Truncation {
	if !cut {
		return nil
	}
	var next *int
	if offset < total {
		next = new(offset)
	}
	return core.ResultCut("chunk_cap", returned, new(total), next)
}

func chunkForLine(line string, chunkOf map[string]int, current int) int {
	if strings.HasPrefix(line, "diff --git ") {
		_, file := hunks.HeaderPaths(strings.TrimSuffix(line, "\n"))
		if idx, ok := chunkOf[file]; ok {
			return idx
		}
	}
	return current
}
