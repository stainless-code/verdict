// Package initcore is the init verb's domain service: the guided offline
// scaffolder that writes the typed `.verdict/config.json` with style
// presets beside the `.verdict/kb.jsonl` starter and the tracked-shape
// `.verdict/.gitignore`, then runs check-tree validation. With `--ci github`
// or `--ci forgejo` it also writes the every-PR workflow. Existing files print
// a preview and need --yes/--force (or, in human mode, an interactive confirm)
// before overwrite — the never-prompts doctrine holds: machine mode never
// prompts, even on a TTY. internal/scaffold owns argv parsing, stdin, envelope
// rendering, and exit mapping.
package initcore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
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
	codeUnknownAgent = "unknown-agent"
	// CodeNeedsConfirm is the refusal the adapter annotates: the re-enter
	// command it prints must carry the --yes this run did not.
	CodeNeedsConfirm = "needs-confirm"
	reviewDotName    = ".verdict"
	newlineStr       = "\n"
	scaffoldDirPerm  = 0o777
	// The three answers a scaffold path can give: absent, already current, or
	// written by an older run.
	statusCreate    = "create"
	statusKeep      = "keep"
	statusOverwrite = "overwrite"
)

// Request carries the resolved init invocation: the shared runtime (env,
// clock, progress, commander), the preset and confirm inputs, the
// adapter-routed streams, and the stdin answer seam.
type Request struct { //nolint:govet // the transport contract keeps its field order
	core.ReadRequest
	Preset      string `enum:"chill|standard|strict"`
	CI          string `enum:"github|forgejo"`
	Agent       string
	KeySecret   string
	Yes         bool
	Interactive bool
	Print       bool
	// Machine is the --json mode: stdout carries exactly one envelope, so a
	// --print payload goes only in the data slot.
	Machine    bool
	Human      io.Writer
	Diag       io.Writer
	ReadAnswer func() string
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

// Data is the init envelope's data profile. The CI fields are present only on
// a --ci run, so a plain init keeps its envelope byte-identical.
type Data struct {
	Preset         string
	BudgetUSD      string
	KBMinJudged    jq.Number
	KBMaxFPRate    jq.Number
	CI             string
	WorkflowPath   string
	WorkflowStatus string
	Workflow       string
	Files          []string
	CheckTreeClean bool
}

// Wire renders the profile in published wire order.
func (d *Data) Wire() *jq.Obj {
	out := jq.NewObj(
		"preset", d.Preset,
		"budget_usd", d.BudgetUSD,
		"kb_min_judged", d.KBMinJudged,
		"kb_max_fp_rate", d.KBMaxFPRate,
		"files", fileValues(d.Files),
		"check_tree_clean", d.CheckTreeClean,
	)
	if d.CI == "" {
		return out
	}
	out.Set("ci", d.CI)
	out.Set("workflow_path", d.WorkflowPath)
	if d.WorkflowStatus != "" {
		out.Set("workflow_status", d.WorkflowStatus)
	}
	if d.Workflow != "" {
		out.Set("workflow", d.Workflow)
	}
	return out
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
	ci       ciProvider
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
	vals, workflow, ref := st.plan()
	if ref != nil {
		return ref
	}
	if workflow != nil && st.req.Print {
		st.printWorkflow(vals, workflow)
		return nil
	}
	return st.writeScaffold(ctx, vals, workflow)
}

// plan resolves the preset and renders the workflow, or answers the refusal
// that stops the run before anything touches disk.
func (st *state) plan() (presetVals, []byte, *core.RefusalError) {
	vals, ref := st.resolvePreset()
	if ref != nil {
		return presetVals{}, nil, ref
	}
	if st.req.CI == "" {
		return vals, nil, nil
	}
	ci, ok := LookupCI(st.req.CI)
	if !ok {
		return presetVals{}, nil, st.refuse(codeBadSettings,
			fmt.Sprintf("unknown --ci '%s' (want %s)", st.req.CI, CIValues()))
	}
	st.ci = ci
	workflow, ref := st.workflowFor()
	if ref != nil {
		return presetVals{}, nil, ref
	}
	return vals, workflow, nil
}

// writeScaffold previews, confirms, writes the controls and the workflow, then
// reports the tree check and the next step.
func (st *state) writeScaffold(ctx context.Context, vals presetVals, workflow []byte) *core.RefusalError {
	wants := presetFiles(vals)
	wfStatus, ref := st.workflowStatus(workflow)
	if ref != nil {
		return ref
	}
	statuses, needOverwrite := st.collectStatuses(wants)
	st.previewScaffold(wants, workflow, wfStatus)
	code, done, ref := st.writeWithConfirm(wants, needOverwrite || wfStatus == statusOverwrite)
	if ref != nil {
		return ref
	}
	if done {
		st.exit = code
		return nil
	}
	if ref := st.validateScaffold(); ref != nil {
		return ref
	}
	if ref := st.publishWorkflowIfAny(workflow, wfStatus); ref != nil {
		return ref
	}
	treeClean := st.reportTree(ctx)
	st.reportNext(vals, statuses, treeClean, workflow, wfStatus)
	return nil
}

// publishWorkflowIfAny writes the workflow when the run asked for one.
func (st *state) publishWorkflowIfAny(workflow []byte, status string) *core.RefusalError {
	if workflow == nil {
		return nil
	}
	return st.writeWorkflow(workflow, status)
}

// workflowStatus reads the committed workflow's relation to the rendered
// bytes. Every component is pinned below the repository root, so a symlinked
// or non-directory workflow directory, or a symlinked workflow, refuses even
// when a matching file sits behind it — and refuses before .verdict/ is
// created.
func (st *state) workflowStatus(workflow []byte) (string, *core.RefusalError) {
	if workflow == nil {
		return "", nil
	}
	root, err := os.OpenRoot(st.req.RepoRootPath())
	if err != nil {
		return "", st.workflowRefusal()
	}
	defer root.Close()
	return st.confinedWorkflowStatus(root, workflow)
}

// confinedWorkflowStatus compares the workflow below an opened repository root.
func (st *state) confinedWorkflowStatus(root *os.Root, workflow []byte) (string, *core.RefusalError) {
	for _, dir := range st.ci.dirs {
		present, ref := st.workflowDirPresent(root, dir)
		if ref != nil {
			return "", ref
		}
		if !present {
			return statusCreate, nil
		}
	}
	parent, err := readcommon.OpenDirectoryIn(root, st.ci.dirs[len(st.ci.dirs)-1])
	if err != nil {
		return "", st.workflowRefusal()
	}
	defer parent.Close()
	return st.workflowFileStatus(parent, workflow)
}

// workflowDirPresent reports whether dir exists below root as a real directory.
func (st *state) workflowDirPresent(root *os.Root, dir string) (bool, *core.RefusalError) {
	info, err := root.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil || !info.IsDir() {
		return false, st.workflowRefusal()
	}
	return true, nil
}

// workflowFileStatus compares the workflow inside an already-pinned workflow
// directory. A path that is not a regular file is refused rather than followed.
func (st *state) workflowFileStatus(parent *os.Root, workflow []byte) (string, *core.RefusalError) {
	info, err := parent.Lstat(st.ci.base)
	if errors.Is(err, fs.ErrNotExist) {
		return statusCreate, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return "", st.refuse(codeInitFailed,
			"init will not write "+st.ci.path()+": the path is not a regular file")
	}
	cur, ref := st.readConfinedWorkflow(parent)
	if ref != nil {
		return "", ref
	}
	if bytes.Equal(cur, workflow) {
		return statusKeep, nil
	}
	return statusOverwrite, nil
}

// readConfinedWorkflow reads the committed workflow through the pinned parent.
func (st *state) readConfinedWorkflow(parent *os.Root) ([]byte, *core.RefusalError) {
	f, err := readcommon.OpenRegularNoSymlinkIn(parent, st.ci.base)
	if err != nil {
		return nil, st.workflowRefusal()
	}
	defer f.Close()
	cur, err := io.ReadAll(f)
	if err != nil {
		return nil, st.workflowRefusal()
	}
	return cur, nil
}

// writeWorkflow publishes the rendered workflow, creating the name or
// replacing a confirmed one.
func (st *state) writeWorkflow(workflow []byte, status string) *core.RefusalError {
	if status == statusKeep {
		st.humanf("keep: %s (already up to date)\n", st.ci.path())
		return nil
	}
	parent, base, ref := st.workflowParent()
	if ref != nil {
		return ref
	}
	defer parent.Close()
	if ref := st.publishWorkflow(parent, base, workflow, status); ref != nil {
		return ref
	}
	st.humanf("wrote: %s\n", st.ci.path())
	return nil
}

func (st *state) publishWorkflow(parent *os.Root, base string, workflow []byte, status string) *core.RefusalError {
	if status == statusOverwrite {
		if err := atomic.WriteFileIn(parent, base, workflow); err != nil {
			return st.workflowRefusal()
		}
		return nil
	}
	if created, err := atomic.CreateFileModeIn(parent, base, workflow, workflowMode); err != nil || !created {
		return st.workflowRefusal()
	}
	return nil
}

// workflowParent opens the provider's workflow directory through [os.Root],
// creating the directory components only when absent and refusing a symlinked
// or non-directory component: the workflow is a committed file, so init never
// writes it through a link. Traversal stays strictly below the repository
// root, so a repository reached through a symlinked ancestor (a /var or
// workspace link above it) still writes normally.
func (st *state) workflowParent() (*os.Root, string, *core.RefusalError) {
	root, err := os.OpenRoot(st.req.RepoRootPath())
	if err != nil {
		return nil, "", st.workflowRefusal()
	}
	defer root.Close()
	for _, dir := range st.ci.dirs {
		if ref := st.createWorkflowDir(root, dir); ref != nil {
			return nil, "", ref
		}
	}
	parent, err := readcommon.OpenDirectoryIn(root, st.ci.dirs[len(st.ci.dirs)-1])
	if err != nil {
		return nil, "", st.workflowRefusal()
	}
	return parent, st.ci.base, nil
}

func (st *state) createWorkflowDir(root *os.Root, dir string) *core.RefusalError {
	info, err := root.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if mkErr := root.Mkdir(dir, scaffoldDirPerm); mkErr != nil {
			return st.workflowRefusal()
		}
		return nil
	}
	if err != nil || !info.IsDir() {
		return st.workflowRefusal()
	}
	return nil
}

