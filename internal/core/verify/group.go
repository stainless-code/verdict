package verifycore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/contenttree"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

// Codes the explicit group-validation mode adds. Patch mode keeps its own
// codes and behavior byte for byte.
const (
	codeBadGroupValidation = "bad-group-validation"
	codeNoRunBundle        = "no-run-bundle"
	codeUnknownGroup       = "unknown-group"
	codeHeadMismatch       = "head-mismatch"
	validationSchema       = "2"
)

// receiptNamePrefix and receiptSuffix frame the per-group receipt file: one
// file per group, so a later group can never overwrite an earlier verdict.
const (
	receiptNamePrefix = "validation-"
	receiptSuffix     = ".json"
	receiptCap        = 64 << 10
	maxGroupIDLen     = 64
)

// RunGroup performs deep group-validation mode: it proves the captured run
// bundle still matches its controls, resolves the requested stable group,
// pins the reviewed head and the capture identity to the checked-out head
// before executing, runs the operator's explicit command in the canonical
// reviewed root through the shared allowlist and watchdog, and records a
// tool-produced receipt bound to the run, the group, and both heads. It
// never reads, writes, or fabricates patch.json, and every outcome of an
// executed command leaves a receipt — a success is never left standing as
// the latest completion after a later failure.
func RunGroup(ctx context.Context, req *Request, dir, now string) (resp *Response, ref *core.RefusalError) {
	st := &state{req: req, warnings: append([]string{}, req.Warnings...)}
	if ref := sandboxFlagsRefusal(req); ref != nil {
		return nil, st.withWarnings(ref)
	}
	run, ref := st.prepareValidation(ctx, dir, req)
	if ref != nil {
		return nil, st.withWarnings(ref)
	}
	if ref := st.prepareEnv(dir, run.rs.BaseSHA); ref != nil {
		return nil, st.withWarnings(ref)
	}
	// The command runs in a snapshot of the checkout's content, which must be
	// the reviewed commit's tree; nothing done to the checkout while it runs
	// changes what it tested.
	snap, ref := st.takeSnapshot(ctx, run.commander, run.root, run.timeout)
	if ref != nil {
		return nil, st.withWarnings(ref)
	}
	run.snap = snap
	// A backstop for the refusal paths; the run removes it as soon as the
	// command ends, so a cleanup warning reaches the response.
	defer func() { st.finishSnapshot(ctx, run.snap, ref) }()
	if snap.content.Tree != run.tree {
		return nil, st.withWarnings(otherContent(run, snap.content.Filtered))
	}
	run.workDir = snap.dir
	if ref := st.bindSandbox(ctx, run); ref != nil {
		return nil, st.withWarnings(ref)
	}
	return st.executeValidation(ctx, dir, now, req, run)
}

// bindSandbox prepares the opt-in wrapper before any receipt exists, so an
// unenforceable sandbox refuses without inventing evidence.
func (st *state) bindSandbox(ctx context.Context, run *validationRun) *core.RefusalError {
	sb, ref := st.sandboxed(ctx, run.commander, run.workDir, run.workDir)
	if ref != nil {
		return ref
	}
	run.sandbox = sb
	return nil
}

// validationRun pins the facts a receipt must certify: the stable group, the
// capture identity verified before execution, the canonical reviewed root,
// the reviewed head, and the command budget.
type validationRun struct {
	commander core.Commander
	sandbox   *sandbox
	// snap is the snapshot the command runs in; nil once removed.
	snap         *snapshot
	rs           *config.RunState
	group        config.PlanGroup
	receiptPath  string
	root         string
	reviewedHead string
	// tree is the reviewed content's tree id, proven equal to the checkout's
	// content before anything ran; workDir is its snapshot checkout.
	tree    string
	workDir string
	git     contenttree.Git
	timeout int
}

// prepareValidation performs every pre-execution check in the gate order the
// contract fixes: both-or-neither, canonical root, pinned capture, timeout,
// allowlist, commander, reviewed head, then the checked-out head. A refusal
// here means nothing ran, so no receipt is invented.
func (st *state) prepareValidation(ctx context.Context, dir string, req *Request) (*validationRun, *core.RefusalError) {
	run, ref := st.validationInputs(dir, req)
	if ref != nil {
		return nil, ref
	}
	run.git = contenttree.Git{Cmdr: run.commander, Root: run.root, Env: req.Environ}
	if ref := bindReviewedHead(ctx, run, dir); ref != nil {
		return nil, ref
	}
	return run, nil
}

