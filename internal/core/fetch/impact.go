package fetchcore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	indexcore "github.com/stainless-code/verdict/internal/core/index"
	"github.com/stainless-code/verdict/internal/fence"
	"github.com/stainless-code/verdict/internal/jq"
)

// The impact hand-off's literals: the artifact name, the section cap, and the
// row/coverage heads the section renders.
const (
	suffixImpact = "/impact.json"
	// impactSectionCap bounds the appended section: toc.txt is model input, and
	// a blast radius over a wide diff would otherwise spend the review budget
	// on rows. The cap counts bytes of the whole "## impact" section.
	impactSectionCap = 8192
	// impactCapKiB is the same cap in the unit the truncation line names.
	impactCapKiB     = impactSectionCap / 1024
	impactHeadSymbol = "symbols:\n"
	impactHeadCaller = "callers:\n"
	impactHeadTest   = "tests:\n"
	impactNoDiffWarn = "» impact: no %s in %s, so %s is unanswered"
)

// impactInput carries one hand-off's inputs: the index the operator named and
// the bundle head it should describe.
type impactInput struct {
	index string
	head  string
}

// writeImpact writes the index hand-off a bundle asked for: impact.json plus a
// capped "## impact" section on toc.txt. Without --index nothing is computed
// and an earlier run's artifact is removed, so a bundle never carries an
// impact section for an index this fetch did not name.
func writeImpact(ctx context.Context, ctx2 *fetchRunCtx, in impactInput) error {
	if in.index == "" {
		return discardImpact(ctx2)
	}
	raw, found, err := impactDiff(ctx2)
	if !found || err != nil {
		return err
	}
	impact, ref := indexcore.ImpactOf(ctx, &indexcore.ImpactRequest{
		ReadRequest: ctx2.readRequest(), Index: in.index, Diff: string(raw), Head: in.head,
	})
	if ref != nil {
		//nolint:wrapcheck // the typed refusal crosses packages intact; the edge renders Code/Message
		return ref
	}
	for _, w := range impact.Warnings {
		ctx2.runner.Warn(w)
	}
	if werr := writeImpactFiles(ctx2, impact); werr != nil {
		return werr
	}
	impactLine(ctx2, impact)
	return nil
}

// discardImpact removes an earlier run's artifact when this fetch names no
// index, so a section and a file always appear and disappear together.
func discardImpact(ctx2 *fetchRunCtx) error {
	if err := os.Remove(ctx2.dir + suffixImpact); err != nil && !errors.Is(err, os.ErrNotExist) {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not remove %s: %v", ctx2.dir+suffixImpact, err)}
	}
	return nil
}

// impactDiff reads the bundle's full diff. A fetch that wrote none leaves the
// hand-off unanswered with one warning rather than a refusal: the bundle is
// still reviewable, and the section is what is missing.
func impactDiff(ctx2 *fetchRunCtx) (raw []byte, found bool, err error) {
	raw, err = os.ReadFile(ctx2.dir + suffixFullDiff)
	if errors.Is(err, os.ErrNotExist) {
		ctx2.runner.Warn(fmt.Sprintf(impactNoDiffWarn, fullDiffFile, ctx2.runner.display(ctx2.dir), suffixImpact))
		return nil, false, nil
	}
	if err != nil {
		return nil, false, &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf(msgReadFailed, ctx2.dir+suffixFullDiff, err)}
	}
	return raw, true, nil
}

// writeImpactFiles writes the artifact and appends the section, in that order:
// a bundle with a section always carries the file the section came from.
func writeImpactFiles(ctx2 *fetchRunCtx, impact *indexcore.Impact) error {
	if werr := atomic.WriteFile(ctx2.dir+suffixImpact, []byte(jq.PrettyOrdered(impactJSON(impact)))); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write %s/impact.json: %v", ctx2.dir, werr)}
	}
	return appendImpactSection(ctx2.dir, impactSection(impact))
}