func (st *state) workflowRefusal() *core.RefusalError {
	return st.refuse(codeInitFailed,
		"init could not write "+st.ci.path()+" (symlinked path, or storage not writable?)")
}

// printWorkflow renders the file and writes nothing: no .verdict directory, no
// workflow directory, and no check-tree pass over a tree it did not touch.
func (st *state) printWorkflow(vals presetVals, workflow []byte) {
	if !st.req.Machine {
		st.humanf("%s", workflow)
	}
	st.at = st.req.TimeUTC()
	st.next = "write it with: " + st.writeCommand()
	st.data = &Data{
		Preset:       st.req.Preset,
		BudgetUSD:    vals.budget,
		KBMinJudged:  jq.Number(vals.kbMin),
		KBMaxFPRate:  jq.Number(vals.kbRate),
		CI:           st.req.CI,
		WorkflowPath: st.ci.path(),
		Workflow:     string(workflow),
	}
}

// writeCommand is the --print line that reproduces these exact bytes: the same
// --ci run, carrying the agent and key secret only when they differ from the
// defaults a bare `verdict init --ci github` already renders.
func (st *state) writeCommand() string {
	out := "verdict init --ci " + st.req.CI
	if st.req.Agent != "" && st.req.Agent != defaultAgent {
		out += " --agent " + st.req.Agent
	}
	if st.req.KeySecret != "" {
		out += " --key-secret " + st.req.KeySecret
	}
	return out
}

