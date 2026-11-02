// Package install ports the `install` verb: the copy-based skill installer.
// It detects the agent skill dirs at the repo root and $HOME, previews the
// plan (create/update/keep/blocked/skip per root), and — after --yes/--force
// or a human-TTY confirm — copies the tool-owned `verdict/` payload
// (SKILL.md + prompts/ + scripts/) with an installer receipt. Foreign
// skills and unowned verdict/ dirs are never touched; the never-prompts
// doctrine holds (machine mode never prompts, even on a TTY).
package install

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
)

// Install status, scope, and payload constants (revive add-constant,
// goconst: production literals appear 3+ times).
const (
	codeInstallFailed = "install-failed"
	codeNeedsConfirm  = "needs-confirm"
	statusCreate      = "create"
	statusUpdate      = "update"
	statusKeep        = "keep"
	statusBlocked     = "blocked"
	statusSkip        = "skip"
	scopeRepo         = "repo"
	scopeHome         = "home"
	nameVerdict       = "verdict"
	nameSkills        = "skills"
	receiptName       = ".verdict-managed"
	toolFallback      = "0.0.0-unknown"
	skillFile         = "SKILL.md"
	promptsDir        = "prompts"
	scriptsDir        = "scripts"
	agentsDir         = ".agents"
	claudeDir         = ".claude"
	codexDir          = ".codex"
	cwdFallback       = "."
)

// Install filesystem mode and message constants.
const (
	installDirPerm = 0o777
	copyFilePerm   = 0o666
	receiptToolKey = "tool"
)

// Request carries the resolved install invocation.
type Request struct {
	// Env holds the process environment view (HOME resolution).
	Env map[string]string
	// Root is the tool checkout root (the payload source).
	Root string
}

// Run executes `install [--yes] [--json]`, returning the process exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	r.Repo, r.Pr = "", ""
	r.Rentry = "verdict install"
	flags, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if flags.yes {
		r.Rentry += " --yes"
	}
	if r.JSONMode {
		r.Rentry += " --json"
	}
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{
			Code:    "canceled",
			Message: "canceled before install completed",
		}, cli.ExitBroke)
	}
	code, err := runInstall(r, req, flags)
	if err != nil {
		if ref, _ := errors.AsType[*cli.RefusalError](err); ref != nil {
			return r.Refuse(ref)
		}
		return r.Refuse(&cli.RefusalError{
			Code:    codeInstallFailed,
			Message: fmt.Sprintf("install failed: %v", err),
		})
	}
	return code
}

// flags carries the parsed install options.
type flags struct {
	yes bool
}

// parseFlags strips install's flags, reporting unknown ones.
func parseFlags(r *cli.Runner, args []string) (flags, int, bool) {
	var f flags
	for _, a := range args {
		switch a {
		case "--yes", "-y", "--force":
			f.yes = true
		case "--json":
		default:
			msg := fmt.Sprintf(
				"unknown flag '%s' for install (want --yes, --force, --json)",
				a,
			)
			return f, r.ReportUsage(&cli.UsageError{Message: msg}), true
		}
	}
	return f, 0, false
}

// target classifies one candidate root.
type target struct {
	root   string
	scope  string
	status string
	diffs  []string
	skew   bool
}

// runInstall mirrors the install block: payload inventory, root detection,
// preview, confirm gate, apply.
func runInstall(r *cli.Runner, req Request, f flags) (int, error) {
	toolRoot := req.Root
	if err := checkToolRoot(toolRoot); err != nil {
		return 0, err
	}
	toolVersion := readToolVersion(toolRoot)
	payload, err := payloadFiles(toolRoot)
	if err != nil {
		return 0, err
	}
	roots, scopes := candidateRoots()
	targets := classifyAll(toolRoot, toolVersion, payload, roots, scopes)
	announcePlan(r, toolVersion, payload, roots)
	printPreview(r, toolRoot, toolVersion, payload, targets)
	if code, done, err := confirmIfNeeded(r, targets, f); err != nil {
		return 0, err
	} else if done {
		return code, nil
	}
	return finishInstall(r, toolRoot, toolVersion, payload, targets, f.yes)
}

