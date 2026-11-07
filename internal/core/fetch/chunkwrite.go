package fetchcore

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
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
	table := chunkFileTable(fullDiff)
	table = withholdChunkFiles(table, exclusions)
	cdir := dir + "/chunks"
	_ = os.RemoveAll(cdir)
	if merr := os.MkdirAll(cdir, dirPerm); merr != nil {
		return nil, internalErrorf("could not create %s: %v", cdir, merr)
	}
	total := int64(0)
	if fi, serr := os.Stat(fullDiff); serr == nil {
		total = fi.Size()
	}
	plan, maxBytes, capSrc := chunkPlanFor(table, limits, maxBytes, capSrc)
	if err := slicePlan(fullDiff, cdir, plan); err != nil {
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

// withholdChunkFiles drops privacy-excluded files from the chunk
// table before any chunk byte is written: the raw full.diff keeps every
// file for patch identity, while chunk payloads never carry excluded
// content. Either rename endpoint of a section triggers the drop.
func withholdChunkFiles(table []fileSlice, exclusions []string) []fileSlice {
	if len(exclusions) == 0 {
		return table
	}
	kept := table[:0]
	for _, entry := range table {
		if !excludedEndpoint(entry.path, entry.oldPath, exclusions) {
			kept = append(kept, entry)
		}
	}
	return kept
}

// slicePlan owns the plan slice with its typed failure: a missing chunk
// diff refuses chunk-write-failed, anything else passes through.
func slicePlan(fullDiff, cdir string, plan []chunkEntry) error {
	if serr := sliceChunks(fullDiff, cdir, plan); serr != nil {
		cwe, _ := errors.AsType[*chunkWriteError](serr)
		if cwe != nil {
			return &core.RefusalError{Code: "chunk-write-failed", Message: fmt.Sprintf(
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
	cdir := dir + "/chunks"
	if werr := atomic.WriteFile(cdir+"/manifest.json", []byte(jq.PrettyOrdered(manifest))); werr != nil {
		return nil, internalErrorf("could not write %s/manifest.json: %v", cdir, werr)
	}
	fmt.Fprintf(human, "» chunked: %d chunk(s) at or under %d bytes + %s/chunks/manifest.json\n", n, maxBytes, dir)
	return jq.NewObj("count", n, "manifest", dir+suffixChunkManifest, "total_bytes", total), nil
}

// packChunks owns the greedy next-fit pack in diff order: seal the chunk
// when the next file would not fit. Deterministic: the same diff bytes
// always yield the same chunk map.
// chunkPlanFor selects the packer: per-group when limits exist, whole-diff
// otherwise, and reports the effective manifest cap and source.
func chunkPlanFor(table []fileSlice, limits []groupLimit, maxBytes int64, capSrc string) ([]chunkEntry, int64, string) {
	if len(limits) == 0 {
		return packChunks(table, maxBytes), maxBytes, capSrc
	}
	return packChunksWithLimits(table, limits, maxBytes), maxGroupCap(limits), capSummarySource(limits)
}

// packChunksWithLimits packs a chunk per group with that group's own cap and
// never mixes groups in one chunk: a small group's cap cannot be bypassed by a
// larger neighboring group.
func packChunksWithLimits(table []fileSlice, limits []groupLimit, fallback int64) []chunkEntry {
	var plan []chunkEntry
	cur, curBytes := 0, int64(0)
	curGroup := ""
	for _, slice := range table {
		capBytes, groupID := sliceCap(slice, limits, fallback)
		newGroup := groupID != curGroup
		if curBytes > 0 && (newGroup || curBytes+slice.size > capBytes) {
			cur++
			curBytes = 0
		}
		if len(plan) == cur {
			plan = append(plan, chunkEntry{name: fmt.Sprintf("chunk-%02d", cur+1)})
		}
		plan[cur].members = append(plan[cur].members, slice)
		plan[cur].estSize += slice.size
		curBytes += slice.size
		curGroup = groupID
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

func packChunks(table []fileSlice, maxBytes int64) []chunkEntry {
	var plan []chunkEntry
	cur, curBytes := 0, int64(0)
	for _, s := range table {
		if curBytes > 0 && curBytes+s.size > maxBytes {
			cur++
			curBytes = 0
		}
		if len(plan) == cur {
			plan = append(plan, chunkEntry{name: fmt.Sprintf("chunk-%02d", cur+1)})
		}
		plan[cur].members = append(plan[cur].members, s)
		plan[cur].estSize += s.size
		curBytes += s.size
	}
	return plan
}

func sliceChunks(fullDiff, cdir string, plan []chunkEntry) error {
	raw, err := os.ReadFile(fullDiff)
	if err != nil {
		return internalErrorf("could not read %s: %v", fullDiff, err)
	}
	chunkOf := chunkIndex(plan)
	byChunk := sliceLines(string(raw), chunkOf, len(plan))
	for i, ch := range plan {
		if err := writeChunkFile(cdir, ch, byChunk[i]); err != nil {
			return err
		}
	}
	return nil
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
	for _, line := range splitLines(raw) {
		if _, isHeader := strings.CutPrefix(line, "diff --git "); isHeader {
			_, path := hunks.HeaderPaths(line)
			if idx, ok := chunkOf[path]; ok && idx != current {
				current = idx
			}
		}
		byChunk[current] = append(byChunk[current], line)
	}
	return byChunk
}

// writeChunkFile writes one chunk diff: the run lines joined with the
// unterminated-source-line newline guard the builder keeps.
func writeChunkFile(cdir string, ch chunkEntry, lines []string) error {
	name := cdir + pathSep + ch.name + diffSuffix
	if len(lines) == 0 {
		return &chunkWriteError{name: ch.name}
	}
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l + newline)
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
	raw, err := os.ReadFile(path)
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
	for i, ch := range plan {
		files := make([]any, len(ch.members))
		for j, m := range ch.members {
			files[j] = m.path
		}
		out = append(out, jq.NewObj(
			keyIndex, i+1,
			"name", ch.name,
			keyFiles, files,
			"estimated_bytes", ch.estSize,
			"bytes", chunkActualBytes(dir, ch.name),
			gitCmdDiff, dir+suffixChunks+ch.name+diffSuffix,
			"toc", dir+suffixChunks+ch.name+".toc.txt",
			"roles", dir+suffixChunks+ch.name+".roles.txt",
		))
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
