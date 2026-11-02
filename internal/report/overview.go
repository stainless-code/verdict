package report

import (
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/overrides"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// renderOverview ports render_overview: the collapsed file-table plus
// change-topology block for large diffs, or "" when gated off. Rendering
// never fails (missing inputs read as absent, like the shell's || guards).
func renderOverview(st *state) string {
	forces := st.req.ForcesOrEmpty()
	if forces.PublishOverviewForce == "false" {
		return ""
	}
	if forces.PublishOverviewForce != "true" {
		if st.runner.Effective.PublishOverview != "true" {
			return ""
		}
		files, maxFiles, bytes, maxBytes := overviewSizes(st.dir)
		if files <= maxFiles && bytes <= maxBytes {
			return ""
		}
	}
	paths := overviewPaths(st.dir)
	roles := overviewRoles(st.dir, paths)
	rows := make([]fileRowShape, 0, len(paths))
	for i, p := range paths {
		role := roles[p.path]
		if role == "" {
			role = pathRole(p.path)
		}
		rows = append(rows, fileRowShape{
			id:    "F" + strconv.Itoa(i+1),
			path:  p.path,
			adds:  p.adds,
			dels:  p.dels,
			hunks: overviewHunks(st.dir, p.path),
			role:  role,
		})
	}
	added := overviewAdded(st.dir)
	edges := overviewEdges(rows, added)
	var lines []string
	lines = append(lines, "<details>", "<summary>Files changed ("+strconv.Itoa(len(rows))+")</summary>", "",
		"| # | File | +/- | Hunks | Role |", "|---|---|---|---|---|")
	for _, row := range rows {
		lines = append(lines, "| "+row.id+" | `"+sanitize(row.path)+"` | +"+strconv.Itoa(row.adds)+"/-"+strconv.Itoa(row.dels)+" | "+strconv.Itoa(row.hunks)+" | "+row.role+" |")
	}
	lines = append(lines, "", "</details>", "")
	if st.runner.Effective.DiagramsStructural == "true" {
		lines = append(lines, "<details>", "<summary>Change topology</summary>", "",
			"```mermaid", "flowchart TD",
			"    PR[\""+sanitize(prName(st.req.Pr))+" +"+strconv.Itoa(sumAdds(rows))+"/-"+strconv.Itoa(sumDels(rows))+"\"]")
		for _, row := range rows {
			lines = append(lines, "    PR --> "+row.id+"[\""+row.id+" "+sanitize(baseName(row.path))+"\"]")
		}
		for _, edge := range edges {
			lines = append(lines, "    "+edge.from+" -->|\""+sanitize(edge.label)+"\"| "+edge.to)
		}
		lines = append(lines, "```", "", "</details>")
	}
	return strings.Join(lines, "\n")
}

// overviewSizes ports the threshold gate: file and byte counts against the
// P5 walkthrough thresholds (defaults 8 files, 24576 bytes).
func overviewSizes(dir string) (files, maxFiles int, bytes, maxBytes int) {
	maxFiles, maxBytes, _ = thresholdSizes()
	files = overviewFileCount(dir)
	bytes = overviewByteCount(dir)
	return files, maxFiles, bytes, maxBytes
}

// thresholdSizes reads the P5 walkthrough thresholds (validated earlier, so
// errors degrade to defaults here).
func thresholdSizes() (int, int, error) {
	files, bytes, err := overrides.Thresholds()
	if err != nil {
		return defaultMaxFiles, defaultMaxBytes, err
	}
	return files, bytes, nil
}

// overview thresholds defaults, mirroring the shell.
const (
	defaultMaxFiles = 8
	defaultMaxBytes = 24576
)

// overviewFileCount ports the file count: PR.json files length, else diff
// header count.
func overviewFileCount(dir string) int {
	if isRegular(dir + "/PR.json") {
		if n, ok := prFilesLength(dir + "/PR.json"); ok {
			return n
		}
	}
	raw, err := os.ReadFile(dir + "/full.diff")
	if err != nil {
		return 0
	}
	return countDiffHeaders(string(raw))
}

// prFilesLength ports `(.files // []) | length` with a validity flag: a
// boolean files value fails the length (mirroring the jq error, which sends
// the shell down the diff-fallback path).
func prFilesLength(path string) (int, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return 0, false
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return 0, false
	}
	files, ok := m["files"]
	if !ok || files == nil {
		return 0, true
	}
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
		if f, ok := readcommon.NumVal(files); ok {
			if f < 0 {
				f = -f
			}
			return int(f), true
		}
		return 0, false
	}
}