// validationInputs proves the request shape, canonical root, pinned capture,
// budget, allowlist, and commander. Nothing executes here.
func (st *state) validationInputs(dir string, req *Request) (*validationRun, *core.RefusalError) {
	if req.Group == "" || req.Command == "" {
		return nil, &core.RefusalError{Code: codeBadGroupValidation, Message: fmt.Sprintf(
			"group validation needs both --group and --command (got group=%q command=%s); "+
				"neither keeps the patch verify path", req.Group, present(req.Command))}
	}
	run, ref := st.pinnedRun(dir, req)
	if ref != nil {
		return nil, ref
	}
	timeout, ref := validationTimeout(req.TimeoutRaw)
	if ref != nil {
		return nil, ref
	}
	if ref := Check(req.Command); ref != nil {
		return nil, ref
	}
	run.commander = st.commander()
	if run.commander == nil {
		return nil, failed(errors.New("no commander in runtime"))
	}
	run.timeout = timeout
	return run, nil
}

// pinnedRun canonicalizes the reviewed root and pins the capture, receipt
// path, and requested stable group.
func (st *state) pinnedRun(dir string, req *Request) (*validationRun, *core.RefusalError) {
	root, ref := canonicalRoot(req.RepoRootPath())
	if ref != nil {
		return nil, ref
	}
	group, receiptPath, rs, ref := st.pinGroup(dir, req.Group)
	if ref != nil {
		return nil, ref
	}
	return &validationRun{group: group, receiptPath: receiptPath, rs: rs, root: root}, nil
}

// validationTimeout parses the operator budget into a positive second count.
func validationTimeout(raw string) (int, *core.RefusalError) {
	timeout, ok := parseTimeout(raw)
	if !ok {
		return 0, &core.RefusalError{Code: codeBadTimeout, Message: fmt.Sprintf(
			"timeout must be a positive number of seconds, got '%s'", raw)}
	}
	return timeout, nil
}

// bindReviewedHead pins the reviewed head from bundle metadata and proves the
// checkout is on it before anything runs.
func bindReviewedHead(ctx context.Context, run *validationRun, dir string) *core.RefusalError {
	reviewedHead := readcommon.OpenBundle(dir).Head()
	if reviewedHead == "" {
		return &core.RefusalError{Code: codeHeadMismatch, Message: fmt.Sprintf(
			"bundle %s carries no reviewed head to bind validation to (re-fetch it)", dir)}
	}
	// The receipt certifies content, so the checkout's content — committed or
	// not — must be the reviewed commit's tree; HEAD itself does not matter.
	// The snapshot the command runs in is what gets compared (RunGroup).
	want, err := run.git.Commit(ctx, reviewedHead)
	if errors.Is(err, contenttree.ErrUnresolved) {
		return &core.RefusalError{Code: codeHeadMismatch, Message: fmt.Sprintf(
			"the reviewed head %s is not in the local repository — fetch it, then validate",
			readcommon.ShortSHA(reviewedHead))}
	}
	if err != nil {
		return failed(err)
	}
	run.tree = want
	run.reviewedHead = reviewedHead
	return nil
}

// otherContent refuses a snapshot whose content is not the reviewed commit's.
// Content a filter driver handles never is, however clean the checkout, so
// that refusal names the driver (git-lfs) instead of asking for a checkout.
func otherContent(run *validationRun, filtered []contenttree.Filtered) *core.RefusalError {
	if len(filtered) > 0 {
		return &core.RefusalError{Code: codeHeadMismatch, Message: fmt.Sprintf(
			"the content at %s is not the reviewed commit %s's: %s — validation certifies the reviewed "+
				"content only", run.root, readcommon.ShortSHA(run.reviewedHead), filteredNote(filtered[0]))}
	}
	return &core.RefusalError{Code: codeHeadMismatch, Message: fmt.Sprintf(
		"the content at %s is not the reviewed commit %s's — check out the reviewed head with no "+
			"other changes; validation certifies the reviewed content only", run.root, readcommon.ShortSHA(run.reviewedHead))}
}

