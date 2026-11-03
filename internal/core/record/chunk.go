package recordcore

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
)

// validateChunkMerge ports validate_chunk_merge: chunked bundles pin anchors
// to the per-chunk snapshots and gate attested-but-partial coverage.
// Non-chunked bundles validate exactly as today. It returns the hunks file
// the anchor validation reads.
func (st *state) validateChunkMerge() (string, error) {
	dir := st.dir
	manifest := dir + "/chunks/manifest.json"
	if !exists(manifest) {
		return dir + "/hunks.tsv", nil
	}
	refetch := chunkRefetch(st)
	chunks, names, ref := readChunkManifest(manifest, refetch)
	if ref != nil {
		return "", ref
	}
	chunkHunks := dir + chunkHunksSuffix
	removeBestEffort(chunkHunks)
	removeBestEffort(dir + "/.chunk-hunks.tsv.part")
	diffs, allHunks, ref := resolveChunkDiffs(st, chunks, refetch)
	if ref != nil {
		return "", ref
	}
	// The shell builds .chunk-hunks.tsv incrementally and leaves it behind
	// on later failures; write it now so residues match through every gate.
	if err := atomic.WriteFile(chunkHunks, []byte(hunks.Format(allHunks))); err != nil {
		return "", &core.RefusalError{Code: codeRecordFailed, Message: fmt.Sprintf("could not write %s: %v", chunkHunks, err)}
	}
	if !concatEquals(diffs, dir+"/full.diff") {
		st.warn(fmt.Sprintf(msgChunkDrifted, dir))
	}
	return checkChunkCoverage(st, manifest, chunks, names, refetch)
}

// chunkRefetch renders the re-fetch hint for chunk-gate refusals.
func chunkRefetch(st *state) string {
	if st.prov == providerLocal {
		slug := strings.TrimPrefix(st.pr, localPrefix)
		return fmt.Sprintf("verdict fetch-local %s %s --chunk -- <paths>", st.repo, slug)
	}
	return fmt.Sprintf("verdict fetch %s %s --chunk", st.repo, st.pr)
}

// chunk is one manifest chunk entry.
type chunk struct {
	name  string
	index any
	diff  string
	files []string
}

// readChunkManifest reads and schema-gates the chunk manifest, returning the
// sorted chunks and their names.
func readChunkManifest(manifest, refetch string) ([]chunk, []string, error) {
	schema, manMap := manifestSchema(manifest)
	if schema != "1" {
		if schema == "" {
			schema = "missing"
		}
		return nil, nil, &core.RefusalError{Code: codeMissingChunkSnap, Message: fmt.Sprintf(msgChunkUnreadable, manifest, schema, refetch)}
	}
	chunks := parseChunks(jq.Elements(manMap["chunks"]))
	slices.SortStableFunc(chunks, func(a, b chunk) int { return cmpLess(a.index, b.index) })
	names := chunkNames(chunks)
	if len(names) == 0 {
		return nil, nil, &core.RefusalError{Code: codeMissingChunkSnap, Message: fmt.Sprintf(msgChunkNoChunks, manifest, refetch)}
	}
	return chunks, names, nil
}

// manifestSchema reads the manifest's schema_version and decoded map.
// Over-cap manifests read as unreadable (same refusal as a bad schema).
func manifestSchema(manifest string) (string, map[string]any) {
	raw, err := readAgentFile(manifest)
	var manMap map[string]any
	if err == nil {
		if doc, derr := jq.Decode(raw); derr == nil {
			manMap, _ = jq.AsMap(doc)
		}
	}
	if manMap == nil {
		return "", nil
	}
	return jq.Str(jq.Alt(manMap["schema_version"], "")), manMap
}

// chunkNames collects the non-empty chunk names in sort order.
func chunkNames(chunks []chunk) []string {
	var names []string
	for _, c := range chunks {
		if c.name != "" {
			names = append(names, c.name)
		}
	}
	return names
}

// parseChunks normalizes the manifest's chunk entries.
func parseChunks(items []any) []chunk {
	var chunks []chunk
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		chunks = append(chunks, parseChunk(m))
	}
	return chunks
}

// parseChunk normalizes one manifest chunk entry.
func parseChunk(m map[string]any) chunk {
	diff := ""
	if d, ok := m["diff"].(string); ok {
		diff = d
	}
	return chunk{name: chunkName(m["name"]), index: m["index"], diff: diff, files: chunkFiles(m)}
}

