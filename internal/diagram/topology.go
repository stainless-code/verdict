package diagram

import (
	"cmp"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/noblock"
	"github.com/stainless-code/verdict/internal/overrides"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	// maxTopoNodes caps the file nodes one block renders, in bundle order.
	maxTopoNodes = 60
	// maxTopoEdges caps the relation edges one block renders, in derivation
	// order, so the two counts bound the block's size.
	maxTopoEdges = 60
	// maxTopoBytes is the hard ceiling one rendered block may occupy: well
	// under GitHub's 65,536-byte body limit, so a body split can never land
	// inside the <details> region or its mermaid fence.
	maxTopoBytes = 12000
	// maxTopoLabel caps one rendered label. The cap is not the block's size
	// bound — 60 nodes and 60 edges at this length already overrun
	// maxTopoBytes — it stops one outsized monorepo path from spending the
	// room the whole graph needs.
	maxTopoLabel = 180
	// labelEllipsis marks a label the single-label ceiling shortened.
	labelEllipsis = "..."
	// countDigits is the widest decimal a rendered count can need: every
	// non-negative int writes in at most 19 digits.
	countDigits = 19
	// topologySummary is the one summary line both surfaces render, so the
	// label cannot fork between report --output and the posted review body.
	topologySummary = "Change topology (computed from the diff)"
	// mermaidIndent indents the flowchart body one level, as mermaid renders it.
	mermaidIndent = "    "
	// notePrefix marks the omission note as a mermaid comment, legal inside
	// the fence and invisible in the rendered diagram.
	notePrefix = "%% "
	// noteSuffix names the order the caps kept, so a truncated block reads as
	// a head of the bundle instead of a complete graph.
	noteSuffix = " omitted (bundle order)"
	// filesOmittedSuffix and edgesOmittedSuffix word the two omission counts,
	// kept separate so the byte budget can reserve the widest note.
	filesOmittedSuffix = " more files"
	edgesOmittedSuffix = " more edges"
	noteJoin           = " and "
	// blockTail closes the mermaid fence and the <details> region. No node or
	// edge may ever spend it, so the byte budget reserves it up front.
	blockTail = newline + "```" + newline + newline + "</details>"
	// newline joins every rendered line, in this file and the behavioral block.
	newline = "\n"
)

// maxNoteBytes is the room the block reserves for its widest omission note,
// taken before the counts that fill it are known: two counts at countDigits
// digits each, the wording, the indented comment prefix, and the suffix.
const maxNoteBytes = len(newline+mermaidIndent+notePrefix) + 2*countDigits +
	len(filesOmittedSuffix) + len(noteJoin) + len(edgesOmittedSuffix) + len(noteSuffix)

// Bundle file and sidecar suffixes, joined onto the bundle directory.
const (
	prJSONFile   = "/PR.json"
	fullDiffFile = "/full.diff"
	hunksTSVFile = "/hunks.tsv"
	rolesTxtFile = "/roles.txt"
)

// Label and markup constants shared with the table renderer.
const (
	quoteMark      = "\""
	slashSep       = "/"
	tabSep         = "\t"
	hashSpace      = "## "
	dashSpace      = "- "
	newMarker      = "NEW"
	nullPath       = "null"
	usesLabel      = "uses "
	validatesLabel = "validates "
)

// File is one changed file: the row both surfaces render, carrying the stable
// id its node uses and, for a renamed file, the path it came from.
type File struct {
	ID, Path, Role string
	PreviousPath   string
	Adds, Dels     int
	Hunks          int
}

// Edge is one derived relation between two changed files. Only an added diff
// line that names another changed file yields one; nothing is invented.
type Edge struct {
	From, To, Label string
}

// Bundle is the deterministic input every topology surface renders.
type Bundle struct {
	Files     []File
	Added     [][2]string
	DiffBytes int
	// FileCount is the size gate's file count: PR.json's `files` length when
	// the document carries a usable one, else the diff header count. It is not
	// len(Files), because a truncated or numeric `files` value counts without
	// producing rows.
	FileCount int
}

