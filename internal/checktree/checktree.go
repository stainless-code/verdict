// Package checktree ports the `check-tree` verb: the machine clean-tree
// gate for address/fix-ci/build prompts. No network; the skill's own
// .review/ scratch never trips the gate.
package checktree

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Request wires a check-tree run.
type Request = readcommon.Request

// Porcelain layout constants: `git status --porcelain` v1 lines read two
// XY status columns, one space, then the path.
const (
	porcelainSpaceAt = 2      // separator between the XY columns and the path
	porcelainPathAt  = 3      // path starts after the XY columns and the space
	porcelainMinLen  = 4      // XY columns + space + at least one path byte
	renameSep        = " -> " // rename lines match on the post-arrow path
	newline          = "\n"
)

// verbCheckTree names the verb in envelopes.
const verbCheckTree = "check-tree"

// keyClean names the envelope clean flag.
const keyClean = "clean"

// errGitStatus marks a failed `git status --porcelain` probe. The error is
// control flow only (runCheck maps any probe error to the not-a-repo
// refusal), sealed per the layer-boundary contract.
func gitProbeFailed(err error) error {
	return jq.Sealed("git status --porcelain", err)
}

// Run executes `check-tree [--expect <prefix,...>] [--json]`, returning the
// exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, _ Request) int {
	expect, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{Code: "canceled", Message: "canceled before check-tree completed"}, cli.ExitBroke)
	}
	if err := readcommon.ValidateOverrides(); err != nil {
		return r.Refuse(err)
	}
	return runCheck(ctx, r, expect)
}

// parseFlags parses the check-tree flags.
func parseFlags(r *cli.Runner, args []string) (string, int, bool) {
	expect := ""
	i := 0
	for i < len(args) {
		a := args[i]
		next := ""
		if i+1 < len(args) {
			next = args[i+1]
		}
		switch a {
		case "--expect":
			expect = next
			i += 2
		case "--json":
			r.JSONMode = true
			routeHuman(r)
			i++
		default:
			return "", r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
				"unknown flag '%s' for check-tree (want --expect <prefix,...>, --json)", a)}), true
		}
	}
	return expect, 0, false
}

// routeHuman sends human progress to stderr in JSON mode.
func routeHuman(r *cli.Runner) {
	if r.JSONMode {
		r.Human = r.Diag
	} else {
		r.Human = r.Out
	}
}

// runCheck ports the check-tree gate over `git status --porcelain`.
func runCheck(ctx context.Context, r *cli.Runner, expect string) int {
	out, err := gitPorcelain(ctx)
	if err != nil {
		return notARepo(r)
	}
	own, porcelain := splitScratch(out)
	scratch := ""
	if own > 0 {
		scratch = fmt.Sprintf(" (%d .review/ scratch line(s) ignored)", own)
	}
	if porcelain == "" {
		return cleanTree(r, scratch, own)
	}
	if expect == "" {
		return dirtyTree(r, porcelain)
	}
	return expectGate(r, porcelain, expect)
}

// gitPorcelain runs git status --porcelain, discarding stderr exactly like
// the shell's 2>/dev/null (a nil Stderr connects the null device).
func gitPorcelain(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	var stdout strings.Builder
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", gitProbeFailed(err)
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
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

// envelope ports the check-tree --json envelope shell.
func envelope(r *cli.Runner, next string, data *jq.Obj, errObj any) *jq.Obj {
	return jq.NewObj(
		"schema_version", "1",
		"verb", verbCheckTree,
		"repo", "",
		"pr", nil,
		"at", cli.NowUTC(r.Now()),
		"next", next,
		"warnings", cliStrings(r.Warnings),
		"data", data,
		"error", errObj,
	)
}

// notARepo ports the not-a-git-repo refusal.
func notARepo(r *cli.Runner) int {
	fmt.Fprintln(r.Diag, "check-tree: not a git repo")
	if !r.JSONMode {
		return 1
	}
	errObj := jq.NewObj("code", "not-a-repo",
		"message", "check-tree needs a git repository (git status failed)")
	env := envelope(r, "run inside a git repository",
		jq.NewObj(keyClean, false, "files", []any{}), errObj)
	fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	return 1
}

// cleanTree ports the clean path.
func cleanTree(r *cli.Runner, scratch string, own int) int {
	msg := "check-tree: clean" + scratch
	fmt.Fprintln(r.Human, msg)
	if !r.JSONMode {
		return 0
	}
	env := envelope(r, msg, jq.NewObj(keyClean, true, "ignored_review_lines", own), nil)
	fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	return 0
}

// dirtyTree ports the dirty path without an --expect allowlist.
func dirtyTree(r *cli.Runner, porcelain string) int {
	msg := "check-tree: dirty worktree — commit or stash first:"
	fmt.Fprintln(r.Human, msg)
	fmt.Fprintln(r.Human, porcelain)
	if !r.JSONMode {
		return 1
	}
	env := envelope(r, msg, jq.NewObj(keyClean, false, "files", porcelainLines(porcelain)), nil)
	fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	return 1
}

// porcelainLines ports the files value: non-empty porcelain lines.
func porcelainLines(porcelain string) []any {
	var out []any
	for _, line := range porcelainLinesRaw(porcelain) {
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

// expectGate ports the --expect allowlist: stray paths fail, covered dirt
// passes.
func expectGate(r *cli.Runner, porcelain, expect string) int {
	if stray := unexpectedPaths(porcelain, splitPrefixes(expect)); len(stray) > 0 {
		return unexpectedDirt(r, stray)
	}
	msg := "check-tree: only expected files dirty"
	fmt.Fprintln(r.Human, msg)
	if !r.JSONMode {
		return 0
	}
	env := envelope(r, msg, jq.NewObj(keyClean, true, "expected_only", true), nil)
	fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	return 0
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
func unexpectedDirt(r *cli.Runner, stray []string) int {
	msg := "check-tree: unexpected dirt:"
	fmt.Fprintln(r.Human, msg)
	for _, line := range stray {
		fmt.Fprintln(r.Human, line)
	}
	if !r.JSONMode {
		return 1
	}
	env := envelope(r, msg, jq.NewObj(keyClean, false, "unexpected", strayLines(stray)), nil)
	fmt.Fprint(r.Out, jq.PrettyOrdered(env))
	return 1
}

// strayLines ports the unexpected value: non-empty stray lines.
func strayLines(stray []string) []any {
	var out []any
	for _, line := range stray {
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

// cliStrings converts warnings to JSON string values.
func cliStrings(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}
