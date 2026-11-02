package fetchcore

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/jq"
)

// buildHunksTSV ports build_hunks_tsv via the shared hunk index: identical
// rows (the one degenerate "@@ "-with-no-ranges line reads empty in Go and
// a 0/0 row pair in awk — unreachable from real diffs).
func buildHunksTSV(fullDiff, out string) error {
	raw, err := os.ReadFile(fullDiff)
	if err != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not read %s: %v", fullDiff, err)}
	}
	if werr := atomic.WriteFile(out, []byte(hunks.Format(hunks.Parse(string(raw))))); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write %s: %v", out, werr)}
	}
	return nil
}

// diffFiles ports the toc coverage-manifest file scan: paths with hunks in
// diff order, sorted unique; the manifest file lands beside the bundle.
func diffFiles(fullDiff, dir string) ([]string, error) {
	raw, err := os.ReadFile(fullDiff)
	if err != nil {
		return nil, &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not read %s: %v", fullDiff, err)}
	}
	files := diffHunkFiles(string(raw))
	slices.Sort(files)
	// The shell pipes through `sort -u`; identical contents dedupe here.
	files = dedupeSorted(files)
	var sb strings.Builder
	for _, f := range files {
		sb.WriteString(f + newline)
	}
	if werr := atomic.WriteFile(dir+"/.difffiles", []byte(sb.String())); werr != nil {
		return nil, &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write %s/.difffiles: %v", dir, werr)}
	}
	return files, nil
}

// diffHunkFiles ports the header/hunk scan: each header flushes the
// previous path when hunks followed it (the shell's grep -B pipeline).
func diffHunkFiles(raw string) []string {
	h := &hunkScan{}
	for _, line := range splitLines(raw) {
		h.visit(line)
	}
	if h.current != "" && h.has {
		h.files = append(h.files, h.current)
	}
	return h.files
}

// hunkScan carries the header/hunk scan state across lines.
type hunkScan struct {
	current string
	files   []string
	has     bool
}

// visit feeds one diff line through the scan: a header flushes the
// previous path when hunks followed it, and a hunk line arms the flag.
func (h *hunkScan) visit(line string) {
	if strings.HasPrefix(line, "diff --git ") {
		if h.current != "" && h.has {
			h.files = append(h.files, h.current)
		}
		h.current, h.has = diffFilePath(line), false
		return
	}
	if strings.HasPrefix(line, "@@") {
		h.has = true
	}
}

// dedupeSorted drops adjacent duplicates from a sorted slice.
func dedupeSorted(in []string) []string {
	out := in[:0]
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// toJSONArray ports to_json_array: newline-separated input to a JSON array
// (empty reads []).
func toJSONArray(s string) []any {
	var out []any
	for line := range strings.Lines(s) {
		line = strings.TrimSuffix(line, newline)
		if line == "" && s == "" {
			continue
		}
		out = append(out, line)
	}
	if out == nil {
		return []any{}
	}
	return out
}

// writeTocRolesGitHub ports write_toc_roles on the GitHub path: the PR
// title line, the grep -n diff header index, the gh files stat, and the
// coverage manifest with the omitted list.
func writeTocRolesGitHub(ctx *fetchRunCtx, total string, statLines []string, withHunks []string, omitted []string) error {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# TOC for PR #%s (%s) — read ranges as coverage checklist\n\n", ctx.pr, ctx.repo)
	sb.WriteString(diffHeaderIndex(ctx.dir + suffixFullDiff))
	sb.WriteString("\n## stat\n")
	for _, l := range statLines {
		sb.WriteString(l + newline)
	}
	sb.WriteString("\n## coverage manifest\n")
	fmt.Fprintf(&sb, "files in PR: %s | files with diff hunks: %d\n", total, len(withHunks))
	if len(omitted) > 0 {
		sb.WriteString("omitted (no hunks — binary, renamed without changes, or mode-only):\n")
		for _, p := range omitted {
			sb.WriteString("- " + p + newline)
		}
	} else {
		sb.WriteString("omitted: (none)\n")
	}
	if werr := atomic.WriteFile(ctx.dir+"/toc.txt", []byte(sb.String())); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write %s/toc.txt: %v", ctx.dir, werr)}
	}
	fmt.Fprintf(ctx.runner.Human, "» roles -> %s/roles.txt (heuristic file roles for triage)\n", ctx.dir)
	paths := prFilePaths(ctx.dir + suffixPRJSON)
	if werr := atomic.WriteFile(ctx.dir+"/roles.txt", []byte(rolesTextEmpty(paths))); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write %s/roles.txt: %v", ctx.dir, werr)}
	}
	return nil
}

