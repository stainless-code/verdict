// Package initcore is the init verb's domain service: the guided offline
// scaffolder that writes the typed `.verdict/config.json` with style
// presets beside the `.verdict/kb.jsonl` starter and the tracked-shape
// `.verdict/.gitignore`, then runs check-tree validation. Existing files
// print a preview and need --yes/--force (or, in human mode, an interactive
// confirm) before overwrite — the never-prompts doctrine holds: machine mode
// never prompts, even on a TTY. internal/scaffold owns argv parsing, stdin,
// envelope rendering, and exit mapping.
package initcore

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	codeInitFailed   = "init-failed"
	codeBadSettings  = "bad-settings"
	codeBadBudget    = "bad-budget"
	codeBadPreset    = "bad-preset"
	codeNeedsConfirm = "needs-confirm"
	reviewDotName    = ".verdict"
	newlineStr       = "\n"
	scaffoldDirPerm  = 0o777
)

// Request carries the resolved init invocation: the shared runtime (env,
// clock, progress, commander), the preset and confirm inputs, the
// adapter-routed streams, and the stdin answer seam.
type Request struct { //nolint:govet // the transport contract keeps its field order
	core.ReadRequest
	Preset      string `enum:"chill|standard|strict"`
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

func fileValues(files []string) []any {
	out := make([]any, 0, len(files))
	for _, f := range files {
		out = append(out, f)
	}
	return out
}

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

func (st *state) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	st.req.Warn(msg)
}

func (st *state) refuse(code, msg string) *core.RefusalError {
	return &core.RefusalError{Code: code, Message: msg, Warnings: st.warnings}
}

func (st *state) humanf(format string, args ...any) {
	if st.req.Human != nil {
		fmt.Fprintf(st.req.Human, format, args...)
	}
}

func (st *state) diagf(format string, args ...any) {
	if st.req.Diag != nil {
		fmt.Fprintf(st.req.Diag, format, args...)
	}
}

func (st *state) response() *Response {
	return &Response{
		At:       st.at,
		Next:     st.next,
		Data:     st.data,
		Warnings: st.warnings,
		ExitCode: st.exit,
	}
}

type presetVals struct {
	budget string
	kbMin  string
	kbRate string
	panel  string
}

var presets = map[string]presetVals{
	"chill":    {budget: "2.00", kbMin: "10", kbRate: "0.7", panel: ""},
	"standard": {budget: "0.50", kbMin: "5", kbRate: "0.5", panel: "main"},
	"strict":   {budget: "0.10", kbMin: "3", kbRate: "0.3", panel: "lead\nlens-1\nlens-2"},
}

// configName is the typed control file init scaffolds first.
const configName = "config.json"

var targets = []string{configName, "kb.jsonl", gitignoreName}

// reviewDot is the reviewed repository root's state directory: every
// scaffold read, write, preview, and validation anchors here, so a
// subdirectory invocation never forks the configuration.
func (st *state) reviewDot() string {
	return filepath.Join(st.req.RepoRootPath(), reviewDotName)
}

// reviewPath names one scaffold file under the reviewed repository root.
func (st *state) reviewPath(name string) string { return filepath.Join(st.reviewDot(), name) }

func (st *state) runInit(ctx context.Context) *core.RefusalError {
	vals, ref := st.resolvePreset()
	if ref != nil {
		return ref
	}
	wants := presetFiles(vals)
	if err := os.MkdirAll(st.reviewDot(), scaffoldDirPerm); err != nil {
		return st.refuse(codeInitFailed,
			fmt.Sprintf("init failed: init could not create .verdict (storage writable?): %v", err))
	}
	statuses, needOverwrite := st.collectStatuses(wants)
	st.previewScaffold(wants)
	if code, done, ref := st.writeWithConfirm(wants, needOverwrite); ref != nil {
		return ref
	} else if done {
		st.exit = code
		return nil
	}
	if ref := st.validateScaffold(); ref != nil {
		return ref
	}
	treeClean := st.reportTree(ctx)
	st.reportNext(vals, statuses, treeClean)
	return nil
}

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

func (st *state) resolvePreset() (presetVals, *core.RefusalError) {
	vals, ok := presets[st.req.Preset]
	if !ok {
		return presetVals{}, st.refuse(codeBadPreset,
			fmt.Sprintf("unknown --preset '%s' (want chill|standard|strict)", st.req.Preset))
	}
	return vals, nil
}

