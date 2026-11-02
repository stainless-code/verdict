// Package scaffold ports the `init` verb: the guided offline scaffolder
// that writes the standard local control files (.review/budget, panel,
// kb-policy, kb.jsonl starter) with style presets, then runs check-tree
// validation. Existing files print a preview and need --yes/--force (or, in
// human mode, an interactive confirm) before overwrite — the never-prompts
// doctrine holds: machine mode never prompts, even on a TTY.
package scaffold

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/bundle"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
)

// Scaffold refusal codes and paths (revive add-constant, goconst).
const (
	codeInitFailed   = "init-failed"
	codeBadSettings  = "bad-settings"
	codeBadBudget    = "bad-budget"
	codeBadPreset    = "bad-preset"
	codeNeedsConfirm = "needs-confirm"
	reviewDir        = ".review/"
	reviewDot        = ".review"
	newlineStr       = "\n"
	scaffoldDirPerm  = 0o777
)

// Request carries the resolved init invocation (init takes no positionals).
type Request struct {
	Env map[string]string
}

// Run executes `init --preset <chill|standard|strict> [--yes] [--json]`,
// returning the process exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, _ Request) int {
	r.Repo, r.Pr = "", ""
	flags, code, done := parseFlags(r, args)
	if done {
		return code
	}
	r.Rentry = "verdict init --preset " + flags.preset
	if flags.yes {
		r.Rentry += " --yes"
	}
	if r.JSONMode {
		r.Rentry += " --json"
	}
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{
			Code:    "canceled",
			Message: "canceled before init completed",
		}, cli.ExitBroke)
	}
	code, err := runInit(ctx, r, flags)
	if err != nil {
		if ref, _ := errors.AsType[*cli.RefusalError](err); ref != nil {
			return r.Refuse(ref)
		}
		return r.Refuse(&cli.RefusalError{
			Code:    codeInitFailed,
			Message: fmt.Sprintf("init failed: %v", err),
		})
	}
	return code
}

// flags carries the parsed init options.
type flags struct {
	preset string
	yes    bool
}

// parseFlags strips init's flags, reporting unknown ones.
func parseFlags(r *cli.Runner, args []string) (flags, int, bool) {
	f := flags{preset: "standard"}
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--preset":
			f.preset = presetValue(args, &i)
		case strings.HasPrefix(a, "--preset="):
			f.preset = strings.TrimPrefix(a, "--preset=")
		case a == "--yes" || a == "-y" || a == "--force":
			f.yes = true
		case a == "--json":
		default:
			msg := fmt.Sprintf(
				"unknown flag '%s' for init (want --preset <chill|standard|strict>, "+
					"--yes, --force, --json)",
				a,
			)
			return f, r.ReportUsage(&cli.UsageError{Message: msg}), true
		}
		i++
	}
	return f, 0, false
}

// presetValue reads the --preset operand, advancing i past it.
func presetValue(args []string, i *int) string {
	if *i+1 < len(args) {
		*i++
		return args[*i]
	}
	return ""
}

// presetVals carries the preset mapping.
type presetVals struct {
	budget string
	kbMin  string
	kbRate string
	panel  string
}

// presets maps preset names to their file values.
var presets = map[string]presetVals{
	"chill":    {budget: "2.00", kbMin: "10", kbRate: "0.7", panel: ""},
	"standard": {budget: "0.50", kbMin: "5", kbRate: "0.5", panel: "main"},
	"strict":   {budget: "0.10", kbMin: "3", kbRate: "0.3", panel: "lead\nlens-1\nlens-2"},
}

// targets lists the scaffolded control files in order.
var targets = []string{"budget", "panel", "kb-policy", "kb.jsonl"}

// runInit mirrors the init block: preset mapping, preview, confirm gate,
// writes, validation, check-tree, next.
func runInit(ctx context.Context, r *cli.Runner, f flags) (int, error) {
	vals, err := resolvePreset(f.preset)
	if err != nil {
		return 0, err
	}
	wants := presetFiles(f.preset, vals)
	if err := os.MkdirAll(reviewDot, scaffoldDirPerm); err != nil {
		return 0, cannotCreateReview(err)
	}
	statuses, needOverwrite := collectStatuses(wants)
	previewScaffold(r, f, wants)
	if code, done, err := writeWithConfirm(r, f, wants, needOverwrite); err != nil {
		return 0, err
	} else if done {
		return code, nil
	}
	if err := validateScaffold(); err != nil {
		return 0, err
	}
	treeClean := reportTree(ctx, r)
	reportNext(r, f, vals, statuses, treeClean)
	return 0, nil
}

