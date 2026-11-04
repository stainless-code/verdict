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

func overviewGate(st *state) (show, forced bool) {
	switch forces := st.req.ForcesOrEmpty(); forces.PublishOverviewForce {
	case strFalse:
		return false, false
	case strTrue:
		return true, true
	}
	return st.req.Effective.PublishOverview == strTrue, false
}

type overviewInputs struct {
	diff     string
	hunksTSV map[string]int
	files    []hunks.File
	hasDiff  bool
	hasTSV   bool
}

func loadOverviewInputs(dir string) *overviewInputs {
	in := &overviewInputs{}
	if raw, err := os.ReadFile(dir + fullDiffSuffix); err == nil {
		in.diff, in.hasDiff = string(raw), true
		in.files = hunks.Files(in.diff)
	}
	in.hunksTSV, in.hasTSV = overviewHunkCounts(dir + hunksTSVSuffix)
	return in
}

func (in *overviewInputs) gateOpen(dir string) bool {
	maxFiles, maxBytes := thresholdSizes()
	return in.fileCount(dir) > maxFiles || len(in.diff) > maxBytes
}

func (in *overviewInputs) fileCount(dir string) int {
	if isRegular(dir + prJSONSuffix) {
		if n, ok := prFilesLength(dir + prJSONSuffix); ok {
			return n
		}
	}
	return len(in.files)
}

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

func overviewTableRow(row fileRowShape) string {
	return "| " + row.id + " | `" + sanitize(row.path) + "` | +" +
		strconv.Itoa(row.adds) + "/-" + strconv.Itoa(row.dels) +
		" | " + strconv.Itoa(row.hunks) + " | " + row.role + " |"
}

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

func thresholdSizes() (int, int) {
	files, bytes, err := overrides.Thresholds()
	if err != nil {
		return defaultMaxFiles, defaultMaxBytes
	}
	return files, bytes
}

const (
	defaultMaxFiles = 8
	defaultMaxBytes = 24576
)

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
	if f < 0 {
		f = -f
	}
	return int(f), true
}

type overviewPath struct {
	path       string
	adds, dels int
}

type filesDisposition int

const (
	filesMissing filesDisposition = iota
	filesPresent
	filesTruncate
)

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

type overviewEdge struct {
	from, to string
	label    string
}

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

func edgeTargets(rows []fileRowShape) (map[string]int, map[string]int) {
	freq := map[string]int{}
	index := map[string]int{}
	for i, row := range rows {
		freq[baseName(row.path)]++
		index[row.path] = i
	}
	return freq, index
}

func appendMentionEdges(
	edges []overviewEdge, rows []fileRowShape, freq map[string]int,
	seen map[[2]int]bool, cur int, line string,
) []overviewEdge {
	for j, row := range rows {
		edges = appendMentionEdge(edges, rows, freq, seen, cur, line, j, row)
	}
	return edges
}

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

type fileRowShape struct {
	id, path, role string
	adds, dels     int
	hunks          int
}

func mentions(line, path string, uniqueBase bool) bool {
	if uniqueBase && strings.Contains(line, baseName(path)) {
		return true
	}
	return strings.Contains(line, path)
}

func baseName(path string) string {
	if _, after, ok := strings.CutLast(path, slashSep); ok {
		return after
	}
	return path
}

func sanitize(s string) string {
	s = strings.ReplaceAll(s, quoteMark, "'")
	s = strings.ReplaceAll(s, "|", ":")
	return strings.ReplaceAll(s, "`", "'")
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

func sumAdds(rows []fileRowShape) int {
	n := 0
	for _, row := range rows {
		n += row.adds
	}
	return n
}

func sumDels(rows []fileRowShape) int {
	n := 0
	for _, row := range rows {
		n += row.dels
	}
	return n
}