// LoadBundle reads one bundle directory into the rows, added lines, and gate
// measurements every topology surface renders. The reader stays tolerant by
// design: PR.json may be absent, truncated, or carry counts as strings, and
// the diff headers are then the file list.
func LoadBundle(dir string) Bundle {
	diff := readDiff(dir)
	files := hunks.Files(diff)
	b := Bundle{Files: fileRows(dir, files), Added: addedLines(files), DiffBytes: len(diff)}
	b.FileCount = bundleFileCount(dir, files)
	return b
}

// readDiff returns the bundle's full diff, or "" when the fetch left none.
func readDiff(dir string) string {
	raw, err := noblock.ReadFile(dir + fullDiffFile)
	if err != nil {
		return ""
	}
	return string(raw)
}

// bundleFileCount is the gate's file count: PR.json's `files` length when the
// document carries one, else the diff header count.
func bundleFileCount(dir string, files []hunks.File) int {
	if regularFile(dir + prJSONFile) {
		if n, ok := prFilesLength(dir + prJSONFile); ok {
			return n
		}
	}
	return len(files)
}

// fileRows assigns the F1..Fn ids and resolves each row's role from roles.txt,
// falling back to the path's shape.
func fileRows(dir string, files []hunks.File) []File {
	paths := bundlePaths(dir, files)
	backfillPrevious(paths, files)
	roles := bundleRoles(dir, paths)
	hunksByPath := bundleHunkCounts(dir, files)
	rows := make([]File, 0, len(paths))
	for i, p := range paths {
		role := roles[p.path]
		if role == "" {
			role = readcommon.PathRole(p.path)
		}
		rows = append(rows, File{
			ID:           "F" + strconv.Itoa(i+1),
			Path:         p.path,
			PreviousPath: p.prev,
			Role:         role,
			Adds:         p.adds,
			Dels:         p.dels,
			Hunks:        hunksByPath[p.path],
		})
	}
	return rows
}

// bundlePaths prefers PR.json's file rows and falls back to the diff headers.
func bundlePaths(dir string, files []hunks.File) []pathRow {
	if regularFile(dir + prJSONFile) {
		if rows, ok := prPathRows(dir + prJSONFile); ok {
			return rows
		}
	}
	return pathsFromFiles(files)
}

// backfillPrevious fills a row's rename source from the diff when PR.json
// omitted it: `gh pr view --json files` carries no `previous_filename`, while
// the bundled diff still renders `rename from`, and preferring the PR rows must
// not discard what the diff already measured.
func backfillPrevious(rows []pathRow, files []hunks.File) {
	renames := diffRenames(files)
	for i := range rows {
		if rows[i].prev == "" {
			rows[i].prev = renames[rows[i].path]
		}
	}
}

// diffRenames maps each renamed destination the diff names to the path it came
// from, skipping a section whose two sides are the same path.
func diffRenames(files []hunks.File) map[string]string {
	renames := make(map[string]string, len(files))
	for i := range files {
		source := files[i].PreviousPath
		if source != "" && files[i].Path != "" && source != files[i].Path {
			renames[files[i].Path] = source
		}
	}
	return renames
}

// bundleHunkCounts prefers the fetch-time hunks.tsv snapshot and falls back
// to the diff's own "@@ " headers.
func bundleHunkCounts(dir string, files []hunks.File) map[string]int {
	if counts, ok := readHunkCounts(dir + hunksTSVFile); ok {
		return counts
	}
	counts := make(map[string]int, len(files))
	for i := range files {
		counts[files[i].Path] += files[i].HunkCount
	}
	return counts
}

// addedLines flattens the diff's added lines into (path, line) pairs.
func addedLines(files []hunks.File) [][2]string {
	var rows [][2]string
	for i := range files {
		f := &files[i]
		if f.Path == "" {
			continue
		}
		for _, ln := range f.AddedLines {
			rows = append(rows, [2]string{f.Path, ln})
		}
	}
	return rows
}