// prFilePaths reads the PR.json file paths (nil on any failure; the stat
// total above already degraded the same way).
func prFilePaths(prJSON string) []string {
	raw, err := os.ReadFile(prJSON)
	if err != nil {
		return nil
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return nil
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return nil
	}
	var out []string
	for _, f := range jq.Elements(m[keyFiles]) {
		fm, ok := f.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, jq.StrOf(fm[keyPath]))
	}
	return out
}

// diffHeaderIndex ports `grep -n "^diff --git" file || echo "(no diff
// headers — empty diff?)"`: numbered header lines or the fallback sentence.
func diffHeaderIndex(fullDiff string) string {
	raw, err := os.ReadFile(fullDiff)
	if err != nil {
		return "(no diff headers — empty diff?)\n"
	}
	var sb strings.Builder
	n := 0
	for _, line := range splitLines(string(raw)) {
		n++
		if strings.HasPrefix(line, "diff --git ") {
			fmt.Fprintf(&sb, "%d:%s\n", n, line)
		}
	}
	if sb.Len() == 0 {
		return "(no diff headers — empty diff?)\n"
	}
	return sb.String()
}

// fileStatLine renders one `path (+adds/-dels)` stat line, with jq's null
// rendering for missing counts.
func fileStatLine(path string, adds, dels any) string {
	return fmt.Sprintf("%s (+%s/-%s)", path, statNum(adds), statNum(dels))
}

// statNum renders a stat count the way jq string interpolation does (null
// stays nullStr).
func statNum(v any) string {
	if v == nil {
		return nullStr
	}
	return jq.Str(v)
}

// omittedPaths ports the omitted computation: PR file-list paths absent
// from the hunked set.
func omittedPaths(listed, withHunks []string) []string {
	inHunks := map[string]bool{}
	for _, p := range withHunks {
		inHunks[p] = true
	}
	var out []string
	for _, p := range listed {
		if !inHunks[p] {
			out = append(out, p)
		}
	}
	return out
}

// joinLines joins paths with newlines (the omitted/extra inputs).
func joinLines(paths []string) string {
	return strings.Join(paths, newline)
}

// emitContext ports emit_context: REVIEW_PANEL wins, else .review/panel;
// the member list lands in $dir/panel. Compact mode skips the advisory
// notices. Sets panelSrc/panelList on the run context.
func emitContext(ctx *fetchRunCtx) *core.RefusalError {
	panelList, panelSrc := resolvePanel(ctx.env)
	ctx.panelSrc, ctx.panelList = panelSrc, panelList
	if ref := emitPanel(ctx, panelList, panelSrc); ref != nil {
		return ref
	}
	return emitLearnings(ctx)
}

// emitPanel ports the panel notice: the pinned list writes and prints,
// else the none-pinned line prints outside compact mode.
func emitPanel(ctx *fetchRunCtx, panelList []string, panelSrc string) *core.RefusalError {
	if len(panelList) > 0 {
		var sb strings.Builder
		for _, p := range panelList {
			sb.WriteString(p + newline)
		}
		if werr := atomic.WriteFile(ctx.dir+"/panel", []byte(sb.String())); werr != nil {
			return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write %s/panel: %v", ctx.dir, werr)}
		}
		fmt.Fprintf(ctx.runner.Human, "» panel (%d from %s)\n", len(panelList), panelSrc)
	} else if ctx.runner.Effective.Compact != strTrue {
		fmt.Fprintln(ctx.runner.Human, "» panel: none pinned (set REVIEW_PANEL or .review/panel for comparable, cost-capped runs)")
	}
	return nil
}

// emitLearnings ports the learnings notice: the line count prints outside
// compact mode when the file reads.
func emitLearnings(ctx *fetchRunCtx) *core.RefusalError {
	if ctx.runner.Effective.Compact != strTrue {
		if fi, err := os.Stat(".review/learnings.md"); err == nil && !fi.IsDir() {
			// `wc -l` counts newline bytes, not lines.
			raw, rerr := os.ReadFile(".review/learnings.md")
			if rerr != nil {
				return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not read .review/learnings.md: %v", rerr)}
			}
			fmt.Fprintf(ctx.runner.Human, "» learnings: .review/learnings.md (%d lines — read headings first, not the full file)\n",
				countNewlines(string(raw)))
		}
	}
	return nil
}

// resolvePanel ports the REVIEW_PANEL / .review/panel precedence: env wins
// (comma/space split), else the file (comments and blanks skipped).
func resolvePanel(env map[string]string) ([]string, string) {
	if v, ok := env["REVIEW_PANEL"]; ok && v != "" {
		return splitPanelList(v), "REVIEW_PANEL"
	}
	raw, err := os.ReadFile(".review/panel")
	if err != nil {
		return nil, ""
	}
	return panelFileLines(string(raw))
}