// executeValidation runs the pinned command once and records its outcome.
func (st *state) executeValidation(ctx context.Context, dir, now string, req *Request,
	run *validationRun,
) (*Response, *core.RefusalError) {
	if !coreexec.WatchdogEnforced() {
		st.warn("no ps(1) — timeout unenforced for this run")
	}
	receipt := core.ValidationReceipt{
		SchemaVersion: validationSchema, Run: run.rs.Name, InputsSHA256: run.rs.InputsSHA256,
		Group: run.group.ID, ReviewedHead: run.reviewedHead, Tree: run.tree,
		RepoRoot: run.root, Command: req.Command, At: now, Sandboxed: run.sandbox.full(),
		SandboxMechanism: run.sandbox.name(),
	}
	// Pin the bundle before the command can touch it, as the patch path does:
	// the receipt is written only through this root, and a command that moves
	// or replaces the bundle directory leaves no receipt anywhere.
	bundle, ref := openBundle(dir)
	if ref != nil {
		st.releaseSandbox(run.sandbox)
		return nil, st.withWarnings(ref)
	}
	defer bundle.close()
	st.human(fmt.Sprintf("» group validation %s: %s in %s (timeout %ss)",
		run.group.ID, req.Command, run.root, req.TimeoutRaw))
	res, runRef := runCommand(ctx, run.commander, req.Command, run.timeout, commandDir(run.workDir, run.sandbox),
		st.childEnv(), run.sandbox)
	st.releaseSandbox(run.sandbox)
	st.removeSnapshot(ctx, run.snap)
	run.snap = nil
	if ref := bundle.unchanged(); ref != nil {
		return nil, st.withWarnings(ref)
	}
	st.applyExecution(ctx, req, &receipt, res, runRef)
	st.applyPostChecks(dir, run, &receipt)
	if ref := bundle.writeValidationReceipt(filepath.Base(run.receiptPath), &receipt); ref != nil {
		return nil, st.withWarnings(ref)
	}
	st.human(fmt.Sprintf("» exit=%d timed_out=%t status=%s recorded into %s",
		receipt.Exit, receipt.TimedOut, receipt.Status, run.receiptPath))
	st.warnOutcomes(&receipt, runRef)
	return st.groupResponse(dir, run.receiptPath, &receipt), nil
}

// warnOutcomes surfaces the run and head failures, then any non-passing
// status with its reason, after the receipt that records them is written.
func (st *state) warnOutcomes(receipt *core.ValidationReceipt, outcomes ...*core.RefusalError) {
	for _, outcome := range outcomes {
		if outcome != nil {
			st.warn(outcome.Message)
		}
	}
	if receipt.Status != statusPassed && receipt.Failure != "" {
		st.warn("group validation " + receipt.Status + ": " + receipt.Failure)
	}
}

// applyExecution stamps the captured execution facts. A cancellation or a
// watchdog timeout is recorded as such even when the injected runner returns
// nil error, so the receipt can never claim a pass for a stopped command.
func (st *state) applyExecution(ctx context.Context, req *Request,
	receipt *core.ValidationReceipt, res core.Result, runRef *core.RefusalError,
) {
	// Sanitize the whole capture before cutting, as the patch path does: a cut
	// first could leave a credential's unrecognizable second half in the tail.
	receipt.OutputTail = coreexec.TrimTail(lastBytes(sanitizeTail(res.Stdout+res.Stderr, req.Environ), tailCap))
	receipt.Exit = res.ExitCode
	receipt.TimedOut = res.TimedOut
	switch {
	case ctx.Err() != nil || (runRef != nil && res.TimedOut):
		receipt.Status = statusTimeout
		receipt.Failure = "the run was canceled or timed out before the outcome could be certified"
	case runRef != nil:
		receipt.Status = statusUnavailable
		receipt.Failure = runRef.Message
	}
}

// applyPostChecks re-reads the capture after execution and classifies the
// final status from the recorded facts: a later failure always replaces an
// earlier success. The command ran in a snapshot, so the checkout's head and
// tree no longer bear on what it tested.
func (st *state) applyPostChecks(dir string, run *validationRun, receipt *core.ValidationReceipt) {
	if ref := st.recheckCapture(dir, run.rs); ref != nil {
		receipt.Status = statusUnavailable
		receipt.Failure = ref.Message
	}
	classifyReceipt(receipt)
}

