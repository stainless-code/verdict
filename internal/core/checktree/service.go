// Package checktreecore is the check-tree verb's domain service: the
// machine clean-tree gate for address/fix-ci/build prompts. No network;
// the skill's own .review/ scratch never trips the gate. The git probe
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
// keys render (Wire branches on Kind), mirroring the shell's per-path
// objects.
type Data struct { //nolint:govet // field order is the published property order
	Kind    string
	Clean   bool
	Ignored int
	Lines   []string
}

// Outcome kinds, one per gate path.
const (
	kindClean      = "clean"
	kindDirty      = "dirty"
	kindExpected   = "expected"
	kindUnexpected = "unexpected"
	kindNotARepo   = "not-a-repo"
)

// keyClean names the envelope clean flag.
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

// stringLines ports the files/unexpected values: non-empty lines, never
// null.
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

// state carries one check-tree run: the request plus the accumulated
// warnings.
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

// human streams one human output line.
func (st *state) human(line string) {
	if st.req.Runtime != nil {
		st.req.Human(line)
	}
}

// diag streams one raw diagnostic line.
func (st *state) diag(line string) {
	if st.req.Runtime != nil {
		st.req.Diag(line)
	}
}

// loadRefusal recovers the typed refusal carried by an overrides-guard
// error; the fallback is defensive and never fires in practice.
func (st *state) loadRefusal(err error) *core.RefusalError {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		ref.Warnings = st.warnings
		return ref
	}
	return &core.RefusalError{Code: "internal", Message: err.Error(), Warnings: st.warnings}
}

// commander returns the injected commander (nil when the caller set none).
func (st *state) commander() core.Commander {
	var commander core.Commander
	if rt := st.runtime(); rt != nil {
		commander = rt.Commander
	}
	return commander
}

// runtime returns the injected runtime (nil when the caller set none).
func (st *state) runtime() *core.Runtime {
	return st.req.Runtime
}

// response assembles the typed response after the gate resolves.
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

// Porcelain layout constants: `git status --porcelain` v1 lines read two
// XY status columns, one space, then the path.
const (
	porcelainSpaceAt = 2      // separator between the XY columns and the path
	porcelainPathAt  = 3      // path starts after the XY columns and the space
	porcelainMinLen  = 4      // XY columns + space + at least one path byte
	renameSep        = " -> " // rename lines match on the post-arrow path
	newline          = "\n"
)

// runCheck ports the check-tree gate over `git status --porcelain`.
func (st *state) runCheck(ctx context.Context) *Response {
	out, ok := st.gitPorcelain(ctx)
	if !ok {
		return st.notARepo()
	}
	own, porcelain := splitScratch(out)
	scratch := ""
	if own > 0 {
		scratch = fmt.Sprintf(" (%d .review/ scratch line(s) ignored)", own)
	}
	if porcelain == "" {
		return st.cleanTree(scratch, own)
	}
	if st.req.Expect == "" {
		return st.dirtyTree(porcelain)
	}
	return st.expectGate(porcelain, st.req.Expect)
}

// gitPorcelain runs git status --porcelain through the injected commander,
// discarding stderr exactly like the shell's 2>/dev/null.
func (st *state) gitPorcelain(ctx context.Context) (string, bool) {
	cmdr := st.commander()
	if cmdr == nil {
		return "", false
	}
	res, err := cmdr.Run(ctx, core.Command{Argv: []string{"git", "status", "--porcelain"}})
	if err != nil || res.ExitCode != 0 {
		return "", false
	}
	return strings.TrimRight(res.Stdout, "\n"), true
}

// splitScratch counts and removes the skill's own .review/ scratch lines:
// two status columns, a space, then .review/.
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

// porcelainLinesRaw splits porcelain output into lines.
func porcelainLinesRaw(porcelain string) []string {
	return slices.Collect(strings.SplitSeq(porcelain, newline))
}

// isScratchLine ports ^.. \.review/: any two status columns, a space,
// then .review/.
func isScratchLine(line string) bool {
	return len(line) >= porcelainMinLen && line[porcelainSpaceAt] == ' ' &&
		strings.HasPrefix(line[porcelainPathAt:], ".review/")
}

// notARepo ports the not-a-git-repo refusal.
func (st *state) notARepo() *Response {
	st.diag("check-tree: not a git repo")
	return st.response("run inside a git repository",
		&Data{Kind: kindNotARepo},
		&core.SchemaError{Code: "not-a-repo", Message: "check-tree needs a git repository (git status failed)"},
		1)
}

// cleanTree ports the clean path.
func (st *state) cleanTree(scratch string, own int) *Response {
	msg := "check-tree: clean" + scratch
	st.human(msg)
	return st.response(msg, &Data{Kind: kindClean, Clean: true, Ignored: own}, nil, 0)
}

// dirtyTree ports the dirty path without an --expect allowlist.
func (st *state) dirtyTree(porcelain string) *Response {
	msg := "check-tree: dirty worktree — commit or stash first:"
	st.human(msg)
	st.human(porcelain)
	return st.response(msg, &Data{Kind: kindDirty, Lines: porcelainLinesRaw(porcelain)}, nil, 1)
}

// expectGate ports the --expect allowlist: stray paths fail, covered dirt
// passes.
func (st *state) expectGate(porcelain, expect string) *Response {
	if stray := unexpectedPaths(porcelain, splitPrefixes(expect)); len(stray) > 0 {
		return st.unexpectedDirt(stray)
	}
	msg := "check-tree: only expected files dirty"
	st.human(msg)
	return st.response(msg, &Data{Kind: kindExpected, Clean: true}, nil, 0)
}

// unexpectedPaths ports the stray scan: rename lines match on the
// post-arrow path.
func unexpectedPaths(porcelain string, prefixes []string) []string {
	var stray []string
	for _, line := range porcelainLinesRaw(porcelain) {
		if !matchesPrefix(renameTarget(stripStatus(line)), prefixes) {
			stray = append(stray, line)
		}
	}
	return stray
}

// stripStatus drops the XY status columns, reading "" on short lines.
func stripStatus(line string) string {
	if len(line) >= porcelainPathAt {
		return line[porcelainPathAt:]
	}
	return ""
}

// renameTarget ports ${f##* -> }: the post-arrow path on renames.
func renameTarget(file string) string {
	if _, after, ok := strings.CutLast(file, renameSep); ok {
		return after
	}
	return file
}

// splitPrefixes splits and trims the --expect prefix list.
func splitPrefixes(expect string) []string {
	var out []string
	for p := range strings.SplitSeq(expect, ",") {
		out = append(out, strings.Trim(p, " \t\r\n\v\f"))
	}
	return out
}

// matchesPrefix ports [[ "$f" == "$p"* ]]: an empty prefix matches
// everything.
func matchesPrefix(file string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(file, p) {
			return true
		}
	}
	return false
}

// unexpectedDirt ports the unexpected-dirt failure.
func (st *state) unexpectedDirt(stray []string) *Response {
	msg := "check-tree: unexpected dirt:"
	st.human(msg)
	for _, line := range stray {
		st.human(line)
	}
	return st.response(msg, &Data{Kind: kindUnexpected, Lines: stray}, nil, 1)
}
