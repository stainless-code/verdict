// Package checktreecore is the check-tree verb's domain service: the
// machine clean-tree gate for address/fix-ci/build prompts. No network;
// the skill's own .verdict/ scratch never trips the gate. The git probe
// goes through the injected commander. internal/checktree owns argv
// parsing, progress routing, envelope rendering, and exit mapping.
package checktreecore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request carries the resolved check-tree invocation: the shared read
// runtime plus the --expect allowlist.
type Request struct {
	core.ReadRequest
	Expect   string
	Warnings []string
}

// Response carries the completed gate: the envelope fields, the typed
// data profile, the machine refusal for the not-a-repo path, and the
// process exit code. Human lines stream live through the runtime progress
// observer, so the response carries no rendered lines.
type Response struct { //nolint:govet // field grouping documents the response surface
	At       string
	Next     string
	Warnings []string
	Data     *Data
	Error    *core.SchemaError
	Exit     int
}

// Data is the check-tree verb's --json data profile. Only the outcome's
// keys render (Wire branches on Kind), one entry per path.
// objects.
type Data struct { //nolint:govet // field order is the published property order
	Kind    string
	Clean   bool
	Ignored int
	Lines   []string
}

const (
	kindClean      = "clean"
	kindDirty      = "dirty"
	kindExpected   = "expected"
	kindUnexpected = "unexpected"
	kindNotARepo   = "not-a-repo"
)

const keyClean = "clean"

// Wire renders the profile in published wire order.
func (d *Data) Wire() *jq.Obj {
	switch d.Kind {
	case kindClean:
		return jq.NewObj(keyClean, true, "ignored_review_lines", d.Ignored)
	case kindDirty:
		return jq.NewObj(keyClean, false, "files", stringLines(d.Lines))
	case kindExpected:
		return jq.NewObj(keyClean, true, "expected_only", true)
	case kindUnexpected:
		return jq.NewObj(keyClean, false, "unexpected", stringLines(d.Lines))
	default:
		return jq.NewObj(keyClean, false, "files", []any{})
	}
}

func stringLines(lines []string) []any {
	var out []any
	for _, line := range lines {
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	if out == nil {
		out = []any{}
	}
	return out
}

type state struct {
	req      *Request
	warnings []string
}

// Run executes the check-tree gate over `git status --porcelain`.
func Run(ctx context.Context, req *Request) (*Response, *core.RefusalError) {
	st := &state{req: req, warnings: append([]string{}, req.Warnings...)}
	if err := ctx.Err(); err != nil {
		return nil, &core.RefusalError{Code: "canceled", Message: "canceled before check-tree completed"}
	}
	if err := readcommon.ValidateOverrides(); err != nil {
		return nil, st.loadRefusal(err)
	}
	return st.runCheck(ctx), nil
}

func (st *state) human(line string) {
	if st.req.Runtime != nil {
		st.req.Human(line)
	}
}

func (st *state) diag(line string) {
	if st.req.Runtime != nil {
		st.req.Diag(line)
	}
}

func (st *state) loadRefusal(err error) *core.RefusalError {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		ref.Warnings = st.warnings
		return ref
	}
	return &core.RefusalError{Code: "internal", Message: err.Error(), Warnings: st.warnings}
}

func (st *state) commander() core.Commander {
	var commander core.Commander
	if rt := st.runtime(); rt != nil {
		commander = rt.Commander
	}
	return commander
}

func (st *state) runtime() *core.Runtime {
	return st.req.Runtime
}

func (st *state) response(next string, data *Data, errObj *core.SchemaError, exit int) *Response {
	return &Response{
		At:       st.req.TimeUTC(),
		Next:     next,
		Warnings: st.warnings,
		Data:     data,
		Error:    errObj,
		Exit:     exit,
	}
}

const (
	porcelainSpaceAt = 2                    // separator between the XY columns and the path
	porcelainPathAt  = 3                    // path starts after the XY columns and the space
	porcelainMinLen  = 4                    // XY columns + space + at least one path byte
	renameSep        = " -> "               // rename lines match on the post-arrow path
	stateDir         = ".verdict/state/"    // review run scratch
	backtestDir      = ".verdict/backtest/" // mined backtest artifacts
	indexDir         = ".verdict/index/"    // local Go index
	newline          = "\n"
)