// impactLine names the hand-off on the human surface.
func impactLine(ctx2 *fetchRunCtx, impact *indexcore.Impact) {
	fmt.Fprintf(ctx2.runner.Human, "» impact: %d symbol(s), %d caller(s), %d test(s) from %s -> %s/impact.json\n",
		len(impact.Data.Blast.Symbols), len(impact.Data.Blast.Callers), len(impact.Data.Blast.Tests),
		impact.Index, ctx2.runner.display(ctx2.dir))
}

// readRequest builds the index hand-off's read request: the run's command seam
// and environment, anchored on the reviewed repository root, never the tool
// checkout.
func (c *fetchRunCtx) readRequest() core.ReadRequest {
	root := c.runner.repoRoot
	if root == "" {
		root = c.root
	}
	return core.ReadRequest{
		Runtime:  &core.Runtime{Commander: c.runner.commander, Env: c.env},
		RepoRoot: root,
	}
}

// impactJSON renders impact.json: the provenance row, the tree the rows came
// from, the hand-off input, and the measured lists with their truncation
// counts. A list the index could not answer reads null, never 0.
func impactJSON(impact *indexcore.Impact) *jq.Obj {
	b := impact.Data.Blast
	indexed := impact.Data.Wire()
	o := jq.NewObj(keySchema, "1", "sources", []any{impact.Index}, "tree", impact.Data.Tree,
		"commit", impact.Data.Commit, "stale", impact.Data.Stale,
		"coverage", jq.NewObj("files_indexed", impact.Data.Files, "skipped", impact.Data.Skipped),
		"input", jq.NewObj("head", impactNullString(b.Input.Head), "paths", impactStringValues(b.Input.Paths)),
		"limit", impact.Data.Limit)
	for _, key := range []string{"symbols", "callers", "tests"} {
		value, _ := indexed.Get(key)
		o.Set(key, value)
	}
	_, receipt := impactSectionReceipt(impact)
	return receipt.Apply(o)
}

// impactSection renders the capped "## impact" section: the heads and counts,
// then the rows, fitted to [impactSectionCap] with the omitted line count.
func impactSection(impact *indexcore.Impact) string {
	section, _ := impactSectionReceipt(impact)
	return section
}

func impactSectionReceipt(impact *indexcore.Impact) (string, *core.Truncation) {
	b := impact.Data.Blast
	lines := []string{
		"## impact\n",
		fmt.Sprintf("index: %s (tree %s, commit %s)\n", impact.Index, impact.Data.Tree, impactNullDash(impact.Data.Commit)),
		fmt.Sprintf("coverage: %d file(s) indexed, %d skipped | changed files: %d\n",
			impact.Data.Files, impact.Data.Skipped, len(b.Input.Paths)),
		fmt.Sprintf("symbols: %s | callers: %s | tests: %s\n",
			impactCount(b.Counts.ChangedSymbols, b.Truncated.ChangedSymbols),
			impactCount(b.Counts.Callers, b.Truncated.Callers),
			impactCount(b.Counts.Tests, b.Truncated.Tests)),
	}
	rows := impactRowLines(b)
	kept, hidden := fitImpactRows(rows, len(strings.Join(lines, "")))
	lines = append(lines, kept...)
	if hidden > 0 {
		lines = append(lines, fmt.Sprintf("truncated: %d line(s) omitted to fit the %d KiB impact cap\n", hidden, impactCapKiB))
	}
	var receipt *core.Truncation
	if hidden > 0 {
		total := len(b.Symbols) + len(b.Callers) + len(b.Tests)
		receipt = core.ResultCut("response_cap", total-hidden, new(total), nil)
	}
	return strings.Join(lines, ""), receipt
}

// impactRowLines renders every row line, in list order, with each section head
// carrying its own list so a truncated tail still names what it began.
func impactRowLines(b *indexcore.Blast) []string {
	var out []string
	if len(b.Symbols) > 0 {
		out = append(out, impactHeadSymbol)
	}
	for _, r := range b.Symbols {
		out = append(out, fmt.Sprintf("- %s:%d %s (%s)\n", impactSafe(r.Path), r.Line, impactSafe(r.Name), impactSafe(r.Kind)))
	}
	if len(b.Callers) > 0 {
		out = append(out, impactHeadCaller)
	}
	for _, r := range b.Callers {
		out = append(out, fmt.Sprintf("- %s:%d %s (%s, depth %d)\n",
			impactSafe(r.Path), r.Line, impactSafe(r.Symbol), impactSafe(r.Resolution), r.Depth))
	}
	if len(b.Tests) > 0 {
		out = append(out, impactHeadTest)
	}
	for _, r := range b.Tests {
		out = append(out, fmt.Sprintf("- %s (%s, depth %d)\n", impactSafe(r.Path), impactSafe(r.Via), r.Depth))
	}
	return out
}