// confirmIfNeeded runs the confirm gate when writes are pending.
func confirmIfNeeded(r *cli.Runner, targets []target, f flags) (int, bool, error) {
	if !needWrite(targets) || f.yes {
		return 0, false, nil
	}
	code, err := confirmInstall(r)
	if err != nil {
		return 0, true, err
	}
	if code != 0 {
		return code, true, nil
	}
	return 0, false, nil
}

// checkToolRoot refuses when the tool payload root is incomplete.
func checkToolRoot(toolRoot string) error {
	if !isFile(filepath.Join(toolRoot, skillFile)) {
		return missingPayload(toolRoot, skillFile)
	}
	if !isDir(filepath.Join(toolRoot, promptsDir)) {
		return missingPayload(toolRoot, promptsDir)
	}
	if !isDir(filepath.Join(toolRoot, scriptsDir)) {
		return missingPayload(toolRoot, scriptsDir)
	}
	return nil
}

// missingPayload builds the install-failed refusal for a missing payload.
func missingPayload(toolRoot, rel string) error {
	return &cli.RefusalError{
		Code: codeInstallFailed,
		Message: fmt.Sprintf(
			"tool payload missing: %s/%s not found (not a verdict checkout?)",
			toolRoot,
			rel,
		),
	}
}

// needWrite reports whether any target needs a write.
func needWrite(targets []target) bool {
	for _, t := range targets {
		if t.status == statusCreate || t.status == statusUpdate {
			return true
		}
	}
	return false
}

// confirmInstall prompts on a human TTY or refuses in machine mode.
// It returns a non-zero exit code when the human aborts the prompt.
func confirmInstall(r *cli.Runner) (int, error) {
	if r.JSONMode || !cli.IsTerminal(os.Stdin) {
		return 0, &cli.RefusalError{
			Code: codeNeedsConfirm,
			Message: "install refuses to write skill files without confirm " +
				"(re-run with --yes after reviewing the preview above)",
		}
	}
	fmt.Fprintf(r.Diag, "Proceed? [y/N] ")
	switch readAnswer() {
	case "y", "Y", "yes", "YES":
		return 0, nil
	default:
		fmt.Fprintf(r.Diag, "install: aborted (re-run with --yes to apply "+
			"after reviewing the preview)\n")
		return 1, nil
	}
}

// announcePlan prints the install header line.
func announcePlan(r *cli.Runner, toolVersion string, payload []string, roots []string) {
	fmt.Fprintf(r.Human, "install: tool v%s, payload %d files "+
		"(SKILL.md + prompts/ + scripts/) -> %d candidate root(s)\n",
		toolVersion, len(payload), len(roots))
}

// finishInstall applies the plan and reports the outcome.
func finishInstall(
	r *cli.Runner,
	toolRoot, toolVersion string,
	payload []string,
	targets []target,
	assumeYes bool,
) (int, error) {
	wrote, err := applyAll(r, toolRoot, toolVersion, targets, assumeYes, cli.NowUTC(r.Now()))
	if err != nil {
		return 0, err
	}
	next := reportDone(r, wrote)
	if r.JSONMode {
		writeInstallEnvelope(r, toolVersion, payload, targets, next)
	}
	return 0, nil
}

// reportDone prints the human completion lines, returning the next hint.
func reportDone(r *cli.Runner, wrote int) string {
	next := "already up to date (foreign dirs untouched)"
	if wrote > 0 {
		next = "reload skills in the agent so it picks up verdict/"
	}
	fmt.Fprintf(r.Human, "install: %d target(s) written\n", wrote)
	fmt.Fprintf(r.Human, "next: %s\n", next)
	return next
}