func (st *state) writeWithConfirm(wants map[string]string, needOverwrite bool) (int, bool, *core.RefusalError) {
	if code, stop, ref := st.confirmGate(needOverwrite); stop {
		return code, true, ref
	}
	if ref := st.createStateDir(); ref != nil {
		return 0, true, ref
	}
	if ref := st.writeControls(wants); ref != nil {
		return 0, true, ref
	}
	return 0, false, nil
}

// confirmGate asks the overwrite question before anything touches disk: a
// refused or aborted run stops here with nothing created.
func (st *state) confirmGate(needOverwrite bool) (int, bool, *core.RefusalError) {
	if !needOverwrite || st.req.Yes {
		return 0, false, nil
	}
	code, ref := st.confirmOverwrite()
	return code, ref != nil || code != 0, ref
}

// createStateDir creates .verdict only after the confirm gate, so a refused
// run leaves no directory behind.
func (st *state) createStateDir() *core.RefusalError {
	if err := os.MkdirAll(st.reviewDot(), scaffoldDirPerm); err != nil {
		return st.refuse(codeInitFailed,
			fmt.Sprintf("init failed: init could not create .verdict (storage writable?): %v", err))
	}
	return nil
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
		if status == statusOverwrite {
			needOverwrite = true
		}
	}
	return statuses, needOverwrite
}