// writeWithConfirm runs the overwrite gate and writes controls.
func writeWithConfirm(
	r *cli.Runner,
	f flags,
	wants map[string]string,
	needOverwrite bool,
) (int, bool, error) {
	if needOverwrite && !f.yes {
		code, err := confirmOverwrite(r)
		if err != nil {
			return 0, true, err
		}
		if code != 0 {
			return code, true, nil
		}
	}
	if err := writeControls(r, wants); err != nil {
		return 0, true, err
	}
	return 0, false, nil
}

// resolvePreset maps a preset name to values, refusing unknown names.
func resolvePreset(name string) (presetVals, error) {
	vals, ok := presets[name]
	if !ok {
		return presetVals{}, &cli.RefusalError{
			Code:    codeBadPreset,
			Message: fmt.Sprintf("unknown --preset '%s' (want chill|standard|strict)", name),
		}
	}
	return vals, nil
}

// cannotCreateReview builds the .review creation refusal.
func cannotCreateReview(err error) error {
	//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
	return fmt.Errorf("init could not create .review (storage writable?): %v", err)
}

// collectStatuses classifies each control file and reports overwrites.
func collectStatuses(wants map[string]string) ([]string, bool) {
	statuses := make([]string, 0, len(targets))
	needOverwrite := false
	for _, t := range targets {
		st := targetStatus(t, wants[t])
		statuses = append(statuses, t+":"+st)
		if st == "overwrite" {
			needOverwrite = true
		}
	}
	return statuses, needOverwrite
}

// previewScaffold prints the preset header and per-file previews.
func previewScaffold(r *cli.Runner, f flags, wants map[string]string) {
	fmt.Fprintf(r.Human, "init: preset %s (chill: unpinned/10/0.7, standard: "+
		"1 member/5/0.5, strict: lead+2 lenses/3/0.3)\n", f.preset)
	for _, t := range targets {
		fmt.Fprintf(r.Human, "--- .review/%s (%s) ---\n", t, targetStatus(t, wants[t]))
		fmt.Fprintf(r.Human, "%s\n", wants[t])
	}
}

// confirmOverwrite prompts on a human TTY or refuses in machine mode.
func confirmOverwrite(r *cli.Runner) (int, error) {
	if r.JSONMode || !cli.IsTerminal(os.Stdin) {
		return 0, &cli.RefusalError{
			Code: codeNeedsConfirm,
			Message: "init refuses to overwrite existing control files without " +
				"confirm (re-run with --yes after reviewing the preview above)",
		}
	}
	fmt.Fprintf(r.Diag, "Proceed? [y/N] ")
	switch readAnswer() {
	case "y", "Y", "yes", "YES":
		return 0, nil
	default:
		fmt.Fprintf(r.Diag, "init: aborted (re-run with --yes to overwrite "+
			"after reviewing the preview)\n")
		return 1, nil
	}
}

// writeControls writes every control file, keeping up-to-date ones.
func writeControls(r *cli.Runner, wants map[string]string) error {
	for _, t := range targets {
		if isCurrent(t, wants[t]) {
			fmt.Fprintf(r.Human, "keep: .review/%s (already up to date)\n", t)
			continue
		}
		if err := atomic.WriteFile(reviewDir+t, []byte(wants[t]+newlineStr)); err != nil {
			return cannotWriteControl(t)
		}
		if !bundle.IsFile(reviewDir + t) {
			return cannotWriteControl(t)
		}
		fmt.Fprintf(r.Human, "wrote: .review/%s\n", t)
	}
	return nil
}

// isCurrent reports whether the control file already holds want.
func isCurrent(t, want string) bool {
	cur, err := os.ReadFile(reviewDir + t)
	if err != nil {
		return false
	}
	return string(cur) == want+newlineStr
}

// cannotWriteControl builds the control-file write refusal.
func cannotWriteControl(t string) error {
	return &cli.RefusalError{
		Code:    codeInitFailed,
		Message: fmt.Sprintf("init could not write .review/%s (storage writable?)", t),
	}
}