// writeInstallEnvelope prints the JSON envelope for install.
func writeInstallEnvelope(
	r *cli.Runner,
	toolVersion string,
	payload []string,
	targets []target,
	next string,
) {
	env := jq.NewObj(
		"schema_version", "1",
		"verb", "install",
		"repo", "",
		"pr", nil,
		"at", cli.NowUTC(r.Now()),
		"next", next,
		"warnings", cliStrings(r.Warnings),
		"data", jq.NewObj(
			"tool_version", toolVersion,
			"payload_files", len(payload),
			"targets", targetJSON(targets),
		),
		"error", nil,
	)
	fmt.Fprintln(r.Out, env.MarshalPretty())
}

// readToolVersion ports the package.json version read with the
// 0.0.0-unknown fallback.
func readToolVersion(toolRoot string) string {
	raw, err := os.ReadFile(filepath.Join(toolRoot, "package.json"))
	if err != nil {
		return toolFallback
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return toolFallback
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return toolFallback
	}
	v, ok := m["version"]
	if !ok {
		return "null"
	}
	return jq.R(v)
}

// payloadFiles ports the payload inventory: SKILL.md, prompts/*.md, and
// the scripts/ tree (release entry points ship beside the skill scripts,
// so the installed copy mirrors the checkout like the registry tarball).
func payloadFiles(toolRoot string) ([]string, error) {
	payload := []string{skillFile}
	var err error
	payload, err = appendPrompts(toolRoot, payload)
	if err != nil {
		return nil, err
	}
	payload, err = appendScripts(toolRoot, payload)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

// appendPrompts appends sorted prompt payload files.
func appendPrompts(toolRoot string, payload []string) ([]string, error) {
	prompts, err := filepath.Glob(filepath.Join(toolRoot, promptsDir, "*.md"))
	if err != nil {
		return nil, missingPrompts(toolRoot)
	}
	slices.Sort(prompts)
	for _, pf := range prompts {
		if !isFile(pf) {
			continue
		}
		payload = append(payload, promptsDir+"/"+filepath.Base(pf))
	}
	if len(payload) <= 1 {
		return nil, missingPrompts(toolRoot)
	}
	return payload, nil
}

// appendScripts appends the scripts/ tree in sorted order: every regular
// file with its scripts/-relative path, so nested helpers (npm-shim/)
// install with the same layout the registry tarball carries.
func appendScripts(toolRoot string, payload []string) ([]string, error) {
	rels, err := scriptRels(filepath.Join(toolRoot, scriptsDir))
	if err != nil {
		return nil, missingScripts(toolRoot)
	}
	before := len(payload)
	for _, rel := range rels {
		if !isFile(filepath.Join(toolRoot, scriptsDir, filepath.FromSlash(rel))) {
			continue
		}
		payload = append(payload, scriptsDir+"/"+rel)
	}
	if len(payload) <= before {
		return nil, missingScripts(toolRoot)
	}
	return payload, nil
}

// scriptRels lists the regular files under root as root-relative slash
// paths in sorted order. Any walk failure reads as a missing payload.
func scriptRels(root string) ([]string, error) {
	prefix := root + string(filepath.Separator)
	var rels []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rels = append(rels, filepath.ToSlash(strings.TrimPrefix(path, prefix)))
		return nil
	})
	if err != nil {
		//nolint:wrapcheck // fresh error created here; nothing to wrap
		return nil, fmt.Errorf("walk %s (not a verdict checkout?)", root)
	}
	slices.Sort(rels)
	return rels, nil
}

// missingPrompts builds the refusal for an empty prompts glob.
func missingPrompts(toolRoot string) error {
	return &cli.RefusalError{
		Code: codeInstallFailed,
		Message: fmt.Sprintf(
			"tool payload missing: no prompts found in %s/prompts "+
				"(not a verdict checkout?)",
			toolRoot,
		),
	}
}

// missingScripts builds the refusal for an empty scripts glob.
func missingScripts(toolRoot string) error {
	return &cli.RefusalError{
		Code: codeInstallFailed,
		Message: fmt.Sprintf(
			"tool payload missing: no scripts found in %s/scripts "+
				"(not a verdict checkout?)",
			toolRoot,
		),
	}
}