// overviewByteCount ports `wc -c` over the diff.
func overviewByteCount(dir string) int {
	raw, err := os.ReadFile(dir + "/full.diff")
	if err != nil {
		return 0
	}
	return len(raw)
}

// countDiffHeaders counts `diff --git ` header lines.
func countDiffHeaders(diff string) int {
	n := 0
	for _, ln := range strings.Split(diff, "\n") {
		if strings.HasPrefix(ln, "diff --git ") {
			n++
		}
	}
	return n
}

// overviewPath is one paths.tsv row.
type overviewPath struct {
	path       string
	adds, dels int
}

// overviewPaths ports the paths.tsv build: PR.json files when the record
// names any, else the diff scan.
func overviewPaths(dir string) []overviewPath {
	if isRegular(dir + "/PR.json") {
		if rows, ok := prPathRows(dir + "/PR.json"); ok {
			return rows
		}
	}
	if isRegular(dir + "/full.diff") {
		raw, err := os.ReadFile(dir + "/full.diff")
		if err == nil {
			return scanFiles(string(raw))
		}
	}
	return nil
}

// prPathRows renders PR.json files as path rows. A non-empty files value
// that is not an array, or a non-object entry, renders zero rows (the
// shell's `jq -r` fails and truncates paths.tsv); an empty files value
// reports not-ok so the caller falls back to the diff.
func prPathRows(path string) ([]overviewPath, bool) {
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
	files, ok := m["files"]
	if !ok || files == nil {
		return nil, false
	}
	list, ok := files.([]any)
	if !ok {
		if emptyFilesValue(files) {
			return nil, false
		}
		return nil, true
	}
	if len(list) == 0 {
		return nil, false
	}
	var rows []overviewPath
	for _, f := range list {
		fm, ok := f.(map[string]any)
		if !ok && f != nil {
			return nil, true
		}
		var path string
		var adds, dels int
		if fm != nil {
			path = jq.Str(fm["path"])
			adds = digitsOrZero(jq.Str(fm["additions"]))
			dels = digitsOrZero(jq.Str(fm["deletions"]))
		} else {
			path = "null"
		}
		rows = append(rows, overviewPath{path: path, adds: adds, dels: dels})
	}
	return rows, true
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

// digitsOrZero ports the paths read loop: non-digit counts read 0.
func digitsOrZero(s string) int {
	if !readcommon.IsDigits(s) {
		return 0
	}
	n, _ := strconv.Atoi(s)
	return n
}

// scanFiles ports the scan.awk files mode: ordered unique paths with
// added/removed line counts.
func scanFiles(diff string) []overviewPath {
	var order []string
	seen := map[string]bool{}
	adds := map[string]int{}
	dels := map[string]int{}
	cur := ""
	for _, ln := range strings.Split(diff, "\n") {
		if strings.HasPrefix(ln, "diff --git ") {
			cur = setCurrent(strings.TrimPrefix(ln, "diff --git "))
			if cur != "" && !seen[cur] {
				seen[cur] = true
				order = append(order, cur)
			}
			continue
		}
		if cur == "" {
			continue
		}
		if strings.HasPrefix(ln, "+") && !strings.HasPrefix(ln, "+++") {
			adds[cur]++
		} else if strings.HasPrefix(ln, "-") && !strings.HasPrefix(ln, "---") {
			dels[cur]++
		}
	}
	rows := make([]overviewPath, 0, len(order))
	for _, path := range order {
		rows = append(rows, overviewPath{path: path, adds: adds[path], dels: dels[path]})
	}
	return rows
}

// setCurrent ports the scan.awk setcur header parse: quoted headers
// unquote the same way, otherwise the b-side wins.
func setCurrent(hdr string) string {
	if strings.HasPrefix(hdr, "\"") {
		t := hdr[1:]
		t2 := afterFirst(t, "\"")
		cur := afterFirst(t2, "\"")
		cur = strings.TrimSuffix(cur, "\"")
		return strings.TrimPrefix(cur, "b/")
	}
	if i := strings.Index(hdr, " b/"); i >= 0 {
		return hdr[i+3:]
	}
	cur := hdr
	if i := strings.Index(hdr, " "); i >= 0 {
		cur = hdr[i+1:]
	}
	return strings.TrimPrefix(cur, "b/")
}

// afterFirst returns the text after the first occurrence of sep, or the
// whole string when sep is absent (mirroring awk's 1-based substr/index
// behavior when index returns 0).
func afterFirst(s, sep string) string {
	if i := strings.Index(s, sep); i >= 0 {
		return s[i+len(sep):]
	}
	return s
}

// overviewRoles ports the roles.map build: roles.txt first, else the batch
// roles_for_paths fallback over every path.
func overviewRoles(dir string, paths []overviewPath) map[string]string {
	roles := map[string]string{}
	if isRegular(dir + "/roles.txt") {
		raw, err := os.ReadFile(dir + "/roles.txt")
		if err == nil {
			parseRolesMap(string(raw), roles)
		}
		return roles
	}
	for _, p := range paths {
		roles[p.path] = pathRole(p.path)
	}
	return roles
}

// parseRolesMap parses "## role" headers and "- path" bullets.
func parseRolesMap(text string, roles map[string]string) {
	current := ""
	for _, ln := range strings.Split(text, "\n") {
		if strings.HasPrefix(ln, "## ") {
			fields := strings.Fields(strings.TrimPrefix(ln, "## "))
			current = ""
			if len(fields) > 0 {
				current = fields[0]
			}
			continue
		}
		if strings.HasPrefix(ln, "- ") {
			roles[strings.TrimPrefix(ln, "- ")] = current
		}
	}
}

// role patterns, mirroring roles_for_paths.
var (
	roleTestRe       = regexp.MustCompile(`(^|/)(test|tests|__tests__|e2e|spec)(s)?/`)
	roleTestBaseRe   = regexp.MustCompile(`^(test_|spec_)`)
	roleTestExtRe    = regexp.MustCompile(`[._](test|spec)\.`)
	roleDocsBaseRe   = regexp.MustCompile(`\.md$|\.txt$|^LICENSE`)
	roleConfigBaseRe = regexp.MustCompile(`^(package\.json|tsconfig.*|Dockerfile.*|Makefile|CMakeLists\.txt|pom\.xml|pyproject\.toml|go\.mod|go\.sum|Cargo\.toml|Gemfile)`)
	roleConfigRcRe   = regexp.MustCompile(`^\..*rc`)
	roleConfigExtRe  = regexp.MustCompile(`\.(yml|yaml|toml)$`)
)

// pathRole ports the roles_for_paths role function.
func pathRole(path string) string {
	base := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		base = path[i+1:]
	}
	switch {
	case roleTestRe.MatchString(path) || roleTestBaseRe.MatchString(base) || roleTestExtRe.MatchString(base):
		return "test"
	case isGeneratedPath(path, base):
		return "generated"
	case roleDocsBaseRe.MatchString(base) || strings.HasPrefix(path, "docs/"):
		return "docs"
	case roleConfigBaseRe.MatchString(base) || roleConfigRcRe.MatchString(base) || roleConfigExtRe.MatchString(base):
		return "config"
	default:
		return "source"
	}
}

