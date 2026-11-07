package fetchcore

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/hunks"
	"github.com/stainless-code/verdict/internal/identity"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

func buildHunksTSV(fullDiff, out string) error {
	raw, err := os.ReadFile(fullDiff)
	if err != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgReadFailed, fullDiff, err)}
	}
	if werr := atomic.WriteFile(out, []byte(hunks.Snapshot(string(raw)))); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write %s: %v", out, werr)}
	}
	return nil
}

func diffFiles(fullDiff, dir string) ([]string, error) {
	raw, err := os.ReadFile(fullDiff)
	if err != nil {
		return nil, &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgReadFailed, fullDiff, err)}
	}
	files := diffHunkFiles(string(raw))
	slices.Sort(files)
	// Identical contents dedupe after the byte-order sort.
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

// diffHunkFiles owns the header/hunk scan: the paths whose section carries
// a hunk line, in diff order.
func diffHunkFiles(raw string) []string {
	sections := hunks.Files(raw)
	var files []string
	for i := range sections {
		if sections[i].Path != "" && sections[i].HunkMarks > 0 {
			files = append(files, sections[i].Path)
		}
	}
	return files
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

// toJSONArray turns newline-separated input into a JSON array
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
	fmt.Fprintf(ctx.runner.Human, "» roles -> %s/roles.txt (heuristic file roles for triage)\n", ctx.runner.display(ctx.dir))
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

// diffHeaderIndex numbers the `diff --git` headers of file, or reports
// headers — empty diff?)"`: numbered header lines or the fallback sentence.
func diffHeaderIndex(fullDiff string) string {
	raw, err := os.ReadFile(fullDiff)
	if err != nil {
		return noDiffHeaders
	}
	sections := hunks.Files(string(raw))
	var sb strings.Builder
	for i := range sections {
		sb.WriteString(sections[i].HeaderIndex() + newline)
	}
	if sb.Len() == 0 {
		return noDiffHeaders
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

// omittedPaths owns the omitted computation: PR file-list paths absent
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

// emitContext resolves the review panel from the effective resolution:
// the member list lands in $dir/panel. Compact mode skips the advisory
// notices. Sets panelSrc/panelList on the run context.
func emitContext(ctx *fetchRunCtx) *core.RefusalError {
	panelList, panelSrc := resolvePanel(ctx.runner, ctx.runCfg.resolved)
	ctx.panelSrc, ctx.panelList = panelSrc, panelList
	if ref := emitPanel(ctx, panelList, panelSrc); ref != nil {
		return ref
	}
	return emitLearnings(ctx)
}

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
		fmt.Fprintln(ctx.runner.Human,
			"» panel: none pinned (set REVIEW_PANEL or the typed defaults.panel for comparable, cost-capped runs)")
	}
	return nil
}

// emitLearnings owns the learnings notice: the line count prints outside
// compact mode when the file reads.
func emitLearnings(ctx *fetchRunCtx) *core.RefusalError {
	if ctx.runner.Effective.Compact != strTrue {
		if raw, ok := ctx.runner.readControl(config.LearningsPath); ok {
			// `wc -l` counts newline bytes, not lines.
			fmt.Fprintf(ctx.runner.Human, "» learnings: %s (%d lines — read headings first, not the full file)\n",
				ctx.runner.controls.Path(config.LearningsPath), countNewlines(string(raw)))
		}
	}
	return nil
}

// resolvePanel reads the panel from the effective resolution: flags beat
// environment, which beats repository scopes and shared defaults, which
// beat personal review. The origin source names the winning layer.
func resolvePanel(_ *session, res *config.Resolved) ([]string, string) {
	if res == nil {
		return nil, ""
	}
	list := res.Defaults.Values.Panel
	if len(list) == 0 {
		return nil, ""
	}
	return list, res.Defaults.Origins["panel"].Source
}

// ensureStore backfills the store marker when absent.
func ensureStore(dir string) error {
	if _, err := os.Stat(dir + "/store.json"); err == nil {
		return nil
	}
	return atomic.WriteFile(dir+"/store.json", []byte("{\n  \"schema_version\": \"1\"\n}\n"))
}

// roleRow is one sortable path with its heuristic role.
type roleRow struct {
	role string
	path string
}

func rolesForPaths(paths []string) string {
	rows := make([]roleRow, len(paths))
	for i, p := range paths {
		rows[i] = roleRow{role: readcommon.PathRole(p), path: p}
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

func rolesForPathsText(text string) string {
	return rolesForPaths(strings.Split(text, newline))
}

func rolesTextEmpty(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	return rolesForPaths(paths)
}

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

// noDiffHeaders is the toc header-index fallback for a diff with no headers.
const noDiffHeaders = "(no diff headers — empty diff?)\n"

// countNewlines counts the newline bytes.
func countNewlines(s string) int {
	return strings.Count(s, newline)
}

// writeIdentityFile builds the bundle patch identity over the present diff
// bytes and writes identity.json beside it: the filtered hash covers
// full.diff, while the identity input prefers full.raw.diff when an
// exclusion pass left one (so the patch id always tracks the raw
// pre-exclusion diff even though both hashes coincide today). It returns
// the built identity so provenance writers stamp the exact bytes-bound
// record. A write failure refuses fetch-failed: the identity is part of
// the bundle contract, not an advisory receipt.
func writeIdentityFile(ctx context.Context, cmdr Commander, dir, head, base string) (*identity.Identity, error) {
	filtered, err := os.ReadFile(dir + suffixFullDiff)
	if err != nil {
		return nil, &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgReadFailed, dir+suffixFullDiff, err)}
	}
	// Only a genuinely absent raw artifact falls back to the filtered
	// bytes: a present-but-unreadable raw diff refuses instead of
	// certifying the wrong identity input.
	raw, err := identity.ReadRawDiff(dir, filtered)
	if err != nil {
		return nil, &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgReadFailed, dir+suffixRawDiff, err)}
	}
	id := identity.Build(ctx, cmdr, filtered, raw, head, base)
	if err := atomic.WriteFile(dir+suffixIdentity, []byte(jq.PrettyOrdered(id.Wire()))); err != nil {
		return nil, &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write %s/identity.json: %v", dir, err)}
	}
	return id, nil
}

// fetchRunCtx carries the shared fetch run state across the bundle writers.
type fetchRunCtx struct {
	runner    *session
	env       map[string]string
	runCfg    *runConfig
	repo      string
	pr        string
	dir       string
	root      string
	panelSrc  string
	panelList []string
}