// candidateRoots ports the six candidate roots (repo scope, then home
// scope) with duplicate suppression. Home roots exist only when HOME is
// set and non-empty (matching `[[ -n "${HOME:-}" ]]` — an unset HOME never
// falls back to the cwd here).
func candidateRoots() ([]string, []string) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = cwdFallback
	}
	roots := []string{
		filepath.Join(cwd, agentsDir, nameSkills),
		filepath.Join(cwd, claudeDir, nameSkills),
		filepath.Join(cwd, codexDir, nameSkills),
	}
	scopes := []string{scopeRepo, scopeRepo, scopeRepo}
	home, homeSet := os.LookupEnv("HOME")
	if homeSet && home != "" {
		roots = append(roots,
			filepath.Join(home, agentsDir, nameSkills),
			filepath.Join(home, claudeDir, nameSkills),
			filepath.Join(home, codexDir, nameSkills),
		)
		scopes = append(scopes, scopeHome, scopeHome, scopeHome)
	}
	return dedupeRoots(roots, scopes)
}

// dedupeRoots suppresses duplicate candidate roots, keeping scopes aligned.
func dedupeRoots(roots, scopes []string) ([]string, []string) {
	var out []string
	var outScopes []string
	seen := map[string]bool{}
	for i, cr := range roots {
		if seen[cr] {
			continue
		}
		seen[cr] = true
		out = append(out, cr)
		outScopes = append(outScopes, scopes[i])
	}
	return out, outScopes
}

// classifyAll classifies every candidate root without writing.
func classifyAll(toolRoot, toolVersion string, payload, roots, scopes []string) []target {
	targets := make([]target, 0, len(roots))
	for i, root := range roots {
		targets = append(targets, classifyOne(toolRoot, toolVersion, payload, root, scopes[i]))
	}
	return targets
}

// classifyOne ports the per-root classification.
func classifyOne(toolRoot, toolVersion string, payload []string, root, scope string) target {
	t := target{root: root, scope: scope}
	if missingAgentDir(root) {
		t.status = statusSkip
		return t
	}
	tgt, receipt := verdictPaths(root)
	if !fileExists(tgt) {
		t.status = statusCreate
		t.diffs = append([]string(nil), payload...)
		return t
	}
	if !ownedSlot(receipt) {
		t.status = statusBlocked
		return t
	}
	classifyOwned(toolRoot, toolVersion, payload, &t, tgt, receipt)
	return t
}

// missingAgentDir reports whether neither root nor its parent exists.
func missingAgentDir(root string) bool {
	return !isDir(root) && !isDir(filepath.Dir(root))
}

// verdictPaths returns the verdict slot and receipt paths for a root.
func verdictPaths(root string) (string, string) {
	tgt := filepath.Join(root, nameVerdict)
	return tgt, filepath.Join(tgt, receiptName)
}

// ownedSlot reports whether receipt exists and marks installer ownership.
func ownedSlot(receipt string) bool {
	return isFile(receipt) && receiptOwned(receipt)
}

// classifyOwned classifies an owned verdict slot by payload diffs.
func classifyOwned(
	toolRoot, toolVersion string,
	payload []string,
	t *target,
	tgt, receipt string,
) {
	t.skew = receiptVersion(receipt) != toolVersion
	t.diffs = ownedDiffs(toolRoot, tgt, payload)
	if len(t.diffs) == 0 {
		t.status = statusKeep
	} else {
		t.status = statusUpdate
	}
}

// ownedDiffs lists payload files differing from the installed slot.
func ownedDiffs(toolRoot, tgt string, payload []string) []string {
	var diffs []string
	for _, rel := range payload {
		if isFile(filepath.Join(tgt, rel)) &&
			filesEqual(filepath.Join(toolRoot, rel), filepath.Join(tgt, rel)) {
			continue
		}
		diffs = append(diffs, rel)
	}
	return diffs
}