// Thresholds returns the walkthrough gate this override set names: the file
// count and diff bytes a bundle must exceed to read as large. A fragment this
// caller cannot read falls back to the built-in numbers, so a malformed or
// absent override never changes what "large" means.
func Thresholds(ref overrides.Set) (files, bytes int) {
	files, bytes = overrides.DefaultThresholds()
	if f, n, err := ref.Thresholds(); err == nil {
		files, bytes = f, n
	}
	return files, bytes
}

// Large reports whether the bundle crosses the walkthrough thresholds: more
// than the configured file count or more than the configured diff bytes. A
// fragment this caller cannot read falls back to the built-in numbers.
func Large(b Bundle, ref overrides.Set) bool {
	files, bytes := Thresholds(ref)
	return b.FileCount > files || b.DiffBytes > bytes
}

// Edges derives uses/validates relations from added diff lines that name
// another file in the same list, deduped per (source, target) pair.
func Edges(files []File, added [][2]string) []Edge {
	if len(added) == 0 {
		return nil
	}
	freq, index := edgeTargets(files)
	mentions := newMentionIndex(files, freq)
	seen := map[[2]int]bool{}
	cands := make([]int, 0, mentionCandidateCap)
	var edges []Edge
	for _, add := range added {
		cur, ok := index[add[0]]
		if !ok {
			continue
		}
		cands = mentions.candidates(add[1], cands[:0])
		edges = appendMentionEdges(edges, files, freq, seen, cur, cands)
	}
	return edges
}

// Block renders the collapsed topology block: the PR head node, one node per
// file in bundle order, then the derived edges, or "" when no file is left to
// draw. Nodes and edges are taken in bundle order until they no longer fit
// maxTopoBytes, and edges derive from the nodes actually kept, so a truncated
// block never points at a node it does not render and never loses its fences.
func Block(b Bundle, prSlug string) string {
	if len(b.Files) == 0 {
		return ""
	}
	lines := blockHead(b.Files, prSlug)
	budget := maxTopoBytes - len(blockTail) - maxNoteBytes
	used := len(strings.Join(lines, newline))
	nodes, used, filesOmitted := fitNodes(b.Files, budget, used)
	edges, _, edgesOmitted := fitEdges(Edges(nodes, b.Added), budget, used)
	for i := range nodes {
		lines = append(lines, nodeLine(&nodes[i]))
	}
	for _, edge := range edges {
		lines = append(lines, edgeLine(edge))
	}
	if note := omittedNote(filesOmitted, edgesOmitted); note != "" {
		lines = append(lines, mermaidIndent+note)
	}
	return strings.Join(append(lines, "```", "", "</details>"), newline)
}

// blockHead renders the fixed opening of every block: the <details> region,
// the mermaid fence, and the PR node.
func blockHead(rows []File, prSlug string) []string {
	return []string{
		"<details>", "<summary>" + topologySummary + "</summary>", "",
		"```mermaid", "flowchart TD", topologyHead(rows, prSlug),
	}
}

// fitNodes keeps the head of the file list the node cap and the byte budget
// allow, reporting the bytes spent and how many files are left out.
func fitNodes(files []File, budget, used int) ([]File, int, int) {
	kept := 0
	for kept < len(files) && kept < maxTopoNodes {
		cost := len(newline + nodeLine(&files[kept]))
		if used+cost > budget {
			break
		}
		used += cost
		kept++
	}
	return files[:kept], used, len(files) - kept
}

// fitEdges keeps the head of the edge list the edge cap and the byte budget
// allow, reporting the bytes spent and how many edges are left out.
func fitEdges(edges []Edge, budget, used int) ([]Edge, int, int) {
	kept := 0
	for kept < len(edges) && kept < maxTopoEdges {
		cost := len(newline + edgeLine(edges[kept]))
		if used+cost > budget {
			break
		}
		used += cost
		kept++
	}
	return edges[:kept], used, len(edges) - kept
}