// generatedSubstrings lists the generated-path literal markers.
var generatedSubstrings = []string{
	"package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock",
	"Cargo.lock", "Gemfile.lock", "poetry.lock", ".min.js", ".min.css",
	"__snapshots__/", ".snap", ".pb.go", ".g.dart", ".generated.",
}

// isGeneratedPath ports the generated branch, including the start-anchored
// dist/build/vendor prefix.
func isGeneratedPath(path, base string) bool {
	for _, sub := range generatedSubstrings {
		if strings.Contains(path, sub) {
			return true
		}
	}
	for _, prefix := range []string{"dist/", "build/", "vendor/"} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return strings.HasSuffix(base, ".snap")
}

// overviewHunks ports overview_hunks: hunks.tsv NEW-side counts, else the
// diff hunks scan, else 0.
func overviewHunks(dir, path string) int {
	if isRegular(dir + "/hunks.tsv") {
		raw, err := os.ReadFile(dir + "/hunks.tsv")
		if err != nil {
			return 0
		}
		n := 0
		for _, ln := range strings.Split(string(raw), "\n") {
			fields := strings.Split(ln, "\t")
			if len(fields) >= 2 && fields[0] == path && fields[1] == "NEW" {
				n++
			}
		}
		return n
	}
	raw, err := os.ReadFile(dir + "/full.diff")
	if err != nil {
		return 0
	}
	return countHunkPaths(string(raw), path)
}