// receiptOwned ports `jq -e '.tool == "verdict"'` on the receipt.
func receiptOwned(receipt string) bool {
	raw, err := os.ReadFile(receipt)
	if err != nil {
		return false
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return false
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return false
	}
	return m[receiptToolKey] == nameVerdict
}

// receiptVersion reads the receipt version (empty when absent).
func receiptVersion(receipt string) string {
	raw, err := os.ReadFile(receipt)
	if err != nil {
		return ""
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return ""
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return ""
	}
	return jq.StrOf(m["version"])
}

// filesEqual compares two files byte for byte (the cmp -s gate).
func filesEqual(a, b string) bool {
	ra, err := os.ReadFile(a)
	if err != nil {
		return false
	}
	rb, err := os.ReadFile(b)
	if err != nil {
		return false
	}
	if len(ra) != len(rb) {
		return false
	}
	for i := range ra {
		if ra[i] != rb[i] {
			return false
		}
	}
	return true
}

// printPreview ports the preview plan print.
func printPreview(r *cli.Runner, toolRoot, toolVersion string, payload []string, targets []target) {
	for i := range targets {
		previewOne(r, toolRoot, toolVersion, payload, &targets[i])
	}
}

// previewOne prints the preview block for one target.
func previewOne(r *cli.Runner, toolRoot, toolVersion string, payload []string, t *target) {
	tgt := filepath.Join(t.root, nameVerdict)
	switch t.status {
	case statusCreate:
		previewCreate(r, toolRoot, tgt, t)
	case statusUpdate:
		previewUpdate(r, toolRoot, payload, tgt, t)
	case statusKeep:
		previewKeep(r, tgt, t, toolVersion)
	case statusBlocked:
		previewBlocked(r, tgt, t)
	case statusSkip:
		previewSkip(r, t)
	}
}

// previewCreate prints a create block.
func previewCreate(r *cli.Runner, toolRoot, tgt string, t *target) {
	fmt.Fprintf(r.Human, "--- %s [%s] (create) ---\n", tgt, t.scope)
	for _, rel := range t.diffs {
		if rel == "" {
			continue
		}
		fmt.Fprintf(r.Human, "  + %s (%d bytes)\n", rel, fileSize(filepath.Join(toolRoot, rel)))
	}
}

// previewUpdate prints an update block.
func previewUpdate(r *cli.Runner, toolRoot string, payload []string, tgt string, t *target) {
	receipt := filepath.Join(tgt, receiptName)
	recV := receiptVersion(receipt)
	if recV == "" {
		recV = "unknown"
	}
	fmt.Fprintf(r.Human, "--- %s [%s] (update, installed v%s) ---\n", tgt, t.scope, recV)
	for _, rel := range t.diffs {
		previewDiffLine(r, toolRoot, tgt, rel)
	}
	fmt.Fprintf(r.Human, "  = %d file(s) unchanged\n", len(payload)-diffLines(t.diffs))
}

// previewDiffLine prints one update diff line.
func previewDiffLine(r *cli.Runner, toolRoot, tgt, rel string) {
	if rel == "" {
		return
	}
	if isFile(filepath.Join(tgt, rel)) {
		fmt.Fprintf(r.Human, "  ~ %s (differs: installed %d -> payload %d bytes)\n",
			rel, fileSize(filepath.Join(tgt, rel)), fileSize(filepath.Join(toolRoot, rel)))
		return
	}
	fmt.Fprintf(r.Human, "  + %s (new, %d bytes)\n", rel, fileSize(filepath.Join(toolRoot, rel)))
}

// previewKeep prints a keep block with an optional skew nudge.
func previewKeep(r *cli.Runner, tgt string, t *target, toolVersion string) {
	fmt.Fprintf(r.Human, "--- %s [%s] (keep, already up to date) ---\n", tgt, t.scope)
	if !t.skew {
		return
	}
	receipt := filepath.Join(tgt, receiptName)
	r.Warn(fmt.Sprintf(
		"version skew at %s: receipt v%s vs tool v%s "+
			"(nudge only — re-run with --yes to refresh the receipt)",
		tgt, receiptVersion(receipt), toolVersion))
}

