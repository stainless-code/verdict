package fetch

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
)

// chunkSchemaVersion is the manifest coverage-obligation version.
const chunkSchemaVersion = "1"

// chunkEntry is one packed chunk: member files in diff order with their
// plan byte sizes.
type chunkEntry struct {
	name    string
	members []fileSlice
	estSize int64
}

// maybeWriteChunks ports maybe_write_chunks: greedy next-fit packing of
// the diff-order file table into cap-sized chunks, per-chunk diff slices
// concatenating byte-identically to full.diff, per-chunk checklists and
// roles, and the versioned manifest. The chunked notice owns stderr in
// both modes (`>&2`). Returns the envelope chunk summary (nil when the
// gate did not arm chunking).
func maybeWriteChunks(human io.Writer, dir string, maxBytes int64, chunkActive bool, headSHA, baseSHA string, capSrc string) (any, error) {
	if !chunkActive {
		return nil, nil //nolint:nilnil // (nil, nil) is the gate-not-armed signal, never an error
	}
	fullDiff := dir + suffixFullDiff
	table := chunkFileTable(fullDiff)
	cdir := dir + "/chunks"
	_ = os.RemoveAll(cdir)
	if merr := os.MkdirAll(cdir, dirPerm); merr != nil {
		return nil, internalErrorf("could not create %s: %v", cdir, merr)
	}
	total := int64(0)
	if fi, serr := os.Stat(fullDiff); serr == nil {
		total = fi.Size()
	}
	plan := packChunks(table, maxBytes)
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

// slicePlan ports the plan slice with its typed failure: a missing chunk
// diff refuses chunk-write-failed, anything else passes through.
func slicePlan(fullDiff, cdir string, plan []chunkEntry) error {
	if serr := sliceChunks(fullDiff, cdir, plan); serr != nil {
		cwe, _ := errors.AsType[*chunkWriteError](serr)
		if cwe != nil {
			return &cli.RefusalError{Code: "chunk-write-failed", Message: fmt.Sprintf(
				"chunk %s missing after slicing %s (re-run the fetch)", cwe.name, fullDiff)}
		}
		return serr
	}
	return nil
}

// writeChunkManifest ports the manifest write: the versioned JSON lands
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

// packChunks ports the greedy next-fit pack in diff order: seal the chunk
// when the next file would not fit. Deterministic: the same diff bytes
// always yield the same chunk map.
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

// sliceChunks ports the awk slice: contiguous file runs in diff order,
// each output line newline-terminated (awk print semantics, so a final
// unterminated source line gains its newline in the chunk).
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

// sliceLines ports the awk slice: diff lines dealt into per-chunk runs,
// switching runs on header lines the plan knows.
func sliceLines(raw string, chunkOf map[string]int, n int) [][]string {
	byChunk := make([][]string, n)
	current := 0
	for _, line := range splitLines(raw) {
		if strings.HasPrefix(line, "diff --git ") {
			if idx, ok := chunkOf[diffFilePath(line)]; ok && idx != current {
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

// writeChunkFiles ports the per-chunk checklist and roles files.
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

// chunkHeaderLines ports `grep -n "^diff --git" chunk.diff || echo "(no
// diff headers?)"`.
func chunkHeaderLines(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return []string{"(no diff headers?)"}
	}
	var out []string
	n := 0
	for _, line := range splitLines(string(raw)) {
		n++
		if strings.HasPrefix(line, "diff --git ") {
			out = append(out, fmt.Sprintf("%d:%s", n, line))
		}
	}
	if len(out) == 0 {
		return []string{"(no diff headers?)"}
	}
	return out
}

// dirBase ports `$(basename "$dir")`.
func dirBase(dir string) string {
	if _, after, ok := strings.CutLast(dir, pathSep); ok {
		return after
	}
	return dir
}

// chunksJSON ports the manifest chunk entries in index order.
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

// chunkActualBytes ports `wc -c` on the sliced chunk diff.
func chunkActualBytes(dir, name string) int64 {
	fi, err := os.Stat(dir + suffixChunks + name + diffSuffix)
	if err != nil {
		return 0
	}
	return fi.Size()
}