// classifyReceipt derives the final status only from recorded facts: a clean
// exit passes, and anything else fails with the real exit named.
func classifyReceipt(receipt *core.ValidationReceipt) {
	if receipt.Status == "" {
		receipt.Status = statusFailed
		if receipt.Exit == 0 && !receipt.TimedOut {
			receipt.Status = statusPassed
		}
	}
	if receipt.Status == statusFailed && receipt.Failure == "" {
		receipt.Failure = fmt.Sprintf("the validation command exited %d", receipt.Exit)
	}
	receipt.Passed = receipt.Status == statusPassed
}

// Receipt status tokens: only statusPassed can approve a deep group.
const (
	statusPassed      = "passed"
	statusFailed      = "failed"
	statusTimeout     = "timeout"
	statusUnavailable = "unavailable"
	statusStaleHead   = "stale-head"
)

// present renders the command-presence half of the both-or-neither refusal.
func present(command string) string {
	if command == "" {
		return "missing"
	}
	return "set"
}

// CanonicalRoot is the shared physical-root resolution the deep gate uses:
// receipt identity and gate comparison both name the same checkout however
// the caller spelled it, and a resolution failure is an error, not a fallback.
func CanonicalRoot(root string) (string, *core.RefusalError) { return canonicalRoot(root) }

// canonicalRoot resolves the physical reviewed root: the receipt identity and
// the command working directory both name the same checkout whether the
// caller passed the root or a subdirectory, and a symlinked or relative path
// canonicalizes once. A resolution failure refuses instead of falling back.
func canonicalRoot(root string) (string, *core.RefusalError) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", failed(err)
	}
	physical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", &core.RefusalError{Code: codeVerifyFailed, Message: fmt.Sprintf(
			"could not canonicalize reviewed root %s: %v (no unverified-root fallback)", root, err)}
	}
	return physical, nil
}

// recheckCapture re-verifies the capture after the command and compares the
// identity pinned before it: a bundle re-fetched or edited mid-command never
// has the fresh command blessed as evidence for the older capture.
func (st *state) recheckCapture(dir string, pinned *config.RunState) *core.RefusalError {
	after, ref := config.VerifyRunState(dir)
	if ref != nil {
		return ref
	}
	if after == nil || after.Name != pinned.Name || after.InputsSHA256 != pinned.InputsSHA256 {
		return &core.RefusalError{Code: codeVerifyFailed, Message: fmt.Sprintf(
			"the run capture changed while the command ran (run %q inputs %s → %s) — command is not evidence for it",
			pinned.Name, readcommon.ShortSHA(pinned.InputsSHA256), readcommon.ShortSHA(afterInputs(after)))}
	}
	return nil
}

// afterInputs reads a re-verified bundle's digest, tolerating absence.
func afterInputs(rs *config.RunState) string {
	if rs == nil {
		return ""
	}
	return rs.InputsSHA256
}

// pinGroup proves the bundle's captured run state, returns the requested
// stable group, and pins the capture identity the receipt must bind.
func (st *state) pinGroup(dir, groupID string) (config.PlanGroup, string, *config.RunState, *core.RefusalError) {
	rs, ref := config.VerifyRunState(dir)
	if ref != nil {
		return config.PlanGroup{}, "", nil, ref
	}
	if ref := verifiedRun(rs, dir); ref != nil {
		return config.PlanGroup{}, "", nil, ref
	}
	group, path, ref := findPinnedGroup(rs, dir, groupID)
	if ref != nil {
		return config.PlanGroup{}, "", nil, ref
	}
	return group, path, rs, nil
}

// verifiedRun refuses a missing bundle, a missing plan, or a capture without
// integrity provenance before any group is resolved.
func verifiedRun(rs *config.RunState, dir string) *core.RefusalError {
	if rs == nil || rs.Resolved == nil {
		return &core.RefusalError{Code: codeNoRunBundle, Message: fmt.Sprintf(
			"bundle %s has no verified run.json (re-fetch it before deep validation)", dir)}
	}
	if rs.InputsSHA256 == "" || !rs.Verified() {
		return &core.RefusalError{Code: codeNoRunBundle, Message: fmt.Sprintf(
			"bundle %s carries no verified provenance (re-fetch it before deep validation)", dir)}
	}
	return nil
}