// panelFileLines ports the panel file scan: comments and blanks skipped,
// empty reading absent.
func panelFileLines(raw string) ([]string, string) {
	var out []string
	for _, line := range splitLines(raw) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		return nil, ""
	}
	return out, ".review/panel"
}

// splitPanelList ports `tr ', ' '\n'` plus trim and blank-drop.
func splitPanelList(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(c rune) bool { return c == ',' || c == ' ' }) {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ensureStore ports ensure_store: backfill the store marker when absent.
func ensureStore(dir string) error {
	if _, err := os.Stat(dir + "/store.json"); err == nil {
		return nil
	}
	return atomic.WriteFile(dir+"/store.json", []byte("{\n  \"schema_version\": \"1\"\n}\n"))
}

// role patterns, mirroring roles_for_paths.
var (
	roleTestRe       = regexp.MustCompile(`(^|/)(test|tests|__tests__|e2e|spec)(s)?/`)
	roleTestBaseRe   = regexp.MustCompile(`^(test_|spec_)`)
	roleTestExtRe    = regexp.MustCompile(`[._](test|spec)\.`)
	roleDocsBaseRe   = regexp.MustCompile(`\.md$|\.txt$|^LICENSE`)
	roleConfigBaseRe = regexp.MustCompile(`^(package\.json|tsconfig.*|Dockerfile.*|Makefile|CMakeLists\.txt|pom\.xml|` +
		`pyproject\.toml|go\.mod|go\.sum|Cargo\.toml|Gemfile)`)
	roleConfigRcRe  = regexp.MustCompile(`^\..*rc`)
	roleConfigExtRe = regexp.MustCompile(`\.(yml|yaml|toml)$`)
)

// pathRole ports the roles_for_paths role function.
func pathRole(path string) string {
	base := path
	if _, after, ok := strings.CutLast(path, pathSep); ok {
		base = after
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
		return keySource
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

// rolesForPaths ports roles_for_paths: grouped triage roles over the
// newline-separated path list. The shell pipelines through `sort` (byte
// order under LC_ALL=C); roles sort by group then path via the same key.
// roleRow is one sortable path with its heuristic role.
type roleRow struct {
	role string
	path string
}

func rolesForPaths(paths []string) string {
	rows := make([]roleRow, len(paths))
	for i, p := range paths {
		rows[i] = roleRow{role: pathRole(p), path: p}
	}
	slices.SortFunc(rows, compareRoleRows)
	w := &roleWriter{first: true}
	for _, r := range rows {
		w.visit(r)
	}
	return w.sb.String()
}

// compareRoleRows orders rows by role, then path.
func compareRoleRows(a, b roleRow) int {
	if a.role != b.role {
		return strings.Compare(a.role, b.role)
	}
	return strings.Compare(a.path, b.path)
}

// roleWriter carries the grouped roles rendering state.
type roleWriter struct {
	prev  string
	sb    strings.Builder
	first bool
}

// visit emits one row, opening a new role section on change.
func (w *roleWriter) visit(r roleRow) {
	if r.role != w.prev {
		if !w.first {
			w.sb.WriteString(newline)
		}
		w.sb.WriteString("## " + r.role + newline)
		w.prev = r.role
		w.first = false
	}
	w.sb.WriteString("- " + r.path + newline)
}

// rolesForPathsText ports the `printf '%s\n' "$text" | roles_for_paths`
// pipeline: the text plus its trailing newline feeds the sorter, so an
// empty text still feeds one empty line (the degenerate "## source" block).
func rolesForPathsText(text string) string {
	return rolesForPaths(strings.Split(text, newline))
}

// rolesTextEmpty ports the `jq -r '.files[].path' | roles_for_paths`
// pipeline: zero rows feed zero lines, so an empty file list renders the
// empty roles file.
func rolesTextEmpty(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	return rolesForPaths(paths)
}

// splitLines ports the shell line iteration (awk/grep/sed), yielding one
// entry per input line.
// newline, dropping only the final empty field of a newline-terminated
// input. Carriage returns stay (unlike [strings.Lines]), and an empty input
// yields no lines.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, newline)
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// countNewlines ports `wc -l`: the number of newline bytes.
func countNewlines(s string) int {
	return strings.Count(s, newline)
}

// fetchRunCtx carries the shared fetch run state across the bundle writers.
type fetchRunCtx struct {
	runner    *session
	env       map[string]string
	repo      string
	pr        string
	dir       string
	root      string
	panelSrc  string
	panelList []string
}