// nodeLine renders one file node.
func nodeLine(node *File) string {
	return mermaidIndent + "PR --> " + node.ID + "[\"" + node.ID + " " +
		topoLabel(Sanitize(baseName(node.Path))) + "\"]"
}

// edgeLine renders one relation edge.
func edgeLine(edge Edge) string {
	return mermaidIndent + edge.From + " -->|\"" + topoLabel(Sanitize(edge.Label)) + "\"| " + edge.To
}

// topoLabel shortens one label past the single-label ceiling, cutting on a
// rune boundary so a shortened path still renders as valid UTF-8.
func topoLabel(label string) string {
	if len(label) <= maxTopoLabel {
		return label
	}
	cut := maxTopoLabel - len(labelEllipsis)
	for cut > 0 && !utf8.RuneStart(label[cut]) {
		cut--
	}
	return label[:cut] + labelEllipsis
}

// topologyHead renders the PR node. Its deltas sum the whole bundle, not the
// rendered head of it: the node describes the PR, the caps only trim the fence.
func topologyHead(rows []File, prSlug string) string {
	return mermaidIndent + "PR[\"" + Sanitize(prName(prSlug)) + " +" + strconv.Itoa(sumAdds(rows)) + "/-" +
		strconv.Itoa(sumDels(rows)) + "\"]"
}

// omittedNote names what the caps left out, so a truncated block never reads
// as a complete graph.
func omittedNote(files, edges int) string {
	var parts []string
	if files > 0 {
		parts = append(parts, strconv.Itoa(files)+filesOmittedSuffix)
	}
	if edges > 0 {
		parts = append(parts, strconv.Itoa(edges)+edgesOmittedSuffix)
	}
	if len(parts) == 0 {
		return ""
	}
	return notePrefix + strings.Join(parts, noteJoin) + noteSuffix
}

// Sanitize neutralizes the characters that would break a mermaid label or a
// markdown table cell.
func Sanitize(s string) string {
	s = strings.ReplaceAll(s, quoteMark, "'")
	s = strings.ReplaceAll(s, "|", ":")
	return strings.ReplaceAll(s, "`", "'")
}

func regularFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

func prFilesLength(path string) (int, bool) {
	m, ok := prFilesMap(path)
	if !ok {
		return 0, false
	}
	files, ok := m["files"]
	if !ok || files == nil {
		return 0, true
	}
	return filesLength(files)
}

func prFilesMap(path string) (map[string]any, bool) {
	raw, err := noblock.ReadFile(path)
	if err != nil {
		return nil, false
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return nil, false
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return nil, false
	}
	return m, true
}

func filesLength(files any) (int, bool) {
	switch t := files.(type) {
	case []any:
		return len(t), true
	case string:
		return len([]rune(t)), true
	case map[string]any:
		return len(t), true
	case bool:
		return 0, false
	default:
		return numericFilesLength(files)
	}
}

func numericFilesLength(files any) (int, bool) {
	f, ok := readcommon.NumVal(files)
	if !ok {
		return 0, false
	}
	return int(max(f, -f)), true
}

type pathRow struct {
	path       string
	prev       string
	adds, dels int
}

type filesDisposition int

const (
	filesMissing filesDisposition = iota
	filesPresent
	filesTruncate
)

func prPathRows(path string) ([]pathRow, bool) {
	list, disposition := prFilesList(path)
	switch disposition {
	case filesMissing:
		return nil, false
	case filesTruncate:
		return nil, true
	case filesPresent:
		if len(list) == 0 {
			return nil, false
		}
		return buildPathRows(list)
	}
	return nil, false
}

func prFilesList(path string) ([]any, filesDisposition) {
	m, ok := prFilesMap(path)
	if !ok {
		return nil, filesMissing
	}
	return filesDispositionOf(m)
}

