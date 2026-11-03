package reportcore

import (
	"os"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/overrides"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// renderOverview ports render_overview: the collapsed file-table plus
// change-topology block for large diffs, or "" when gated off. Rendering
// never fails (missing inputs read as absent, like the shell's || guards).
func renderOverview(st *state) string {
	show, forced := overviewGate(st)
	if !show {
		return ""
	}
	in := loadOverviewInputs(st.dir)
	if !forced && !in.gateOpen(st.dir) {
		return ""
	}
	paths := in.paths(st.dir)
	roles := overviewRoles(st.dir, paths)
	rows := overviewFileRows(paths, roles, in.hunkCounts())
	edges := overviewEdges(rows, in.addedLines())
	lines := overviewTableLines(rows)
	lines = append(lines, overviewTopologyLines(st, rows, edges)...)
	return strings.Join(lines, newline)
}

// overviewGate ports the forced/toggle half of the overview gate: a forced
// off always hides, a forced on always shows (skipping the size
// thresholds), otherwise the publish toggle decides. It runs before any
// input is read, so a hidden overview never opens a file.
func overviewGate(st *state) (show, forced bool) {
	switch forces := st.req.ForcesOrEmpty(); forces.PublishOverviewForce {
	case strFalse:
		return false, false
	case strTrue:
		return true, true
	}
	return st.req.Effective.PublishOverview == strTrue, false
}

// overviewInputs is the run's single read of the overview inputs: the diff
// bytes every fallback scans, and the hunks.tsv NEW-side counts. Reading
// them once per render keeps the file table linear in the diff size instead
// of re-opening the same files per row.
type overviewInputs struct {
	diff     string
	hunksTSV map[string]int
	files    []hunks.File
	hasDiff  bool
	hasTSV   bool
}

// loadOverviewInputs reads the diff and the hunks.tsv snapshot once, and
// indexes the diff's sections once for every derived column.
func loadOverviewInputs(dir string) *overviewInputs {
	in := &overviewInputs{}
	if raw, err := os.ReadFile(dir + fullDiffSuffix); err == nil {
		in.diff, in.hasDiff = string(raw), true
		in.files = hunks.Files(in.diff)
	}
	in.hunksTSV, in.hasTSV = overviewHunkCounts(dir + hunksTSVSuffix)
	return in
}

// gateOpen ports the size half of the overview gate: file and byte counts
// against the P5 walkthrough thresholds (defaults 8 files, 24576 bytes).
func (in *overviewInputs) gateOpen(dir string) bool {
	maxFiles, maxBytes := thresholdSizes()
	return in.fileCount(dir) > maxFiles || len(in.diff) > maxBytes
}

// fileCount ports the file count: PR.json files length, else diff header
// count.
func (in *overviewInputs) fileCount(dir string) int {
	if isRegular(dir + prJSONSuffix) {
		if n, ok := prFilesLength(dir + prJSONSuffix); ok {
			return n
		}
	}
	return len(in.files)
}

// paths ports the paths.tsv build: PR.json files when the record names any,
// else the diff scan.
func (in *overviewInputs) paths(dir string) []overviewPath {
	if isRegular(dir + prJSONSuffix) {
		if rows, ok := prPathRows(dir + prJSONSuffix); ok {
			return rows
		}
	}
	if !in.hasDiff {
		return nil
	}
	return pathsFromFiles(in.files)
}

// hunkCounts resolves the table's Hunks column for every path at once:
// hunks.tsv NEW-side rows when the snapshot exists, else one diff pass.
func (in *overviewInputs) hunkCounts() map[string]int {
	if in.hasTSV {
		return in.hunksTSV
	}
	counts := make(map[string]int, len(in.files))
	for i := range in.files {
		counts[in.files[i].Path] += in.files[i].HunkCount
	}
	return counts
}

// addedLines flattens the sections' added content lines into topology rows.
func (in *overviewInputs) addedLines() [][2]string {
	var rows [][2]string
	for i := range in.files {
		f := &in.files[i]
		if f.Path == "" {
			continue
		}
		for _, ln := range f.AddedLines {
			rows = append(rows, [2]string{f.Path, ln})
		}
	}
	return rows
}

// overviewFileRows numbers the path rows and attaches roles and hunk counts.
func overviewFileRows(paths []overviewPath, roles map[string]string, hunksByPath map[string]int) []fileRowShape {
	rows := make([]fileRowShape, 0, len(paths))
	for i, p := range paths {
		role := roles[p.path]
		if role == "" {
			role = readcommon.PathRole(p.path)
		}
		rows = append(rows, fileRowShape{
			id:    "F" + strconv.Itoa(i+1),
			path:  p.path,
			role:  role,
			adds:  p.adds,
			dels:  p.dels,
			hunks: hunksByPath[p.path],
		})
	}
	return rows
}

// overviewTableLines ports the collapsed file-table block.
func overviewTableLines(rows []fileRowShape) []string {
	lines := []string{
		"<details>", "<summary>Files changed (" + strconv.Itoa(len(rows)) + ")</summary>", "",
		"| # | File | +/- | Hunks | Role |", "|---|---|---|---|---|",
	}
	for _, row := range rows {
		lines = append(lines, overviewTableRow(row))
	}
	return append(lines, "", "</details>", "")
}

// overviewTableRow renders one file-table row.
func overviewTableRow(row fileRowShape) string {
	return "| " + row.id + " | `" + sanitize(row.path) + "` | +" +
		strconv.Itoa(row.adds) + "/-" + strconv.Itoa(row.dels) +
		" | " + strconv.Itoa(row.hunks) + " | " + row.role + " |"
}

// overviewTopologyLines ports the change-topology block, or nil when the
// structural-diagrams toggle is off.
func overviewTopologyLines(st *state, rows []fileRowShape, edges []overviewEdge) []string {
	if st.req.Effective.DiagramsStructural != strTrue {
		return nil
	}
	head := "    PR[\"" + sanitize(prName(st.req.Pr)) + " +" + strconv.Itoa(sumAdds(rows)) + "/-" +
		strconv.Itoa(sumDels(rows)) + "\"]"
	lines := []string{
		"<details>", "<summary>Change topology</summary>", "",
		"```mermaid", "flowchart TD", head,
	}
	for _, row := range rows {
		lines = append(lines, "    PR --> "+row.id+"[\""+row.id+" "+sanitize(baseName(row.path))+"\"]")
	}
	for _, edge := range edges {
		lines = append(lines, "    "+edge.from+" -->|\""+sanitize(edge.label)+"\"| "+edge.to)
	}
	return append(lines, "```", "", "</details>")
}

// thresholdSizes reads the P5 walkthrough thresholds (validated earlier, so
// errors degrade to defaults here).
func thresholdSizes() (int, int) {
	files, bytes, err := overrides.Thresholds()
	if err != nil {
		return defaultMaxFiles, defaultMaxBytes
	}
	return files, bytes
}

// overview thresholds defaults, mirroring the shell.
const (
	defaultMaxFiles = 8
	defaultMaxBytes = 24576
)

// prFilesLength ports `(.files // []) | length` with a validity flag: a
// boolean files value fails the length (mirroring the jq error, which sends
// the shell down the diff-fallback path).
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

// prFilesMap loads the PR.json record as a map, or false when the record is
// missing or malformed.
func prFilesMap(path string) (map[string]any, bool) {
	raw, err := os.ReadFile(path)
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

// filesLength ports the files length: arrays and objects count members,
// strings count runes, and numbers read through NumVal.
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

// numericFilesLength reads a numeric files value, mirroring the shell's
// absolute-value guard on degenerate counts.
func numericFilesLength(files any) (int, bool) {
	f, ok := readcommon.NumVal(files)
	if !ok {
		return 0, false
	}
	if f < 0 {
		f = -f
	}
	return int(f), true
}

// overviewPath is one paths.tsv row.
type overviewPath struct {
	path       string
	adds, dels int
}

// filesDisposition tells prPathRows whether the files value names rows,
// reads empty (fall back to the diff), or truncates (zero rows).
type filesDisposition int

const (
	filesMissing filesDisposition = iota
	filesPresent
	filesTruncate
)

// prPathRows renders PR.json files as path rows. A non-empty files value
// that is not an array, or a non-object entry, renders zero rows (the
// shell's `jq -r` fails and truncates paths.tsv); an empty files value
// reports not-ok so the caller falls back to the diff.
func prPathRows(path string) ([]overviewPath, bool) {
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

// prFilesList loads the PR.json files array with its disposition.
func prFilesList(path string) ([]any, filesDisposition) {
	m, ok := prFilesMap(path)
	if !ok {
		return nil, filesMissing
	}
	return filesDispositionOf(m)
}

// filesDispositionOf classifies the files value of a decoded PR.json record.
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

// nonArrayDisposition classifies a non-array files value: empty values read
// missing (sending the shell down the diff-fallback path), anything else
// truncates.
func nonArrayDisposition(files any) filesDisposition {
	if emptyFilesValue(files) {
		return filesMissing
	}
	return filesTruncate
}

// buildPathRows renders one overview row per files entry. A non-object
// entry truncates the whole table (mirroring the shell's `jq -r` failure).
func buildPathRows(list []any) ([]overviewPath, bool) {
	var rows []overviewPath
	for _, f := range list {
		row, ok := prPathRow(f)
		if !ok {
			return nil, true
		}
		rows = append(rows, row)
	}
	return rows, true
}

// prPathRow renders one files entry: objects read path and counts, null
// entries read a null path, and anything else truncates.
func prPathRow(f any) (overviewPath, bool) {
	fm, ok := f.(map[string]any)
	if !ok && f != nil {
		return overviewPath{}, false
	}
	if fm == nil {
		return overviewPath{path: nullPath}, true
	}
	return overviewPath{
		path: jq.Str(fm["path"]),
		adds: digitsOrZero(jq.Str(fm["additions"])),
		dels: digitsOrZero(jq.Str(fm["deletions"])),
	}, true
}

// emptyFilesValue reports whether a non-array files value reads empty
// (sending the shell down the diff-fallback path instead of truncating).
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
			if f < 0 {
				f = -f
			}
			return f == 0
		}
		return false
	}
}