// chunkFiles reads the chunk's file list, skipping non-strings.
func chunkFiles(m map[string]any) []string {
	var files []string
	fl, ok := m["files"].([]any)
	if !ok {
		return nil
	}
	for _, f := range fl {
		if s, ok := f.(string); ok {
			files = append(files, s)
		}
	}
	return files
}

// resolveChunkDiffs resolves each chunk's pinned diff and parses the hunks,
// refusing when any snapshot is missing.
func resolveChunkDiffs(st *state, chunks []chunk, refetch string) ([]string, []hunks.Hunk, error) {
	dir := st.dir
	chunkHunks := dir + chunkHunksSuffix
	var missing []string
	var diffs []string
	var allHunks []hunks.Hunk
	for _, c := range chunks {
		raw, ok := readChunkDiff(dir, c)
		if !ok {
			missing = append(missing, c.name)
			continue
		}
		diffs = append(diffs, chunkDiffPath(dir, c))
		allHunks = append(allHunks, hunks.Parse(string(raw))...)
	}
	if len(missing) > 0 {
		removeBestEffort(chunkHunks)
		return nil, nil, &core.RefusalError{Code: codeMissingChunkSnap, Message: fmt.Sprintf(
			msgChunkSnapMissing, strings.Join(missing, " "), refetch)}
	}
	return diffs, allHunks, nil
}

// chunkDiffPath resolves one chunk's pinned diff path.
func chunkDiffPath(dir string, c chunk) string {
	if c.diff != "" && fileNonEmpty(c.diff) {
		return c.diff
	}
	if c.name != "" && fileNonEmpty(dir+"/chunks/"+c.name+".diff") {
		return dir + "/chunks/" + c.name + ".diff"
	}
	return ""
}