func fitImpactRows(rows []string, head int) ([]string, int) {
	groups := groupImpactRows(rows)
	reserve := len(fmt.Sprintf("truncated: %d line(s) omitted to fit the %d KiB impact cap\n", len(rows), impactCapKiB))
	budget := impactSectionCap - head - reserve
	kept := make([]int, len(groups))
	for _, group := range groups {
		budget -= len(group[0]) + len(fmt.Sprintf("omitted: %d row(s)\n", len(group)-1))
	}
	allocateImpactRows(groups, kept, budget)
	var out []string
	hidden := 0
	for i, group := range groups {
		out = append(out, group[:kept[i]+1]...)
		omitted := len(group) - 1 - kept[i]
		hidden += omitted
		if omitted > 0 {
			out = append(out, fmt.Sprintf("omitted: %d row(s)\n", omitted))
		}
	}
	return out, hidden
}

func groupImpactRows(rows []string) [][]string {
	var groups [][]string
	for _, row := range rows {
		if row == impactHeadSymbol || row == impactHeadCaller || row == impactHeadTest {
			groups = append(groups, []string{row})
		} else {
			groups[len(groups)-1] = append(groups[len(groups)-1], row)
		}
	}
	return groups
}

func allocateImpactRows(groups [][]string, kept []int, budget int) {
	for progress := true; progress; {
		progress = false
		for i, group := range groups {
			next := kept[i] + 1
			if next < len(group) && len(group[next]) <= budget {
				budget -= len(group[next])
				kept[i]++
				progress = true
			}
		}
	}
}

// impactCount renders one list's measured count with its hidden rows.
func impactCount(count, truncated *int) string {
	if count == nil {
		return "null"
	}
	out := strconv.Itoa(*count)
	if truncated != nil && *truncated > 0 {
		out += fmt.Sprintf(" (%d not shown by --limit)", *truncated)
	}
	return out
}

// impactSafe renders one attacker-influenced path or symbol so it cannot forge
// a fence or a row: control characters become their backslash escapes, then
// the shared fence rule breaks any run of "<" that could spell a marker.
func impactSafe(s string) string {
	if !strings.ContainsAny(s, "\n\r\t") {
		return fence.Safe(s)
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			sb.WriteRune(r)
		}
	}
	return fence.Safe(sb.String())
}

// appendImpactSection appends the section to the bundle's toc.txt.
func appendImpactSection(dir, section string) error {
	path := dir + "/toc.txt"
	raw := readOptional(path)
	body := raw
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += newline
	}
	body += newline + section
	if werr := atomic.WriteFile(path, []byte(body)); werr != nil {
		return &core.RefusalError{Code: CodeFetchFailed, Message: fmt.Sprintf("could not write %s/toc.txt: %v", dir, werr)}
	}
	return nil
}

// impactPresent reports whether a bundle carries the hand-off artifact.
func impactPresent(dir string) bool {
	_, err := os.Stat(dir + suffixImpact)
	return err == nil
}

// impactArtifactRow adds the hand-off to a bundle's artifact map when present.
func impactArtifactRow(obj *jq.Obj, dir string) {
	if impactPresent(dir) {
		obj.Set("impact.json", dir+suffixImpact)
	}
}

// impactNullString renders an absent string as null.
func impactNullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// impactStringValues renders one path list as a JSON array (never null).
func impactStringValues(v []string) []any {
	out := make([]any, 0, len(v))
	for _, s := range v {
		out = append(out, s)
	}
	return out
}

// impactNullDash renders an absent value as a dash on the human section.
func impactNullDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