// findPinnedGroup resolves the requested stable id to its plan group and
// confined receipt path, listing known ids when the request is unknown.
func findPinnedGroup(rs *config.RunState, dir, groupID string) (config.PlanGroup, string, *core.RefusalError) {
	groups := rs.Resolved.Plan.Groups
	for i := range groups {
		if groups[i].ID != groupID {
			continue
		}
		path, ok := receiptPath(dir, groupID)
		if !ok {
			return config.PlanGroup{}, "", &core.RefusalError{Code: codeBadGroupValidation, Message: fmt.Sprintf(
				"group id %q cannot name a confined receipt file", groupID)}
		}
		return groups[i], path, nil
	}
	ids := make([]string, 0, len(groups))
	for i := range groups {
		ids = append(ids, groups[i].ID)
	}
	slices.Sort(ids)
	return config.PlanGroup{}, "", &core.RefusalError{Code: codeUnknownGroup, Message: fmt.Sprintf(
		"run %s has no plan group %q (groups: %v)", rs.Name, groupID, ids)}
}

// receiptPath renders the per-group receipt path, refusing any identity that
// could escape the bundle directory.
func receiptPath(dir, groupID string) (string, bool) {
	if groupID == "" || len(groupID) > maxGroupIDLen {
		return "", false
	}
	for _, r := range groupID {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			return "", false
		}
	}
	return filepath.Join(dir, receiptNamePrefix+groupID+receiptSuffix), true
}

// writeValidationReceipt persists a group receipt atomically through the
// pinned bundle, after one last identity check: the evidence is the file
// itself, so a failed or timed-out run is recorded, never dropped.
func (b *bundleHandle) writeValidationReceipt(name string, receipt *core.ValidationReceipt) *core.RefusalError {
	if ref := b.unchanged(); ref != nil {
		return ref
	}
	raw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return failed(err)
	}
	if err := atomic.WriteFileIn(b.root, name, append(raw, '\n')); err != nil {
		return &core.RefusalError{Code: codeVerifyFailed, Message: fmt.Sprintf("could not write %s/%s: %v", b.dir, name, err)}
	}
	return nil
}

// ReadValidationReceipt loads one group receipt with strict capped parsing:
// an absent file reports found false, and malformed bytes refuse instead of
// degrading to a pass.
func ReadValidationReceipt(dir, groupID string) (core.ValidationReceipt, bool, *core.RefusalError) {
	path, ok := receiptPath(dir, groupID)
	if !ok {
		return core.ValidationReceipt{}, false, &core.RefusalError{
			Code:    codeBadGroupValidation,
			Message: fmt.Sprintf("group id %q cannot name a confined receipt file", groupID),
		}
	}
	if _, statErr := os.Stat(path); statErr != nil {
		if os.IsNotExist(statErr) {
			return core.ValidationReceipt{}, false, nil
		}
		return core.ValidationReceipt{}, false, failed(statErr)
	}
	raw, err := jq.CappedFile(path, receiptCap)
	if err != nil {
		return core.ValidationReceipt{}, false, failed(err)
	}
	var receipt core.ValidationReceipt
	if problem := decodeStrict(raw, &receipt); problem != "" {
		return core.ValidationReceipt{}, false, &core.RefusalError{
			Code:    codeVerifyFailed,
			Message: fmt.Sprintf("validation receipt %s is unreadable (%s); re-run the group validation", path, problem),
		}
	}
	if receipt.SchemaVersion != validationSchema {
		return core.ValidationReceipt{}, false, &core.RefusalError{
			Code:    codeVerifyFailed,
			Message: fmt.Sprintf("validation receipt %s has schema_version %q, want %s", path, receipt.SchemaVersion, validationSchema),
		}
	}
	return receipt, true, nil
}

// ReceiptCurrent reports whether the receipt certifies this exact run,
// group, and head: anything else is stale evidence, never a pass.
func ReceiptCurrent(r *core.ValidationReceipt, run, inputsSHA, group, head string) bool {
	// The execution facts are re-derived on read: a receipt claiming a pass
	// while recording a nonzero exit, a timeout, or a non-passing status is
	// contradictory evidence and never approves.
	return r.Passed && r.Status == statusPassed && r.Exit == 0 && !r.TimedOut &&
		r.ReviewedHead != "" && r.Tree != "" &&
		r.Run == run && r.InputsSHA256 == inputsSHA &&
		r.Group == group && r.ReviewedHead == head
}