// readChunkDiff reads one chunk's pinned diff.
func readChunkDiff(dir string, c chunk) ([]byte, bool) {
	path := chunkDiffPath(dir, c)
	if path == "" {
		return nil, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return raw, true
}

// checkChunkCoverage validates the review's chunks attestation against the
// manifest, warning unattested and refusing partial coverage.
func checkChunkCoverage(st *state, manifest string, chunks []chunk, names []string, refetch string) (string, error) {
	chunkHunks := st.dir + chunkHunksSuffix
	reviewRaw, err := os.ReadFile(st.reviewFile)
	if err != nil {
		return "", &core.RefusalError{Code: codeMissingReviewFile, Message: fmt.Sprintf(msgReviewMissing, st.reviewFile)}
	}
	claimed, kind := chunkAttestation(reviewRaw)
	switch kind {
	case attestationPresent:
		// Handled by the coverage tail below.
	case attestationAbsent:
		st.warn(fmt.Sprintf(msgUnattested, manifest, len(names)))
		return chunkHunks, nil
	case attestationMalformed:
		st.warn("ignoring malformed chunks attestation (want an array of manifest chunk names) — validating as unattested")
		return chunkHunks, nil
	}
	return checkChunkCoverageTail(st, manifest, coverageInput{chunks: chunks, names: names, claimed: claimed}, refetch)
}

// checkChunkCoverageTail diffs the attested chunks against the manifest,
// refusing partial coverage.
// coverageInput carries the attestation check inputs.
type coverageInput struct {
	chunks  []chunk
	names   []string
	claimed []any
}

func checkChunkCoverageTail(st *state, manifest string, in coverageInput, refetch string) (string, error) {
	chunks, names, claimed := in.chunks, in.names, in.claimed
	chunkHunks := st.dir + chunkHunksSuffix
	claimedSet := stringSet(claimed)
	manifestSet := map[string]bool{}
	for _, n := range names {
		manifestSet[n] = true
	}
	missingDescs, nMiss := describeMissingChunks(chunks, claimedSet)
	for _, c := range claimed {
		if s, ok := c.(string); ok && s != "" && !manifestSet[s] {
			st.warn(fmt.Sprintf("ignoring unknown chunk '%s' in the chunks attestation (not in %s)", s, manifest))
		}
	}
	if len(missingDescs) > 0 {
		// The shell removes the derived snapshot before refusing partial
		// coverage; a residue here would linger past the refusal (I3).
		removeBestEffort(chunkHunks)
		return "", &core.RefusalError{Code: codeMissingChunkCov, Message: fmt.Sprintf(
			msgChunkCoverage, len(names)-nMiss, len(names), strings.Join(missingDescs, ", "), refetch)}
	}
	return chunkHunks, nil
}

// stringSet builds a set from string values, skipping non-strings.
func stringSet(vals []any) map[string]bool {
	set := map[string]bool{}
	for _, v := range vals {
		if s, ok := v.(string); ok {
			set[s] = true
		}
	}
	return set
}

// describeMissingChunks lists manifest chunks absent from the attestation.
func describeMissingChunks(chunks []chunk, claimedSet map[string]bool) ([]string, int) {
	var missingDescs []string
	nMiss := 0
	for _, c := range chunks {
		if c.name == "" || claimedSet[c.name] {
			continue
		}
		nMiss++
		if len(c.files) > 0 {
			missingDescs = append(missingDescs, fmt.Sprintf("%s (%s)", c.name, strings.Join(c.files, ", ")))
		} else {
			missingDescs = append(missingDescs, c.name)
		}
	}
	return missingDescs, nMiss
}

// attestationKind distinguishes absent from malformed chunks attestations.
type attestationKind int

const (
	attestationAbsent attestationKind = iota
	attestationMalformed
	attestationPresent
)

// chunkAttestation reads the review's chunks attestation: absent or malformed
// reads unattested, each with its own warning.
func chunkAttestation(reviewRaw []byte) ([]any, attestationKind) {
	reviewMap := jq.NewObj()
	if reviewDoc, derr := jq.DecodeOrdered(reviewRaw); derr == nil {
		if rm, ok := reviewDoc.(*jq.Obj); ok {
			reviewMap = rm
		}
	}
	var claimed any
	has := false
	if reviewMap != nil {
		claimed, has = reviewMap.Get("chunks")
	}
	if !has || claimed == nil {
		return nil, attestationAbsent
	}
	claimedList, ok := claimed.([]any)
	if !ok {
		return nil, attestationMalformed
	}
	return claimedList, attestationPresent
}

// chunkName renders a manifest chunk name the way `jq -r '.name // empty'`
// does: null/false drop out, everything else renders raw.
func chunkName(v any) string {
	if v == nil || v == false {
		return ""
	}
	return jq.Str(v)
}

// cmpLess is the three-way chunk-index order for [slices.SortStableFunc],
// porting sort_by: null < false < true < numbers < strings < arrays < objects.
func cmpLess(a, b any) int {
	ra, rb := jqRank(a), jqRank(b)
	if ra != rb {
		return ra - rb
	}
	switch ra {
	case rankNumber:
		fa, fb := jqNum(a), jqNum(b)
		switch {
		case fa < fb:
			return -1
		case fa > fb:
			return 1
		default:
			return 0
		}
	case rankString:
		return strings.Compare(jq.Str(a), jq.Str(b))
	default:
		return 0
	}
}

// jqRank returns the jq type rank.
func jqRank(v any) int {
	switch v.(type) {
	case nil:
		return 0
	case bool:
		return 1
	case jq.Number, float64, int:
		return rankNumber
	case string:
		return rankString
	case []any:
		return rankArray
	default:
		return rankObject
	}
}

// jqNum reads a numeric index value.
func jqNum(v any) float64 {
	switch t := v.(type) {
	case jq.Number:
		if f, err := strconv.ParseFloat(t.String(), floatBits); err == nil {
			return f
		}
		return 0
	case float64:
		return t
	case int:
		return float64(t)
	default:
		return 0
	}
}

// fileNonEmpty reports whether path exists with size > 0 (`-s`).
func fileNonEmpty(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Size() > 0
}

// concatEquals reports whether the concatenated files equal target bytes.
func concatEquals(paths []string, target string) bool {
	want, err := os.ReadFile(target)
	if err != nil {
		return false
	}
	total, ok := concatSize(paths)
	if !ok || total != len(want) {
		return false
	}
	got, ok := concatRead(paths, total)
	if !ok {
		return false
	}
	return got == string(want)
}

// concatSize sums the file sizes, reporting ok=false when any stat fails.
func concatSize(paths []string) (int, bool) {
	total := 0
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			return 0, false
		}
		total += int(fi.Size())
	}
	return total, true
}

// concatRead reads and concatenates the files, reporting ok=false on error.
func concatRead(paths []string, total int) (string, bool) {
	var sb strings.Builder
	sb.Grow(total)
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return "", false
		}
		sb.Write(raw)
	}
	return sb.String(), true
}