// previewBlocked prints a blocked block.
func previewBlocked(r *cli.Runner, tgt string, t *target) {
	fmt.Fprintf(r.Human, "--- %s [%s] (blocked: no installer receipt — "+
		"foreign-owned, left untouched) ---\n", tgt, t.scope)
	r.Warn(fmt.Sprintf(
		"leaving foreign-owned %s untouched (no .verdict-managed receipt — "+
			"remove it by hand to let the installer own this slot, nothing was written)",
		tgt))
}

// previewSkip prints a skip block.
func previewSkip(r *cli.Runner, t *target) {
	fmt.Fprintf(r.Human, "--- %s [%s] (skip: no agent dir) ---\n", t.root, t.scope)
}

// diffLines counts non-empty diff lines (the `grep -c .` shape).
func diffLines(diffs []string) int {
	n := 0
	for _, d := range diffs {
		if d != "" {
			n++
		}
	}
	return n
}

// fileSize reads a file size (0 when unreadable).
func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// applyAll ports phase 2: owned namespaces are written, foreign slots and
// undetected roots skipped. Returns the written target count.
func applyAll(
	r *cli.Runner,
	toolRoot, toolVersion string,
	targets []target,
	assumeYes bool,
	now string,
) (int, error) {
	wrote := 0
	for i := range targets {
		count, err := applyOne(r, toolRoot, toolVersion, &targets[i], assumeYes, now)
		if err != nil {
			return wrote, err
		}
		wrote += count
	}
	return wrote, nil
}

// applyOne applies the plan for one target, returning targets written.
func applyOne(
	r *cli.Runner,
	toolRoot, toolVersion string,
	t *target,
	assumeYes bool,
	now string,
) (int, error) {
	tgt := filepath.Join(t.root, nameVerdict)
	receipt := filepath.Join(tgt, receiptName)
	switch t.status {
	case statusCreate, statusUpdate:
		return applyWritable(r, toolRoot, tgt, receipt, t, toolVersion, now)
	case statusKeep:
		return applyKeep(r, tgt, receipt, t, assumeYes, toolVersion, now)
	case statusBlocked:
		fmt.Fprintf(r.Human, "skip: %s (foreign-owned, left untouched)\n", tgt)
		return 0, nil
	default:
		fmt.Fprintf(r.Human, "skip: %s (no agent dir)\n", t.root)
		return 0, nil
	}
}

// applyWritable writes one create/update slot plus its receipt.
func applyWritable(
	r *cli.Runner,
	toolRoot, tgt, receipt string,
	t *target,
	toolVersion, now string,
) (int, error) {
	if err := applyTarget(toolRoot, tgt, t.diffs); err != nil {
		return 0, &cli.RefusalError{Code: codeInstallFailed, Message: err.Error()}
	}
	if err := writeReceipt(receipt, toolVersion, now); err != nil {
		return 0, cannotWriteReceipt(receipt)
	}
	fmt.Fprintf(r.Human, "wrote: %s (%d file(s), receipt v%s)\n", tgt, diffLines(t.diffs), toolVersion)
	return 1, nil
}

// applyKeep keeps one slot, refreshing a skewed receipt when confirmed.
func applyKeep(
	r *cli.Runner,
	tgt, receipt string,
	t *target,
	assumeYes bool,
	toolVersion, now string,
) (int, error) {
	fmt.Fprintf(r.Human, "keep: %s (already up to date)\n", tgt)
	if !t.skew || !assumeYes {
		return 0, nil
	}
	if err := writeReceipt(receipt, toolVersion, now); err != nil {
		return 0, cannotWriteReceipt(receipt)
	}
	fmt.Fprintf(r.Human, "receipt: %s refreshed to v%s\n", tgt, toolVersion)
	return 0, nil
}