func (st *state) collectStatuses(wants map[string]string) ([]string, bool) {
	statuses := make([]string, 0, len(targets))
	needOverwrite := false
	for _, t := range targets {
		status := st.targetStatus(t, wants[t])
		statuses = append(statuses, t+":"+status)
		if status == "overwrite" {
			needOverwrite = true
		}
	}
	return statuses, needOverwrite
}

func (st *state) previewScaffold(wants map[string]string) {
	st.humanf("init: preset %s (chill: unpinned/10/0.7, standard: "+
		"1 member/5/0.5, strict: lead+2 lenses/3/0.3)\n", st.req.Preset)
	for _, t := range targets {
		st.humanf("--- .verdict/%s (%s) ---\n", t, st.targetStatus(t, wants[t]))
		st.humanf("%s\n", wants[t])
	}
}

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

func (st *state) readAnswer() string {
	if st.req.ReadAnswer == nil {
		return ""
	}
	return st.req.ReadAnswer()
}

func isYes(ans string) bool {
	switch ans {
	case "y", "Y", "yes", "YES":
		return true
	default:
		return false
	}
}

func (st *state) writeControls(wants map[string]string) *core.RefusalError {
	for _, t := range targets {
		if st.isCurrent(t, wants[t]) {
			st.humanf("keep: .verdict/%s (already up to date)\n", t)
			continue
		}
		if ref := st.writeControl(t, wants[t]); ref != nil {
			return ref
		}
	}
	return nil
}

// writeControl writes one scaffolded control file and confirms it landed.
func (st *state) writeControl(name, body string) *core.RefusalError {
	if name != configName {
		body += newlineStr
	}
	if err := atomic.WriteFile(st.reviewPath(name), []byte(body)); err != nil {
		return st.cannotWriteControl(name)
	}
	if !readcommon.IsFile(st.reviewPath(name)) {
		return st.cannotWriteControl(name)
	}
	st.humanf("wrote: .verdict/%s\n", name)
	return nil
}

func (st *state) isCurrent(t, want string) bool { return st.targetStatus(t, want) == "keep" }

func (st *state) cannotWriteControl(t string) *core.RefusalError {
	return st.refuse(codeInitFailed,
		fmt.Sprintf("init could not write .verdict/%s (storage writable?)", t))
}

func (st *state) reportTree(ctx context.Context) bool {
	treeOut, treeClean, rest := st.checkTree(ctx)
	st.humanf("%s\n", treeOut)
	if !treeClean && rest != "" {
		st.humanf("%s\n", rest)
	}
	if !treeClean {
		st.warn("init completed; the worktree has uncommitted changes (see check-tree above)")
	}
	return treeClean
}

func (st *state) reportNext(vals presetVals, statuses []string, treeClean bool) {
	st.humanf("next: pin real panel slugs in .verdict/config.json, " +
		"tune defaults.budget_usd, then fetch\n")
	st.at = st.req.TimeUTC()
	st.next = "pin real panel slugs in .verdict/config.json, tune defaults.budget_usd, then fetch"
	st.data = &Data{
		Preset:         st.req.Preset,
		BudgetUSD:      vals.budget,
		KBMinJudged:    jq.Number(vals.kbMin),
		KBMaxFPRate:    jq.Number(vals.kbRate),
		Files:          statuses,
		CheckTreeClean: treeClean,
	}
}

// presetFiles renders the scaffolded shared files: the typed configuration
// the preset describes, the knowledge-corpus starter, and the tracked-shape
// ignore that keeps generated state local.
func presetFiles(vals presetVals) map[string]string {
	return map[string]string{
		configName:    presetConfig(vals),
		"kb.jsonl":    kbStarter(),
		gitignoreName: gitignoreBody,
	}
}

// presetConfig renders one preset as the canonical typed configuration.
// Key order follows the published contract; the values are the preset's
// budget, panel, and retirement quorum.
func presetConfig(vals presetVals) string {
	var sb strings.Builder
	sb.WriteString("{\n  \"schema_version\": \"1\",\n  \"defaults\": {\n")
	panel := presetPanel(vals.panel)
	if panel != "" {
		sb.WriteString(panel)
	}
	sb.WriteString("    \"budget_usd\": " + vals.budget + ",\n")
	sb.WriteString("    \"kb_policy\": {\n")
	sb.WriteString("      \"min_judged\": " + vals.kbMin + ",\n")
	sb.WriteString("      \"max_fp_rate\": " + vals.kbRate + "\n")
	sb.WriteString("    }\n  }\n}")
	return sb.String()
}