func (st *state) runCheck(ctx context.Context) *Response {
	out, ok := st.gitPorcelain(ctx)
	if !ok {
		return st.notARepo()
	}
	own, porcelain := splitScratch(out)
	scratch := ""
	if own > 0 {
		scratch = fmt.Sprintf(" (%d .verdict/ scratch line(s) ignored)", own)
	}
	if porcelain == "" {
		return st.cleanTree(scratch, own)
	}
	if st.req.Expect == "" {
		return st.dirtyTree(porcelain)
	}
	return st.expectGate(porcelain, st.req.Expect)
}

func (st *state) gitPorcelain(ctx context.Context) (string, bool) {
	cmdr := st.commander()
	if cmdr == nil {
		return "", false
	}
	argv := []string{"git"}
	if root := st.req.RepoRootPath(); root != "" {
		argv = append(argv, "-C", root)
	}
	argv = append(argv, "status", "--porcelain", "--untracked-files=all")
	res, err := cmdr.Run(ctx, &core.Command{Argv: argv})
	if err != nil || res.ExitCode != 0 {
		return "", false
	}
	return strings.TrimRight(res.Stdout, "\n"), true
}

func splitScratch(porcelain string) (int, string) {
	if porcelain == "" {
		return 0, ""
	}
	own := 0
	var kept []string
	for _, line := range porcelainLinesRaw(porcelain) {
		if isScratchLine(line) {
			own++
			continue
		}
		kept = append(kept, line)
	}
	return own, strings.Join(kept, newline)
}

func porcelainLinesRaw(porcelain string) []string {
	return slices.Collect(strings.SplitSeq(porcelain, newline))
}

func isScratchLine(line string) bool {
	if len(line) < porcelainMinLen || line[porcelainSpaceAt] != ' ' {
		return false
	}
	file := stripStatus(line)
	return isScratchPath(file) && isScratchPath(renameTarget(file))
}

// isScratchPath reports a generated artifact directory: review state, mined
// backtest runs, and the local index stay out of the dirt set, both sides of
// a rename.
func isScratchPath(path string) bool {
	return strings.HasPrefix(path, stateDir) || strings.HasPrefix(path, backtestDir) ||
		strings.HasPrefix(path, indexDir)
}

func (st *state) notARepo() *Response {
	st.diag("check-tree: not a git repo")
	return st.response("run inside a git repository",
		&Data{Kind: kindNotARepo},
		&core.SchemaError{Code: "not-a-repo", Message: "check-tree needs a git repository (git status failed)"},
		1)
}

func (st *state) cleanTree(scratch string, own int) *Response {
	msg := "check-tree: clean" + scratch
	st.human(msg)
	return st.response(msg, &Data{Kind: kindClean, Clean: true, Ignored: own}, nil, 0)
}

func (st *state) dirtyTree(porcelain string) *Response {
	msg := "check-tree: dirty worktree — commit or stash first:"
	st.human(msg)
	st.human(porcelain)
	return st.response(msg, &Data{Kind: kindDirty, Lines: porcelainLinesRaw(porcelain)}, nil, 1)
}

func (st *state) expectGate(porcelain, expect string) *Response {
	if stray := unexpectedPaths(porcelain, splitPrefixes(expect)); len(stray) > 0 {
		return st.unexpectedDirt(stray)
	}
	msg := "check-tree: only expected files dirty"
	st.human(msg)
	return st.response(msg, &Data{Kind: kindExpected, Clean: true}, nil, 0)
}

func unexpectedPaths(porcelain string, prefixes []string) []string {
	var stray []string
	for _, line := range porcelainLinesRaw(porcelain) {
		if !matchesPrefix(renameTarget(stripStatus(line)), prefixes) {
			stray = append(stray, line)
		}
	}
	return stray
}

func stripStatus(line string) string {
	if len(line) >= porcelainPathAt {
		return line[porcelainPathAt:]
	}
	return ""
}

func renameTarget(file string) string {
	if _, after, ok := strings.CutLast(file, renameSep); ok {
		return after
	}
	return file
}

func splitPrefixes(expect string) []string {
	var out []string
	for p := range strings.SplitSeq(expect, ",") {
		out = append(out, strings.Trim(p, " \t\r\n\v\f"))
	}
	return out
}

func matchesPrefix(file string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(file, p) {
			return true
		}
	}
	return false
}

func (st *state) unexpectedDirt(stray []string) *Response {
	msg := "check-tree: unexpected dirt:"
	st.human(msg)
	for _, line := range stray {
		st.human(line)
	}
	return st.response(msg, &Data{Kind: kindUnexpected, Lines: stray}, nil, 1)
}
