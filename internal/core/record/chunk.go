package recordcore

import (
	"crypto/sha256"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/noblock"
	"github.com/stainless-code/verdict/internal/readcommon"
)

//nolint:revive // chunk snapshot, dry-run, and coverage gates read as one block
func (st *state) validateChunkMerge() (string, error) {
	dir := st.dir
	manifest := dir + "/chunks/manifest.json"
	if !exists(manifest) {
		return dir + "/hunks.tsv", nil
	}
	refetch := chunkRefetch(st)
	exclusions := privacyExclusions(dir)
	chunks, names, ref := readChunkManifest(manifest, refetch, noRetainedSections(dir, exclusions))
	if ref != nil {
		return "", ref
	}
	chunkHunks := dir + chunkHunksSuffix
	diffs, allHunks, ref := resolveChunkDiffs(st, chunks, refetch)
	if ref != nil {
		return "", ref
	}
	if st.dryRun {
		st.hunkRows = allHunks
	} else {
		removeBestEffort(chunkHunks)
		removeBestEffort(dir + "/.chunk-hunks.tsv.part")
		if err := atomic.WriteFile(chunkHunks, []byte(hunks.Format(allHunks))); err != nil {
			return "", &core.RefusalError{Code: codeRecordFailed, Message: fmt.Sprintf("could not write %s: %v", chunkHunks, err)}
		}
	}
	if !concatEquals(diffs, dir+"/full.diff", exclusions) {
		st.warn(fmt.Sprintf(msgChunkDrifted, dir))
	}
	if st.inputChecked {
		return chunkHunks, nil
	}
	return checkChunkCoverage(st, manifest, chunks, names, refetch)
}

func chunkRefetch(st *state) string {
	if st.prov == providerLocal {
		slug := strings.TrimPrefix(st.pr, localPrefix)
		return fmt.Sprintf("verdict fetch-local %s %s --chunk -- <paths>", st.repo, slug)
	}
	return fmt.Sprintf("verdict fetch %s %s --chunk", st.repo, st.pr)
}

type chunk struct {
	name  string
	index any
	diff  string
	files []string
}

// readChunkManifest parses the chunk list. An empty list is valid only when
// the privacy exclusions left no section to slice (allowEmpty), which is the
// empty manifest a fetch writes after withholding everything; any other empty
// list is the missing snapshot it claims to be.
func readChunkManifest(manifest, refetch string, allowEmpty bool) ([]chunk, []string, error) {
	schema, manMap := manifestSchema(manifest)
	if schema != "1" {
		if schema == "" {
			schema = "missing"
		}
		return nil, nil, &core.RefusalError{Code: codeMissingChunkSnap, Message: fmt.Sprintf(msgChunkUnreadable, manifest, schema, refetch)}
	}
	chunks := parseChunks(jq.Elements(manMap[fieldChunks]))
	sortChunks(chunks)
	names := chunkNames(chunks)
	if len(names) == 0 && !allowEmpty {
		return nil, nil, &core.RefusalError{Code: codeMissingChunkSnap, Message: fmt.Sprintf(msgChunkNoChunks, manifest, refetch)}
	}
	return chunks, names, nil
}

// noRetainedSections reports whether the privacy exclusions leave full.diff
// with no file section at all, so the empty chunk list a fetch wrote is the
// whole truth. A missing or unreadable diff keeps the strict reading.
func noRetainedSections(dir string, exclusions []string) bool {
	raw, err := noblock.ReadFile(dir + "/full.diff")
	if err != nil {
		return false
	}
	_, sections := hunks.Sections(config.WithoutExcludedSections(string(raw), exclusions))
	return len(sections) == 0
}

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

func chunkNames(chunks []chunk) []string {
	var names []string
	for _, c := range chunks {
		if c.name != "" {
			names = append(names, c.name)
		}
	}
	return names
}

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

func parseChunk(m map[string]any) chunk {
	diff := ""
	if d, ok := m["diff"].(string); ok {
		diff = d
	}
	return chunk{name: chunkName(m["name"]), index: m["index"], diff: diff, files: chunkFiles(m)}
}

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
		if !st.dryRun {
			removeBestEffort(chunkHunks)
		}
		return nil, nil, &core.RefusalError{Code: codeMissingChunkSnap, Message: fmt.Sprintf(
			msgChunkSnapMissing, strings.Join(missing, " "), refetch)}
	}
	return diffs, allHunks, nil
}

func chunkDiffPath(dir string, c chunk) string {
	if c.diff != "" && fileNonEmpty(c.diff) {
		return c.diff
	}
	if c.name != "" && fileNonEmpty(dir+"/chunks/"+c.name+".diff") {
		return dir + "/chunks/" + c.name + ".diff"
	}
	return ""
}

func readChunkDiff(dir string, c chunk) ([]byte, bool) {
	path := chunkDiffPath(dir, c)
	if path == "" {
		return nil, false
	}
	raw, err := noblock.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return raw, true
}