func filesDispositionOf(m map[string]any) ([]any, filesDisposition) {
	files, ok := m["files"]
	if !ok || files == nil {
		return nil, filesMissing
	}
	list, ok := files.([]any)
	if !ok {
		return nil, nonArrayDisposition(files)
	}
	return list, filesPresent
}

func nonArrayDisposition(files any) filesDisposition {
	if emptyFilesValue(files) {
		return filesMissing
	}
	return filesTruncate
}

func buildPathRows(list []any) ([]pathRow, bool) {
	var rows []pathRow
	for _, f := range list {
		row, ok := prPathRow(f)
		if !ok {
			return nil, true
		}
		rows = append(rows, row)
	}
	return rows, true
}

func prPathRow(f any) (pathRow, bool) {
	fm, ok := f.(map[string]any)
	if !ok && f != nil {
		return pathRow{}, false
	}
	if fm == nil {
		return pathRow{path: nullPath}, true
	}
	row := pathRow{
		path: jq.Str(fm["path"]),
		adds: digitsOrZero(jq.Str(fm["additions"])),
		dels: digitsOrZero(jq.Str(fm["deletions"])),
	}
	row.prev = previousName(fm["previous_filename"], row.path)
	return row, true
}

// previousName reads a file entry's pre-rename path, which the host omits for
// an ordinary change. A missing, non-string, empty, or unchanged value reads
// as no previous path, so the row never claims a rename that did not happen.
func previousName(value any, path string) string {
	prev, ok := value.(string)
	if !ok || prev == "" || prev == path {
		return ""
	}
	return prev
}

func emptyFilesValue(files any) bool {
	switch t := files.(type) {
	case string:
		return len([]rune(t)) == 0
	case map[string]any:
		return len(t) == 0
	case bool:
		return false
	default:
		if f, ok := readcommon.NumVal(files); ok {
			return max(f, -f) == 0
		}
		return false
	}
}

func digitsOrZero(s string) int {
	if !readcommon.IsDigits(s) {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return n
	}
	return n
}

func bundleRoles(dir string, paths []pathRow) map[string]string {
	roles := map[string]string{}
	if regularFile(dir + rolesTxtFile) {
		raw, err := noblock.ReadFile(dir + rolesTxtFile)
		if err == nil {
			parseRolesMap(string(raw), roles)
		}
		return roles
	}
	for _, p := range paths {
		roles[p.path] = readcommon.PathRole(p.path)
	}
	return roles
}

func parseRolesMap(text string, roles map[string]string) {
	current := ""
	for ln := range strings.SplitSeq(text, newline) {
		current = parseRolesLine(ln, roles, current)
	}
}

func parseRolesLine(ln string, roles map[string]string, current string) string {
	if rest, ok := strings.CutPrefix(ln, hashSpace); ok {
		if fields := strings.Fields(rest); len(fields) > 0 {
			return fields[0]
		}
		return ""
	}
	if rest, ok := strings.CutPrefix(ln, dashSpace); ok {
		roles[rest] = current
	}
	return current
}

func readHunkCounts(path string) (map[string]int, bool) {
	if !regularFile(path) {
		return nil, false
	}
	raw, err := noblock.ReadFile(path)
	if err != nil {
		return map[string]int{}, true
	}
	counts := map[string]int{}
	for ln := range strings.SplitSeq(string(raw), newline) {
		fields := strings.Split(ln, tabSep)
		if len(fields) >= 2 && fields[1] == newMarker {
			counts[fields[0]]++
		}
	}
	return counts, true
}

func pathsFromFiles(files []hunks.File) []pathRow {
	var rows []pathRow
	index := map[string]int{}
	for i := range files {
		f := &files[i]
		if f.Path == "" {
			continue
		}
		i, ok := index[f.Path]
		if !ok {
			i = len(rows)
			index[f.Path] = i
			rows = append(rows, pathRow{path: f.Path, prev: f.PreviousPath})
		}
		if rows[i].prev == "" {
			rows[i].prev = f.PreviousPath
		}
		rows[i].adds += f.Additions
		rows[i].dels += f.Deletions
	}
	return rows
}