// reportTree runs check-tree and prints its outcome, returning cleanliness.
func reportTree(ctx context.Context, r *cli.Runner) bool {
	treeOut, treeClean, rest := checkTree(ctx)
	fmt.Fprintf(r.Human, "%s\n", treeOut)
	if !treeClean && rest != "" {
		fmt.Fprintf(r.Human, "%s\n", rest)
	}
	if !treeClean {
		r.Warn("init wrote the control files but the worktree carries non-.review " +
			"dirt (see check-tree above)")
	}
	return treeClean
}

// reportNext prints the next steps and the JSON envelope in JSON mode.
func reportNext(r *cli.Runner, f flags, vals presetVals, statuses []string, treeClean bool) {
	fmt.Fprintf(r.Human, "next: replace panel placeholders with real slugs, "+
		"tune .review/budget, then fetch\n")
	if !r.JSONMode {
		return
	}
	files := make([]any, 0, len(statuses))
	for _, s := range statuses {
		files = append(files, s)
	}
	env := jq.NewObj(
		"schema_version", "1",
		"verb", "init",
		"repo", "",
		"pr", nil,
		"at", cli.NowUTC(r.Now()),
		"next", "replace panel placeholders with real slugs, tune .review/budget, then fetch",
		"warnings", cliStrings(r.Warnings),
		"data", jq.NewObj(
			"preset", f.preset,
			"budget_usd", vals.budget,
			"kb_min_judged", jq.Number(vals.kbMin),
			"kb_max_fp_rate", jq.Number(vals.kbRate),
			"files", files,
			"check_tree_clean", treeClean,
		),
		"error", nil,
	)
	fmt.Fprintln(r.Out, env.MarshalPretty())
}

// presetFiles renders the four control-file bodies (no trailing newline;
// the write and preview append exactly one, matching `printf '%s\n'`).
func presetFiles(preset string, vals presetVals) map[string]string {
	budget := "# Verdict spend budget (USD) — written by `verdict init --preset " + preset + "`.\n" +
		"# Env REVIEW_BUDGET_USD wins over this file. Edit or delete to change.\n" + vals.budget
	panel := presetPanel(preset, vals.panel)
	kbpolicy := "# Verdict KB retirement policy — written by `verdict init --preset " + preset + "`.\n" +
		"# Env REVIEW_KB_MIN_JUDGED / REVIEW_KB_MAX_FP_RATE win over this file.\n" +
		"MIN_JUDGED=" + vals.kbMin + "\nMAX_FP_RATE=" + vals.kbRate
	return map[string]string{
		"budget":    budget,
		"panel":     panel,
		"kb-policy": kbpolicy,
		"kb.jsonl":  kbStarter(),
	}
}

// presetPanel renders the panel body for a preset.
func presetPanel(preset, body string) string {
	if body == "" {
		return "# Verdict panel — written by `verdict init --preset " + preset + "`" +
			" (chill: no pinning).\n" +
			"# One model slug per line, first synthesizes. REVIEW_PANEL wins over this file.\n" +
			"# Replace these comments with real slugs to pin a formation; empty (all comments)\n" +
			"# means the operator picks per run."
	}
	return "# Verdict panel — written by `verdict init --preset " + preset + "`.\n" +
		"# One model slug per line, first synthesizes. REVIEW_PANEL wins over this file.\n" +
		"# Replace these placeholders with real slugs; same panel every run " +
		"keeps verdicts comparable.\n" + body
}

// kbStarter returns the two-row starter kb.jsonl body (no trailing newline).
func kbStarter() string {
	first := `{"id":"check-error-paths",` +
		`"rule":"Check error paths on every awaited call before approving",` +
		`"scope":"src/*.ts,src/*.js","provenance":"starter",` +
		`"rigor":"pointed","hits":0,"false_hits":0,"judged":0,"status":"active"}`
	second := `{"id":"verify-before-claim",` +
		`"rule":"Verify every addressed claim with the repo's own verify command ` +
		`before marking addressed",` +
		`"scope":"*","provenance":"starter",` +
		`"rigor":"walked","hits":0,"false_hits":0,"judged":0,"status":"active"}`
	return first + newlineStr + second
}

// targetStatus classifies one control file: create, keep, or overwrite.
func targetStatus(t, want string) string {
	cur, err := os.ReadFile(reviewDir + t)
	if err != nil {
		return "create"
	}
	if string(cur) == want+newlineStr {
		return "keep"
	}
	return "overwrite"
}

// validateScaffold ports the scaffolded-file validation.
func validateScaffold() error {
	if err := checkBudget(); err != nil {
		return err
	}
	if err := checkScaffoldFiles(); err != nil {
		return err
	}
	return checkKB()
}