// countHunkPaths ports the hunks-mode fallback: one count per @@ header
// naming the path.
func countHunkPaths(diff, path string) int {
	n := 0
	cur := ""
	for _, ln := range strings.Split(diff, "\n") {
		if strings.HasPrefix(ln, "diff --git ") {
			cur = setCurrent(strings.TrimPrefix(ln, "diff --git "))
			continue
		}
		if strings.HasPrefix(ln, "@@ ") && cur == path {
			n++
		}
	}
	return n
}

// overviewAdded ports the added-mode scan: path plus added-line rows.
func overviewAdded(dir string) [][2]string {
	raw, err := os.ReadFile(dir + "/full.diff")
	if err != nil {
		return nil
	}
	var rows [][2]string
	cur := ""
	for _, ln := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(ln, "diff --git ") {
			cur = setCurrent(strings.TrimPrefix(ln, "diff --git "))
			continue
		}
		if cur == "" {
			continue
		}
		if strings.HasPrefix(ln, "+") && !strings.HasPrefix(ln, "+++") {
			rows = append(rows, [2]string{cur, ln[1:]})
		}
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
	freq := map[string]int{}
	index := map[string]int{}
	for i, row := range rows {
		freq[baseName(row.path)]++
		index[row.path] = i
	}
	seen := map[[2]int]bool{}
	var edges []overviewEdge
	for _, add := range added {
		cur, ok := index[add[0]]
		if !ok {
			continue
		}
		for j, row := range rows {
			if j == cur {
				continue
			}
			if !mentions(add[1], row.path, freq[baseName(row.path)] == 1) {
				continue
			}
			key := [2]int{cur, j}
			if seen[key] {
				continue
			}
			seen[key] = true
			target := row.path
			if freq[baseName(row.path)] == 1 {
				target = baseName(row.path)
			}
			relation := "uses "
			if rows[cur].role == "test" || rows[cur].role == "config" {
				relation = "validates "
			}
			edges = append(edges, overviewEdge{from: rows[cur].id, to: row.id, label: relation + target})
		}
	}
	return edges
}

// fileRowShape is one overview table row.
type fileRowShape struct {
	id, path string
	adds     int
	dels     int
	hunks    int
	role     string
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
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// sanitize ports the gen.awk san function.
func sanitize(s string) string {
	s = strings.ReplaceAll(s, "\"", "'")
	s = strings.ReplaceAll(s, "|", ":")
	return strings.ReplaceAll(s, "`", "'")
}

// prName ports the review/PR name for the topology head.
func prName(slug string) string {
	if slug == "" {
		return "review"
	}
	for i := 0; i < len(slug); i++ {
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