// presetPanel renders the pinned roster as a JSON array line, or the empty
// string when the preset pins nothing.
func presetPanel(body string) string {
	if body == "" {
		return ""
	}
	slugs := strings.Split(body, "\n")
	quoted := make([]string, 0, len(slugs))
	for _, slug := range slugs {
		quoted = append(quoted, strconv.Quote(slug))
	}
	return "    \"panel\": [" + strings.Join(quoted, ", ") + "],\n"
}

// gitignoreName is the tracked-shape nested ignore file the state dir needs.
const gitignoreName = ".gitignore"

// gitignoreBody is the entire tracked ignore contract: only generated state
// stays local, so the configuration and support files are reviewable.
const gitignoreBody = "/state/"

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

func (st *state) targetStatus(t, want string) string {
	cur, err := os.ReadFile(st.reviewPath(t))
	if err != nil {
		return "create"
	}
	if string(cur) == want+newlineStr || string(cur) == want {
		return "keep"
	}
	return "overwrite"
}

// validateScaffold re-parses the written configuration through the strict
// parser and re-checks the knowledge starter: the scaffold proves its own
// output is a valid typed contract, not merely well-shaped text.
func (st *state) validateScaffold() *core.RefusalError {
	raw, err := os.ReadFile(st.reviewPath(configName))
	if err != nil {
		return &core.RefusalError{Code: codeInitFailed, Message: "scaffolded .verdict/config.json is unreadable after write"}
	}
	cfg, ref := config.Parse(raw)
	if ref != nil {
		return &core.RefusalError{Code: codeBadBudget, Message: "scaffolded .verdict/config.json does not validate: " + ref.Message}
	}
	if cfg.Defaults == nil || cfg.Defaults.BudgetUSD == nil {
		return &core.RefusalError{Code: codeBadBudget, Message: "scaffolded .verdict/config.json carries no defaults.budget_usd"}
	}
	return st.checkKB()
}

func (st *state) checkKB() *core.RefusalError {
	kbRaw, err := os.ReadFile(st.reviewPath("kb.jsonl"))
	if err != nil {
		return &core.RefusalError{
			Code:    codeBadSettings,
			Message: "scaffolded .verdict/kb.jsonl is not valid JSONL",
		}
	}
	vals, err := jq.DecodeSlurp(strings.NewReader(string(kbRaw)))
	if err != nil {
		return &core.RefusalError{
			Code:    codeBadSettings,
			Message: "scaffolded .verdict/kb.jsonl is not valid JSONL",
		}
	}
	if countObjects(vals) == 0 {
		return &core.RefusalError{
			Code:    codeBadSettings,
			Message: "scaffolded .verdict/kb.jsonl carries no rule rows",
		}
	}
	return nil
}

func countObjects(vals []any) int {
	objects := 0
	for _, v := range vals {
		if _, ok := v.(map[string]any); ok {
			objects++
		}
	}
	return objects
}

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
		return fmt.Sprintf("check-tree: clean (%d .verdict/ scratch line(s) ignored)", own), true, ""
	}
	return "check-tree: dirty worktree — commit or stash first:", false, strings.Join(restLines, newlineStr)
}

// gitStatus runs `git status --porcelain` through the commander seam.
func (st *state) gitStatus(ctx context.Context) (string, error) {
	if st.req.Commander == nil {
		return "", errNoCommander
	}
	res, err := st.req.Commander.Run(ctx, &core.Command{
		Argv: []string{"git", "-C", st.req.RepoRootPath(), "status", "--porcelain", "--untracked-files=all"},
	})
	if err != nil {
		//nolint:wrapcheck // the commander error is only a non-zero signal; check-tree reads "not a git repo"
		return "", err
	}
	if res.ExitCode != 0 {
		return "", res.ExitFailure("git") //nolint:wrapcheck // locally constructed exit-status error
	}
	return res.Stdout, nil
}

func trimTrailingBlanks(lines []string) []string {
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func dirtLines(lines []string) []string {
	var restLines []string
	for _, ln := range lines {
		if isOwnScratch(ln) {
			continue
		}
		restLines = append(restLines, ln)
	}
	return restLines
}

// isOwnScratch reports a porcelain line whose content is confined to the run
// state directory; both sides of a rename must remain within state.
func isOwnScratch(line string) bool {
	if !ownRe.MatchString(line) {
		return false
	}
	if _, after, ok := strings.CutLast(line, initRenameSep); ok {
		return ownTargetRe.MatchString(after)
	}
	return true
}

const initRenameSep = " -> "

var ownRe = regexp.MustCompile(`^.. \.verdict/state/`)

var ownTargetRe = regexp.MustCompile(`^\.verdict/state/`)

var errNoCommander = scaffoldError("no commander injected")

type scaffoldError string

func (e scaffoldError) Error() string { return string(e) }
