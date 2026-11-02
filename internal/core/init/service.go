// Package initcore is the init verb's domain service: the guided offline
// scaffolder that writes the standard local control files (.review/budget,
// panel, kb-policy, kb.jsonl starter) with style presets, then runs check-tree
// validation. Existing files print a preview and need --yes/--force (or, in
// human mode, an interactive confirm) before overwrite — the never-prompts
// doctrine holds: machine mode never prompts, even on a TTY. internal/scaffold
// owns argv parsing, stdin, envelope rendering, and exit mapping.
package initcore

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/bundle"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
)

// Scaffold refusal codes and paths.
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

// Request carries the resolved init invocation: the shared runtime (env,
// clock, progress, commander), the preset and confirm inputs, the
// adapter-routed streams, and the stdin answer seam.
type Request struct { //nolint:govet // the transport contract keeps its field order
	core.ReadRequest
	Preset      string
	Yes         bool
	Interactive bool
	Human       io.Writer
	Diag        io.Writer
	ReadAnswer  func() string
}

// Response carries the completed scaffold: the envelope inputs and the typed
// data profile.
type Response struct {
	At       string
	Next     string
	Data     *Data
	Warnings []string
	ExitCode int
}

// Data is the init envelope's data profile.
type Data struct {
	Preset         string
	BudgetUSD      string
	KBMinJudged    jq.Number
	KBMaxFPRate    jq.Number
	Files          []string
	CheckTreeClean bool
}

// Wire renders the profile in published wire order.
func (d *Data) Wire() *jq.Obj {
	return jq.NewObj(
		"preset", d.Preset,
		"budget_usd", d.BudgetUSD,
		"kb_min_judged", d.KBMinJudged,
		"kb_max_fp_rate", d.KBMaxFPRate,
		"files", fileValues(d.Files),
		"check_tree_clean", d.CheckTreeClean,
	)
}

// fileValues converts status lines to a JSON array value the ordered printer
// renders one entry per line (a []string would fall through to compact JSON).
func fileValues(files []string) []any {
	out := make([]any, 0, len(files))
	for _, f := range files {
		out = append(out, f)
	}
	return out
}

// state carries one init run: the request, the accumulated warnings, the
// envelope timestamp, and the completed summary.
type state struct { //nolint:govet // init service state is allocated once per invocation
	req      *Request
	warnings []string
	at       string
	next     string
	data     *Data
	exit     int
}

// Run executes `init --preset <chill|standard|strict> [--yes]`.
func Run(ctx context.Context, req *Request) (*Response, *core.RefusalError) {
	st := &state{req: req}
	if err := ctx.Err(); err != nil {
		return nil, st.refuse("canceled", "canceled before init completed")
	}
	if ref := st.runInit(ctx); ref != nil {
		return nil, ref
	}
	return st.response(), nil
}

// warn records and streams one warning.
func (st *state) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	st.req.Warn(msg)
}

// refuse builds a typed refusal carrying the accumulated warnings.
func (st *state) refuse(code, msg string) *core.RefusalError {
	return &core.RefusalError{Code: code, Message: msg, Warnings: st.warnings}
}

// humanf writes one rendered human line to the adapter stream.
func (st *state) humanf(format string, args ...any) {
	if st.req.Human != nil {
		fmt.Fprintf(st.req.Human, format, args...)
	}
}

// diagf writes one diagnostic fragment to the adapter stream.
func (st *state) diagf(format string, args ...any) {
	if st.req.Diag != nil {
		fmt.Fprintf(st.req.Diag, format, args...)
	}
}