// digitsOrZero ports the paths read loop: non-digit counts read 0. IsDigits
// admits only ASCII digits, so Atoi fails solely on overflow; Atoi still
// returns the clamped value then, matching the shell arithmetic, so the
// clamped value is kept.
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

// overviewRoles ports the roles.map build: roles.txt first, else the batch
// roles_for_paths fallback over every path.
func overviewRoles(dir string, paths []overviewPath) map[string]string {
	roles := map[string]string{}
	if isRegular(dir + rolesTxtSuffix) {
		raw, err := os.ReadFile(dir + rolesTxtSuffix)
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

// parseRolesMap parses "## role" headers and "- path" bullets.
func parseRolesMap(text string, roles map[string]string) {
	current := ""
	for ln := range strings.SplitSeq(text, newline) {
		current = parseRolesLine(ln, roles, current)
	}
}

// parseRolesLine consumes one roles.txt line: headers switch the current
// role, bullets file the path under it.
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

// overviewHunkCounts ports the hunks.tsv build for every path in one read:
// NEW-side rows per file. A non-regular snapshot reports not-ok, so rows
// fall back to the diff scan.
func overviewHunkCounts(path string) (map[string]int, bool) {
	if !isRegular(path) {
		return nil, false
	}
	raw, err := os.ReadFile(path)
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

// pathsFromFiles ports the paths fallback: first-seen path order with the
// added/removed line counts of every section naming it.
func pathsFromFiles(files []hunks.File) []overviewPath {
	var rows []overviewPath
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
			rows = append(rows, overviewPath{path: f.Path})
		}
		rows[i].adds += f.Additions
		rows[i].dels += f.Deletions
	}
	return rows
}

// overviewEdge is one topology edge.
type overviewEdge struct {
	from, to string
	label    string
}

// overviewEdges ports the gen.awk edge join: added-line mentions of another
// changed file (basename, or full path when basenames collide).
func overviewEdges(rows []fileRowShape, added [][2]string) []overviewEdge {
	freq, index := edgeTargets(rows)
	seen := map[[2]int]bool{}
	var edges []overviewEdge
	for _, add := range added {
		cur, ok := index[add[0]]
		if !ok {
			continue
		}
		edges = appendMentionEdges(edges, rows, freq, seen, cur, add[1])
	}
	return edges
}

// edgeTargets indexes rows by path and counts basename frequency.
func edgeTargets(rows []fileRowShape) (map[string]int, map[string]int) {
	freq := map[string]int{}
	index := map[string]int{}
	for i, row := range rows {
		freq[baseName(row.path)]++
		index[row.path] = i
	}
	return freq, index
}

// appendMentionEdges appends one edge per other file mentioned by line.
func appendMentionEdges(
	edges []overviewEdge, rows []fileRowShape, freq map[string]int,
	seen map[[2]int]bool, cur int, line string,
) []overviewEdge {
	for j, row := range rows {
		edges = appendMentionEdge(edges, rows, freq, seen, cur, line, j, row)
	}
	return edges
}

// appendMentionEdge appends the edge to row when line mentions it and the
// pair is not linked yet.
func appendMentionEdge(
	edges []overviewEdge, rows []fileRowShape, freq map[string]int,
	seen map[[2]int]bool, cur int, line string, j int, row fileRowShape,
) []overviewEdge {
	if j == cur {
		return edges
	}
	if !mentions(line, row.path, freq[baseName(row.path)] == 1) {
		return edges
	}
	key := [2]int{cur, j}
	if seen[key] {
		return edges
	}
	seen[key] = true
	return append(edges, overviewEdge{from: rows[cur].id, to: row.id, label: edgeLabel(rows[cur], row, freq)})
}

// edgeLabel ports the gen.awk edge label: the validating relation for test
// and config sources, otherwise the uses relation, plus the basename when
// unique and the full path when basenames collide.
func edgeLabel(src, dst fileRowShape, freq map[string]int) string {
	target := dst.path
	if freq[baseName(dst.path)] == 1 {
		target = baseName(dst.path)
	}
	relation := "uses "
	if src.role == "test" || src.role == "config" {
		relation = "validates "
	}
	return relation + target
}

// fileRowShape is one overview table row.
type fileRowShape struct {
	id, path, role string
	adds, dels     int
	hunks          int
}

// mentions ports the edge hit test: a bare basename mention (when unique),
// or a full-path mention.
func mentions(line, path string, uniqueBase bool) bool {
	if uniqueBase && strings.Contains(line, baseName(path)) {
		return true
	}
	return strings.Contains(line, path)
}

// baseName ports the gen.awk basename (text after the last slash).
func baseName(path string) string {
	if _, after, ok := strings.CutLast(path, slashSep); ok {
		return after
	}
	return path
}

// sanitize ports the gen.awk san function.
func sanitize(s string) string {
	s = strings.ReplaceAll(s, quoteMark, "'")
	s = strings.ReplaceAll(s, "|", ":")
	return strings.ReplaceAll(s, "`", "'")
}

// prName ports the review/PR name for the topology head.
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

// sumAdds sums row additions.
func sumAdds(rows []fileRowShape) int {
	n := 0
	for _, row := range rows {
		n += row.adds
	}
	return n
}

// sumDels sums row deletions.
func sumDels(rows []fileRowShape) int {
	n := 0
	for _, row := range rows {
		n += row.dels
	}
	return n
}