func appendMentionEdges(
	edges []Edge, rows []File, freq map[string]int,
	seen map[[2]int]bool, cur int, cands []int,
) []Edge {
	for _, dst := range cands {
		key := [2]int{cur, dst}
		if dst == cur || seen[key] {
			continue
		}
		seen[key] = true
		edges = append(edges, Edge{
			From:  rows[cur].ID,
			To:    rows[dst].ID,
			Label: edgeLabel(&rows[cur], &rows[dst], freq),
		})
	}
	return edges
}

func edgeTargets(rows []File) (map[string]int, map[string]int) {
	freq := map[string]int{}
	index := map[string]int{}
	for i, row := range rows {
		freq[baseName(row.Path)]++
		index[row.Path] = i
	}
	return freq, index
}

// mentionIndex matches the file paths named on each added diff line. It walks
// the rows once to pick one pattern per row, then walks each line once per
// distinct pattern length with a rolling hash.
type mentionIndex struct {
	names   []string
	slots   []mentionEntry
	rows    []int
	starts  []int
	shifts  []uint64
	lengths []int
	always  []int
	mask    uint64
}

type mentionEntry struct {
	hash uint64
	id   int
}

type mentionMembership struct {
	name string
	row  int
}

const (
	mentionHashBase uint64 = 0x100000001b3
	// mentionIndexMin is the pattern count below which scanning the patterns
	// with [strings.Contains] beats a rolling-hash window scan.
	mentionIndexMin = 12
	// mentionCandidateCap seeds the reused per-line candidate slice.
	mentionCandidateCap = 8
)

func newMentionIndex(rows []File, freq map[string]int) *mentionIndex {
	m := &mentionIndex{}
	m.collect(rows, freq)
	if len(m.names) > mentionIndexMin {
		m.index()
	}
	return m
}

// collect picks the pattern each row is matched by. A unique basename
// subsumes its full path: a line naming the path also names the basename, so
// one shorter pattern answers both probes. Rows with an empty path — or an
// empty basename that is unique — match every line.
func (m *mentionIndex) collect(rows []File, freq map[string]int) {
	members := make([]mentionMembership, 0, len(rows))
	for i := range rows {
		if pattern, always := mentionPattern(rows[i].Path, freq); always {
			m.always = append(m.always, i)
		} else {
			members = append(members, mentionMembership{name: pattern, row: i})
		}
	}
	m.buildRuns(members)
}

func (m *mentionIndex) buildRuns(members []mentionMembership) {
	slices.SortFunc(members, func(a, b mentionMembership) int {
		if c := strings.Compare(a.name, b.name); c != 0 {
			return c
		}
		return cmp.Compare(a.row, b.row)
	})
	m.rows = make([]int, len(members))
	m.names = make([]string, 0, len(members))
	m.starts = make([]int, 0, len(members)+1)
	for i := range members {
		if i == 0 || members[i].name != members[i-1].name {
			m.names = append(m.names, members[i].name)
			m.starts = append(m.starts, i)
		}
		m.rows[i] = members[i].row
	}
	m.starts = append(m.starts, len(members))
}

// mentionPattern returns the pattern a row's path is matched by. always means
// every added line mentions the row.
func mentionPattern(path string, freq map[string]int) (pattern string, always bool) {
	if path == "" {
		return "", true
	}
	base := baseName(path)
	switch {
	case freq[base] > 1:
		return path, false
	case base == "":
		return "", true
	default:
		return base, false
	}
}