func (st *state) previewScaffold(wants map[string]string, workflow []byte, wfStatus string) {
	st.humanf("init: preset %s (chill: unpinned/10/0.7, standard: "+
		"1 member/5/0.5, strict: lead+2 lenses/3/0.3)\n", st.req.Preset)
	for _, t := range targets {
		st.humanf("--- .verdict/%s (%s) ---\n", t, st.targetStatus(t, wants[t]))
		st.humanf("%s\n", wants[t])
	}
	if workflow != nil {
		st.humanf("--- %s (%s) ---\n", st.ci.path(), wfStatus)
		st.humanf("%s", workflow)
	}
}

func (st *state) confirmOverwrite() (int, *core.RefusalError) {
	if !st.req.Interactive {
		return 0, st.refuse(CodeNeedsConfirm,
			"init refuses to overwrite existing files without "+
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

func (st *state) isCurrent(t, want string) bool { return st.targetStatus(t, want) == statusKeep }

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

func (st *state) reportNext(vals presetVals, statuses []string, treeClean bool, workflow []byte, wfStatus string) {
	next := "pin real panel slugs in .verdict/config.json, " +
		"tune defaults.budget_usd, then fetch"
	if workflow != nil {
		next = "commit " + st.ci.path() + " with .verdict/, set the repository variable " +
			"VERDICT_VERSION to a release with run --agent, then open a pull request"
	}
	st.humanf("next: %s\n", next)
	st.at = st.req.TimeUTC()
	st.next = next
	st.data = &Data{
		Preset:         st.req.Preset,
		BudgetUSD:      vals.budget,
		KBMinJudged:    jq.Number(vals.kbMin),
		KBMaxFPRate:    jq.Number(vals.kbRate),
		Files:          statuses,
		CheckTreeClean: treeClean,
	}
	if workflow != nil {
		st.data.CI = st.req.CI
		st.data.WorkflowPath = st.ci.path()
		st.data.WorkflowStatus = wfStatus
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

// gitignoreBody is the entire tracked ignore contract: only generated
// state, backtest artifacts, the local index, and the tracked stores' lock
// files stay local, so the configuration and support files are reviewable.
const gitignoreBody = "/state/\n/backtest/\n/index/\nincidents.jsonl.lock\nkb.jsonl.lock\nreverts.jsonl.lock"

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
		return statusCreate
	}
	if string(cur) == want+newlineStr || string(cur) == want {
		return statusKeep
	}
	return statusOverwrite
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