// response assembles the typed response after the scaffold.
func (st *state) response() *Response {
	return &Response{
		At:       st.at,
		Next:     st.next,
		Data:     st.data,
		Warnings: st.warnings,
		ExitCode: st.exit,
	}
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
func (st *state) runInit(ctx context.Context) *core.RefusalError {
	vals, ref := st.resolvePreset()
	if ref != nil {
		return ref
	}
	wants := presetFiles(st.req.Preset, vals)
	if err := os.MkdirAll(reviewDot, scaffoldDirPerm); err != nil {
		return st.refuse(codeInitFailed,
			fmt.Sprintf("init failed: init could not create .review (storage writable?): %v", err))
	}
	statuses, needOverwrite := collectStatuses(wants)
	st.previewScaffold(wants)
	if code, done, ref := st.writeWithConfirm(wants, needOverwrite); ref != nil {
		return ref
	} else if done {
		st.exit = code
		return nil
	}
	if ref := validateScaffold(); ref != nil {
		return ref
	}
	treeClean := st.reportTree(ctx)
	st.reportNext(vals, statuses, treeClean)
	return nil
}

// writeWithConfirm runs the overwrite gate and writes controls.
func (st *state) writeWithConfirm(wants map[string]string, needOverwrite bool) (int, bool, *core.RefusalError) {
	if needOverwrite && !st.req.Yes {
		code, ref := st.confirmOverwrite()
		if ref != nil {
			return 0, true, ref
		}
		if code != 0 {
			return code, true, nil
		}
	}
	if ref := st.writeControls(wants); ref != nil {
		return 0, true, ref
	}
	return 0, false, nil
}

// resolvePreset maps a preset name to values, refusing unknown names.
func (st *state) resolvePreset() (presetVals, *core.RefusalError) {
	vals, ok := presets[st.req.Preset]
	if !ok {
		return presetVals{}, st.refuse(codeBadPreset,
			fmt.Sprintf("unknown --preset '%s' (want chill|standard|strict)", st.req.Preset))
	}
	return vals, nil
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
func (st *state) previewScaffold(wants map[string]string) {
	st.humanf("init: preset %s (chill: unpinned/10/0.7, standard: "+
		"1 member/5/0.5, strict: lead+2 lenses/3/0.3)\n", st.req.Preset)
	for _, t := range targets {
		st.humanf("--- .review/%s (%s) ---\n", t, targetStatus(t, wants[t]))
		st.humanf("%s\n", wants[t])
	}
}

// confirmOverwrite prompts on a human TTY or refuses in machine mode.
func (st *state) confirmOverwrite() (int, *core.RefusalError) {
	if !st.req.Interactive {
		return 0, st.refuse(codeNeedsConfirm,
			"init refuses to overwrite existing control files without "+
				"confirm (re-run with --yes after reviewing the preview above)")
	}
	st.diagf("Proceed? [y/N] ")
	if !isYes(st.readAnswer()) {
		st.diagf("init: aborted (re-run with --yes to overwrite " +
			"after reviewing the preview)\n")
		return 1, nil
	}
	return 0, nil
}

// readAnswer reads one stdin line through the adapter seam.
func (st *state) readAnswer() string {
	if st.req.ReadAnswer == nil {
		return ""
	}
	return st.req.ReadAnswer()
}

// isYes matches the shell's accepted confirmation words.
func isYes(ans string) bool {
	switch ans {
	case "y", "Y", "yes", "YES":
		return true
	default:
		return false
	}
}

// writeControls writes every control file, keeping up-to-date ones.
func (st *state) writeControls(wants map[string]string) *core.RefusalError {
	for _, t := range targets {
		if isCurrent(t, wants[t]) {
			st.humanf("keep: .review/%s (already up to date)\n", t)
			continue
		}
		if err := atomic.WriteFile(reviewDir+t, []byte(wants[t]+newlineStr)); err != nil {
			return st.cannotWriteControl(t)
		}
		if !bundle.IsFile(reviewDir + t) {
			return st.cannotWriteControl(t)
		}
		st.humanf("wrote: .review/%s\n", t)
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
func (st *state) cannotWriteControl(t string) *core.RefusalError {
	return st.refuse(codeInitFailed,
		fmt.Sprintf("init could not write .review/%s (storage writable?)", t))
}

// reportTree runs check-tree and prints its outcome, returning cleanliness.
func (st *state) reportTree(ctx context.Context) bool {
	treeOut, treeClean, rest := st.checkTree(ctx)
	st.humanf("%s\n", treeOut)
	if !treeClean && rest != "" {
		st.humanf("%s\n", rest)
	}
	if !treeClean {
		st.warn("init wrote the control files but the worktree carries non-.review " +
			"dirt (see check-tree above)")
	}
	return treeClean
}

// reportNext prints the next steps and records the typed envelope data.
func (st *state) reportNext(vals presetVals, statuses []string, treeClean bool) {
	st.humanf("next: replace panel placeholders with real slugs, " +
		"tune .review/budget, then fetch\n")
	st.at = st.req.TimeUTC()
	st.next = "replace panel placeholders with real slugs, tune .review/budget, then fetch"
	st.data = &Data{
		Preset:         st.req.Preset,
		BudgetUSD:      vals.budget,
		KBMinJudged:    jq.Number(vals.kbMin),
		KBMaxFPRate:    jq.Number(vals.kbRate),
		Files:          statuses,
		CheckTreeClean: treeClean,
	}
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
func validateScaffold() *core.RefusalError {
	if ref := checkBudget(); ref != nil {
		return ref
	}
	if ref := checkScaffoldFiles(); ref != nil {
		return ref
	}
	return checkKB()
}

// checkBudget validates the scaffolded budget file.
func checkBudget() *core.RefusalError {
	raw, err := os.ReadFile(reviewDir + "budget")
	if err != nil {
		return &core.RefusalError{
			Code:    codeBadBudget,
			Message: "scaffolded .review/budget '' is not a plain USD number",
		}
	}
	line := firstUsableLine(string(raw))
	if !budgetRe.MatchString(line) {
		return &core.RefusalError{
			Code: codeBadBudget,
			Message: fmt.Sprintf(
				"scaffolded .review/budget '%s' is not a plain USD number", line),
		}
	}
	return nil
}

// checkScaffoldFiles validates the scaffolded panel and kb-policy files.
func checkScaffoldFiles() *core.RefusalError {
	if !bundle.IsFile(reviewDir + "panel") {
		return &core.RefusalError{Code: codeInitFailed, Message: "scaffolded .review/panel missing after write"}
	}
	if !bundle.IsFile(reviewDir + "kb-policy") {
		return &core.RefusalError{
			Code:    codeInitFailed,
			Message: "scaffolded .review/kb-policy missing after write",
		}
	}
	return nil
}

// checkKB validates the scaffolded kb.jsonl file.
func checkKB() *core.RefusalError {
	kbRaw, err := os.ReadFile(reviewDir + "kb.jsonl")
	if err != nil {
		return &core.RefusalError{
			Code:    codeBadSettings,
			Message: "scaffolded .review/kb.jsonl is not valid JSONL",
		}
	}
	vals, err := jq.DecodeSlurp(strings.NewReader(string(kbRaw)))
	if err != nil {
		return &core.RefusalError{
			Code:    codeBadSettings,
			Message: "scaffolded .review/kb.jsonl is not valid JSONL",
		}
	}
	if countObjects(vals) == 0 {
		return &core.RefusalError{
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
func (st *state) checkTree(ctx context.Context) (out string, clean bool, rest string) {
	raw, err := st.gitStatus(ctx)
	if err != nil {
		return "check-tree: not a git repo", false, ""
	}
	lines := strings.Split(raw, newlineStr)
	lines = trimTrailingBlanks(lines)
	restLines := dirtLines(lines)
	if len(restLines) == 0 {
		own := len(lines) - len(restLines)
		return fmt.Sprintf("check-tree: clean (%d .review/ scratch line(s) ignored)", own), true, ""
	}
	return "check-tree: dirty worktree — commit or stash first:", false, strings.Join(restLines, newlineStr)
}

// gitStatus runs `git status --porcelain` through the commander seam.
func (st *state) gitStatus(ctx context.Context) (string, error) {
	if st.req.Commander == nil {
		return "", errNoCommander
	}
	res, err := st.req.Commander.Run(ctx, core.Command{Argv: []string{"git", "status", "--porcelain"}})
	if err != nil {
		//nolint:wrapcheck // the commander error is only a non-zero signal; check-tree reads "not a git repo"
		return "", err
	}
	return res.Stdout, nil
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

// budgetRe matches a plain USD number.
var budgetRe = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

// ownRe matches own .review/ scratch lines (any 2-char status + space).
var ownRe = regexp.MustCompile(`^.. \.review/`)

// errNoCommander guards direct service calls that did not inject a commander.
var errNoCommander = scaffoldError("no commander injected")

// scaffoldError is a plain error value for seam failures.
type scaffoldError string

// Error implements error.
func (e scaffoldError) Error() string { return string(e) }