func checkChunkCoverage(st *state, manifest string, chunks []chunk, names []string, refetch string) (string, error) {
	chunkHunks := st.dir + chunkHunksSuffix
	reviewRaw, err := ReadReview(st.dir, st.reviewFile)
	if err != nil {
		return "", &core.RefusalError{Code: codeMissingReviewFile, Message: fmt.Sprintf(msgReviewMissing, st.reviewFile)}
	}
	claimed, kind := chunkAttestation(reviewRaw)
	switch kind {
	case attestationPresent:
	case attestationAbsent:
		st.warnInput(fmt.Sprintf(msgUnattested, manifest, len(names)))
		return chunkHunks, nil
	case attestationMalformed:
		st.warnInput("ignoring malformed chunks attestation (want an array of manifest chunk names) — validating as unattested")
		return chunkHunks, nil
	}
	return checkChunkCoverageTail(st, manifest, coverageInput{chunks: chunks, names: names, claimed: claimed}, refetch)
}

type coverageInput struct {
	chunks  []chunk
	names   []string
	claimed []any
}

//nolint:revive // coverage checks read as one block
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
			st.warnInput(fmt.Sprintf("ignoring unknown chunk '%s' in the chunks attestation (not in %s)", s, manifest))
		}
	}
	if len(missingDescs) > 0 {
		if !st.dryRun {
			removeBestEffort(chunkHunks)
		}
		return "", &core.RefusalError{Code: codeMissingChunkCov, Message: fmt.Sprintf(
			msgChunkCoverage, len(names)-nMiss, len(names), strings.Join(missingDescs, ", "), refetch)}
	}
	return chunkHunks, nil
}

func stringSet(vals []any) map[string]bool {
	set := map[string]bool{}
	for _, v := range vals {
		if s, ok := v.(string); ok {
			set[s] = true
		}
	}
	return set
}

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

type attestationKind int

const (
	attestationAbsent attestationKind = iota
	attestationMalformed
	attestationPresent
)

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
		claimed, has = reviewMap.Get(fieldChunks)
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

func chunkName(v any) string {
	if v == nil || v == false {
		return ""
	}
	return jq.Str(v)
}

func sortChunks(chunks []chunk) {
	slices.SortStableFunc(chunks, func(a, b chunk) int { return readcommon.Cmp(a.index, b.index) })
}

func fileNonEmpty(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Size() > 0
}

func concatEquals(paths []string, target string, exclusions []string) bool {
	want, err := noblock.ReadFile(target)
	if err != nil {
		return false
	}
	// The chunk payloads carry the diff the privacy exclusions already
	// filtered, so the comparison reads that same view: full.diff keeps every
	// section for patch identity, and a withheld one is not drift.
	kept := config.WithoutExcludedSections(string(want), exclusions)
	total, ok := concatSize(paths)
	if !ok || total != len(kept) {
		return false
	}
	got, ok := concatRead(paths, total)
	if !ok {
		return false
	}
	return sameDiffSections(got, kept)
}

// privacyExclusions reads the personal exclusion globs the fetch withheld from
// every model-facing export. A bundle with no captured run state records none,
// so its chunks are compared against full.diff unchanged.
func privacyExclusions(dir string) []string {
	rs, found, err := config.ReadRunState(dir)
	if err != nil || !found || rs == nil || rs.Resolved == nil {
		return nil
	}
	return rs.Resolved.Privacy.Exclusions
}

// sameDiffSections compares two diffs as their leading bytes plus the multiset
// of file sections: chunk membership follows the semantic file order (a
// package's files and their tests share a chunk), so the ordered concatenation
// is not the diff's own order — what the record still proves is that every
// section appears exactly as often, and byte-identically. A lost, doubled, or
// edited section fails; the total byte count is compared by the caller.
func sameDiffSections(got, want string) bool {
	if got == want {
		return true
	}
	gotPreamble, gotSections := hunks.Sections(got)
	wantPreamble, wantSections := hunks.Sections(want)
	if gotPreamble != wantPreamble || len(gotSections) != len(wantSections) {
		return false
	}
	counts := make(map[[sha256.Size]byte]int, len(wantSections))
	for i := range wantSections {
		counts[sha256.Sum256([]byte(wantSections[i]))]++
	}
	for i := range gotSections {
		sum := sha256.Sum256([]byte(gotSections[i]))
		if counts[sum] == 0 {
			return false
		}
		counts[sum]--
	}
	return true
}

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

func concatRead(paths []string, total int) (string, bool) {
	var sb strings.Builder
	sb.Grow(total)
	for _, p := range paths {
		raw, err := noblock.ReadFile(p)
		if err != nil {
			return "", false
		}
		sb.Write(raw)
	}
	return sb.String(), true
}

// chunkCheckNames returns the manifest's chunk names for the review checker:
// an unreadable or empty manifest leaves the checker silent so the merge's
// pinned-snapshot refusal keeps its own code.
func chunkCheckNames(dir string) ([]string, string) {
	manifest := dir + "/chunks/manifest.json"
	if !exists(manifest) {
		return nil, ""
	}
	schema, manMap := manifestSchema(manifest)
	if schema != "1" || manMap == nil {
		return nil, ""
	}
	names := chunkNames(parseChunks(jq.Elements(manMap[fieldChunks])))
	if len(names) == 0 {
		return nil, ""
	}
	return names, manifest
}