// groupResponse renders the group-mode outcome; only a matching head and a
// zero exit may carry the success next step.
func (st *state) groupResponse(dir, receiptPath string, receipt *core.ValidationReceipt) *Response {
	next := "validation failed or unavailable — fix it, or report the failure; do not claim a pass"
	if receipt.Passed {
		next = "validation holds for this group and head — the receipt is recorded"
	}
	return &Response{
		Repo: st.req.Repo,
		Pr:   st.req.Pr,
		At:   receipt.At,
		Data: &core.VerifyData{
			Exit: receipt.Exit, TimedOut: receipt.TimedOut, OutputTail: receipt.OutputTail,
			Group: receipt.Group, Head: receipt.ReviewedHead, Tree: receipt.Tree, Receipt: receiptPath,
			Sandboxed: receipt.Sandboxed, SandboxMechanism: receipt.SandboxMechanism,
		},
		Warnings: st.warnings,
		Next:     next,
		Exit:     receipt.Exit,
		TimedOut: receipt.TimedOut,
		Tail:     receipt.OutputTail,
		PatchDir: dir,
	}
}

// DeepGaps describes each deep plan group's validation standing for a
// read-only surface (record/report): present, missing, failed, unavailable,
// timed out, stale head, or stale capture. It reads receipts and never
// executes anything, so a gap is visible evidence, not a manufactured pass.
func DeepGaps(rs *config.RunState, dir, head, root string) []string {
	if rs == nil || rs.Resolved == nil {
		return nil
	}
	canon, ref := canonicalRoot(root)
	if ref != nil {
		return []string{"deep validation: " + ref.Message}
	}
	gaps := make([]string, 0, len(rs.Resolved.Plan.Groups))
	for i := range rs.Resolved.Plan.Groups {
		if line, ok := deepGap(rs, dir, canon, head, &rs.Resolved.Plan.Groups[i]); ok {
			gaps = append(gaps, line)
		}
	}
	return gaps
}

// deepGap classifies one deep group as a visible gap, reporting false when a
// current passing receipt covers it.
func deepGap(rs *config.RunState, dir, root, head string, group *config.PlanGroup) (string, bool) {
	if group.Depth != "deep" {
		return "", false
	}
	receipt, found, ref := ReadValidationReceipt(dir, group.ID)
	state := validationState(&receipt, found, ref, rs, group.ID, head, root)
	if state == statusPassed {
		return "", false
	}
	return "deep validation " + state + " for " + groupDesc(group) + ": " +
		validationGapDetail(state, &receipt, ref, group.ID), true
}

// groupDesc names a group in human lines without trusting its label as an id.
func groupDesc(group *config.PlanGroup) string {
	if group.Label == "" {
		return group.ID
	}
	return group.Label + " (id " + group.ID + ")"
}

// validationState classifies one group receipt for read-only surfaces without
// deciding approval: pending (no receipt), unreadable, stale (another head,
// run, capture, or root), contradictory (it says passed but ReceiptCurrent
// rejects its evidence: a failing exit, a timeout, no tree), its recorded
// unavailable, timeout, or failed status, or passed (a current successful
// receipt).
func validationState(r *core.ValidationReceipt, found bool, ref *core.RefusalError, rs *config.RunState, group, head, root string) string {
	switch {
	case ref != nil:
		return "unreadable"
	case !found:
		return "pending"
	case ReceiptCurrent(r, rs.Name, rs.InputsSHA256, group, head) && r.RepoRoot == root:
		return statusPassed
	case r.ReviewedHead != head:
		return "stale"
	case r.Run != rs.Name || r.InputsSHA256 != rs.InputsSHA256 || r.RepoRoot != root:
		return "stale"
	case r.Status == statusPassed:
		// Only ReceiptCurrent may call a receipt passed.
		return "contradictory"
	}
	return r.Status
}

// validationGapDetail explains one classified gap and the step that fixes it.
func validationGapDetail(state string, r *core.ValidationReceipt, ref *core.RefusalError, group string) string {
	switch state {
	case "unreadable":
		return "receipt unreadable — " + ref.Message
	case "pending":
		return "no validation receipt — run verify --group " + group + " --command <cmd> at the reviewed head"
	}
	return "receipt records " + r.Status + " for head " + readcommon.ShortSHA(r.ReviewedHead) +
		"; not current evidence — re-run verify --group " + group + " --command <cmd>"
}