// checkBudget validates the scaffolded budget file.
func checkBudget() error {
	raw, err := os.ReadFile(reviewDir + "budget")
	if err != nil {
		return &cli.RefusalError{
			Code:    codeBadBudget,
			Message: "scaffolded .review/budget '' is not a plain USD number",
		}
	}
	line := firstUsableLine(string(raw))
	if !budgetRe.MatchString(line) {
		return &cli.RefusalError{
			Code: codeBadBudget,
			Message: fmt.Sprintf(
				"scaffolded .review/budget '%s' is not a plain USD number",
				line,
			),
		}
	}
	return nil
}

// checkScaffoldFiles validates the scaffolded panel and kb-policy files.
func checkScaffoldFiles() error {
	if !bundle.IsFile(reviewDir + "panel") {
		return &cli.RefusalError{Code: codeInitFailed, Message: "scaffolded .review/panel missing after write"}
	}
	if !bundle.IsFile(reviewDir + "kb-policy") {
		return &cli.RefusalError{
			Code:    codeInitFailed,
			Message: "scaffolded .review/kb-policy missing after write",
		}
	}
	return nil
}

// checkKB validates the scaffolded kb.jsonl file.
func checkKB() error {
	kbRaw, err := os.ReadFile(reviewDir + "kb.jsonl")
	if err != nil {
		return &cli.RefusalError{
			Code:    codeBadSettings,
			Message: "scaffolded .review/kb.jsonl is not valid JSONL",
		}
	}
	vals, err := jq.DecodeSlurp(strings.NewReader(string(kbRaw)))
	if err != nil {
		return &cli.RefusalError{
			Code:    codeBadSettings,
			Message: "scaffolded .review/kb.jsonl is not valid JSONL",
		}
	}
	if countObjects(vals) == 0 {
		return &cli.RefusalError{
			Code:    codeBadSettings,
			Message: "scaffolded .review/kb.jsonl carries no rule rows",
		}
	}
	return nil
}

// countObjects counts map rows in decoded JSONL values.
func countObjects(vals []any) int {
	objects := 0
	for _, v := range vals {
		if _, ok := v.(map[string]any); ok {
			objects++
		}
	}
	return objects
}

// firstUsableLine ports the budget-line read: first non-comment non-blank
// line, trimmed, with one leading $ stripped.
func firstUsableLine(content string) string {
	for ln := range strings.SplitSeq(content, newlineStr) {
		trimmed := strings.Trim(ln, " \t\n\v\f\r")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		return strings.TrimPrefix(trimmed, "$")
	}
	return ""
}

// checkTree ports the init check-tree validation: own .review/ scratch
// ignored, everything else is dirt. The git failure path reads "not a git
// repo".
func checkTree(ctx context.Context) (out string, clean bool, rest string) {
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	raw, err := cmd.Output()
	if err != nil {
		return "check-tree: not a git repo", false, ""
	}
	lines := strings.Split(string(raw), newlineStr)
	lines = trimTrailingBlanks(lines)
	restLines := dirtLines(lines)
	if len(restLines) == 0 {
		own := len(lines) - len(restLines)
		return fmt.Sprintf("check-tree: clean (%d .review/ scratch line(s) ignored)", own), true, ""
	}
	return "check-tree: dirty worktree — commit or stash first:", false, strings.Join(restLines, newlineStr)
}

// trimTrailingBlanks drops trailing empty lines from git status output.
func trimTrailingBlanks(lines []string) []string {
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// dirtLines returns status lines outside the owned .review/ scratch.
func dirtLines(lines []string) []string {
	var restLines []string
	for _, ln := range lines {
		if ownRe.MatchString(ln) {
			continue
		}
		restLines = append(restLines, ln)
	}
	return restLines
}

// readAnswer reads one stdin line for the human confirm (errors read empty,
// matching `read -r ans || ans=""`, and only the trailing newline is cut).
func readAnswer() string {
	rd := bufio.NewReader(os.Stdin)
	line, err := rd.ReadString('\n')
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(line, newlineStr)
}

// budgetRe matches a plain USD number.
var budgetRe = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

// ownRe matches own .review/ scratch lines (any 2-char status + space).
var ownRe = regexp.MustCompile(`^.. \.review/`)

// cliStrings converts warnings to JSON string values.
func cliStrings(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}