// index hashes the patterns into an open-addressing table so appendWindow can
// find them in constant time. Hash collisions are kept in the probe sequence
// and verified against the pattern text.
func (m *mentionIndex) index() {
	size := 1
	for size < len(m.names)*2 {
		size <<= 1
	}
	m.mask = uint64(size - 1) //nolint:gosec // G115: size is a positive power of two.
	m.slots = make([]mentionEntry, size)
	for i := range m.slots {
		m.slots[i].id = -1
	}
	m.lengths = make([]int, 0, len(m.names))
	for id := range m.names {
		name := m.names[id]
		hash := mentionHash(name)
		slot := hash & m.mask
		for m.slots[slot].id >= 0 {
			slot = (slot + 1) & m.mask
		}
		m.slots[slot] = mentionEntry{hash: hash, id: id}
		m.lengths = append(m.lengths, len(name))
	}
	slices.Sort(m.lengths)
	m.lengths = slices.Compact(m.lengths)
	m.shifts = make([]uint64, len(m.lengths))
	for i, size := range m.lengths {
		m.shifts[i] = mentionPow(size - 1)
	}
}

// mentionPow returns base raised to exp, for rolling-hash window shifts.
func mentionPow(exp int) uint64 {
	result := uint64(1)
	base := mentionHashBase
	for exp > 0 {
		if exp&1 == 1 {
			result *= base
		}
		base *= base
		exp >>= 1
	}
	return result
}

func mentionHash(s string) uint64 {
	h := uint64(0)
	for i := range len(s) {
		h = h*mentionHashBase + uint64(s[i])
	}
	return h
}

// candidates returns the sorted, deduplicated row indexes mentioned by line.
// dst is reused across calls to keep the per-line scan allocation-free.
func (m *mentionIndex) candidates(line string, dst []int) []int {
	dst = append(dst, m.always...)
	if m.slots == nil {
		dst = m.linearCandidates(line, dst)
	} else {
		dst = m.windowCandidates(line, dst)
	}
	slices.Sort(dst)
	return slices.Compact(dst)
}

func (m *mentionIndex) linearCandidates(line string, dst []int) []int {
	for id, name := range m.names {
		if strings.Contains(line, name) {
			dst = append(dst, m.rows[m.starts[id]:m.starts[id+1]]...)
		}
	}
	return dst
}

func (m *mentionIndex) windowCandidates(line string, dst []int) []int {
	for i, size := range m.lengths {
		if size > len(line) {
			break
		}
		dst = m.scanLength(line, size, m.shifts[i], dst)
	}
	return dst
}

func (m *mentionIndex) scanLength(line string, size int, shift uint64, dst []int) []int {
	hash := mentionHash(line[:size])
	for pos := 0; ; pos++ {
		dst = m.appendWindow(hash, line[pos:pos+size], dst)
		if pos+size == len(line) {
			return dst
		}
		hash = (hash-uint64(line[pos])*shift)*mentionHashBase + uint64(line[pos+size])
	}
}

func (m *mentionIndex) appendWindow(hash uint64, window string, dst []int) []int {
	for slot := hash & m.mask; ; slot = (slot + 1) & m.mask {
		entry := &m.slots[slot]
		if entry.id < 0 {
			return dst
		}
		if entry.hash == hash && m.names[entry.id] == window {
			dst = append(dst, m.rows[m.starts[entry.id]:m.starts[entry.id+1]]...)
		}
	}
}

func edgeLabel(src, dst *File, freq map[string]int) string {
	target := dst.Path
	if freq[baseName(dst.Path)] == 1 {
		target = baseName(dst.Path)
	}
	relation := usesLabel
	if src.Role == "test" || src.Role == "config" {
		relation = validatesLabel
	}
	return relation + target
}

func baseName(path string) string {
	if _, after, ok := strings.CutLast(path, slashSep); ok {
		return after
	}
	return path
}

func prName(slug string) string {
	if slug == "" {
		return "review"
	}
	for i := range len(slug) {
		if slug[i] < '0' || slug[i] > '9' {
			return "review " + slug
		}
	}
	return "PR #" + slug
}

func sumAdds(rows []File) int {
	n := 0
	for _, row := range rows {
		n += row.Adds
	}
	return n
}

func sumDels(rows []File) int {
	n := 0
	for _, row := range rows {
		n += row.Dels
	}
	return n
}