// cannotWriteReceipt builds the receipt-write refusal.
func cannotWriteReceipt(receipt string) error {
	return &cli.RefusalError{
		Code:    codeInstallFailed,
		Message: fmt.Sprintf("install could not write %s (storage writable?)", receipt),
	}
}

// applyTarget copies the diff payload files into the target (plain cp
// semantics: content copied, default permissions; the single mkdir ports
// the shell's one `mkdir -p` with its prompts-naming error).
func applyTarget(toolRoot, tgt string, diffs []string) error {
	if err := os.MkdirAll(filepath.Join(tgt, promptsDir), installDirPerm); err != nil {
		//nolint:wrapcheck // fresh error created here; nothing to wrap
		return fmt.Errorf("install could not create %s/prompts (storage writable?)", tgt)
	}
	if err := os.MkdirAll(filepath.Join(tgt, scriptsDir), installDirPerm); err != nil {
		//nolint:wrapcheck // fresh error created here; nothing to wrap
		return fmt.Errorf("install could not create %s/prompts (storage writable?)", tgt)
	}
	for _, rel := range diffs {
		if rel == "" {
			continue
		}
		if err := copyPayloadFile(toolRoot, tgt, rel); err != nil {
			return err
		}
	}
	return nil
}

// copyPayloadFile stages parent dirs, then copies one payload file.
func copyPayloadFile(toolRoot, tgt, rel string) error {
	dst := filepath.Join(tgt, rel)
	if err := os.MkdirAll(filepath.Dir(dst), installDirPerm); err != nil {
		//nolint:wrapcheck // fresh error created here; nothing to wrap
		return fmt.Errorf("install could not create %s (storage writable?)", filepath.Dir(dst))
	}
	if err := copyFile(filepath.Join(toolRoot, rel), dst); err != nil {
		//nolint:wrapcheck // fresh error created here; nothing to wrap
		return fmt.Errorf("install could not write %s/%s (storage writable?)", tgt, rel)
	}
	return nil
}

// copyFile copies one payload file with cp semantics: bytes copied, default
// permissions on create, existing mode kept on overwrite.
func copyFile(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return fmt.Errorf("read %s: %v", src, err)
	}
	//nolint:gosec // G306: installer copies tool payload with cp semantics (0666 & umask).
	if err := os.WriteFile(dst, raw, copyFilePerm); err != nil {
		//nolint:wrapcheck,errorlint // %v seals the boundary by design (GO-ERRORS-03)
		return fmt.Errorf("write %s: %v", dst, err)
	}
	return nil
}

// writeReceipt writes the installer receipt (pretty, like `jq -n`, via the
// shared atomic path).
func writeReceipt(receipt, toolVersion, now string) error {
	doc := jq.NewObj(receiptToolKey, nameVerdict, "version", toolVersion, "installed_at", now)
	return atomic.WriteFile(receipt, []byte(doc.MarshalPretty()+"\n"))
}

// targetJSON builds the envelope targets array.
func targetJSON(targets []target) []any {
	out := make([]any, 0, len(targets))
	for i := range targets {
		out = append(out, targetEntry(&targets[i]))
	}
	return out
}

// targetEntry builds one envelope target object.
func targetEntry(t *target) any {
	return jq.NewObj(
		"root", t.root,
		"scope", t.scope,
		"status", t.status,
		"files_written", filesWritten(t),
		"version_skew", t.skew,
	)
}

// filesWritten counts writable files for create/update targets.
func filesWritten(t *target) int {
	if t.status == statusCreate || t.status == statusUpdate {
		return diffLines(t.diffs)
	}
	return 0
}

// readAnswer reads one stdin line for the human confirm.
func readAnswer() string {
	rd := bufio.NewReader(os.Stdin)
	line, err := rd.ReadString('\n')
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(line, "\n")
}

// isFile reports whether path is a regular file.
func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

// isDir reports whether path is a directory.
func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// fileExists reports whether path exists (any type), mirroring `[[ -e ]]`
// (symlinks included: [os.Stat] follows them, like test -e).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// cliStrings converts warnings to JSON string values.
func cliStrings(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}
