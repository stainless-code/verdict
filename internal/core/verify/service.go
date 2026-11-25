package verifycore

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/envsecret"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/noblock"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	codeMissingPatch = "missing-patch"
	codeNoVerifyCmd  = "no-verify-cmd"
	codeBadTimeout   = "bad-timeout"
	codeVerifyFailed = "verify-failed"
	codeCanceled     = "canceled"
	msgCanceled      = "canceled before verify completed"
	patchFile        = "patch.json"
	noVerifyCmdMsg   = "patch.json carries no verifyCmd to execute"
	msgVerifyFailed  = "verify failed: %v"
)

func patchPath(dir string) string {
	return dir + "/" + patchFile
}

func missingPatchRefusal(dir string) *core.RefusalError {
	return &core.RefusalError{
		Code:    codeMissingPatch,
		Message: fmt.Sprintf("missing %s/patch.json (nothing claimed to verify)", dir),
	}
}

func corruptStoreRefusal(dir string) *core.RefusalError {
	return &core.RefusalError{
		Code:    "corrupt-store",
		Message: fmt.Sprintf("patch store %s/patch.json is unreadable (restore it from backup, then re-run)", dir),
	}
}

// preflightRefusal rejects the request shapes that outrank every gate: a
// canceled context and the mutually exclusive sandbox flags. Checking the
// flags here keeps bad-flags dominant over a later verify-destructive or
// bundle refusal.
func preflightRefusal(ctx context.Context, req *Request) *core.RefusalError {
	if ref := sandboxFlagsRefusal(req); ref != nil {
		return ref
	}
	if err := ctx.Err(); err != nil {
		return &core.RefusalError{Code: codeCanceled, Message: msgCanceled}
	}
	return nil
}

// deepGroup reports whether the request asks for deep group validation, which
// takes the group path instead of the ordinary patch path.
func deepGroup(req *Request) bool {
	return req.Group != "" || req.Command != ""
}

// Run executes the verify verb: bundle gates, the allowlist gate, the
// watchdog spawn, and the patch record. It never writes stdout/stderr or
// exits; human lines stream through the runtime progress observer, stores
// go through the shared atomic path, and the envelope inputs come back in
// the response for the adapter to render.
func Run(ctx context.Context, req *Request) (*Response, *core.RefusalError) {
	st := &state{req: req, warnings: append([]string{}, req.Warnings...)}
	if ref := preflightRefusal(ctx, req); ref != nil {
		return nil, st.withWarnings(ref)
	}
	// verify writes into the bundle, so it resolves it like every other
	// writing verb: a symlinked bundle directory refuses instead of steering
	// the receipt outside the repository.
	dir, ref := readcommon.ResolveWritableBundleDir(req.RepoRootPath(), req.Pr)
	if ref != nil {
		return nil, st.withWarnings(ref)
	}
	if deepGroup(req) {
		return RunGroup(ctx, req, dir, req.TimeUTC())
	}
	if !readcommon.IsFile(patchPath(dir)) {
		return nil, st.withWarnings(missingPatchRefusal(dir))
	}
	now := req.TimeUTC()
	vcmd, ref := readVerifyCmd(dir)
	if ref != nil {
		return nil, st.withWarnings(ref)
	}
	timeout, ok := parseTimeout(req.TimeoutRaw)
	if !ok {
		return nil, st.withWarnings(&core.RefusalError{Code: codeBadTimeout, Message: fmt.Sprintf(
			"timeout must be a positive number of seconds, got '%s'", req.TimeoutRaw)})
	}
	if ref := Check(vcmd); ref != nil {
		return nil, st.withWarnings(ref)
	}
	return st.execute(ctx, dir, now, vcmd, timeout)
}

type state struct {
	req      *Request
	warnings []string
	env      []string
}

func (st *state) withWarnings(ref *core.RefusalError) *core.RefusalError {
	ref.Warnings = st.warnings
	return ref
}

func (st *state) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	if st.req.Runtime != nil {
		st.req.Warn(msg)
	}
}

func (st *state) human(line string) {
	if st.req.Runtime != nil {
		st.req.Human(line)
	}
}

func (st *state) runtime() *core.Runtime {
	return st.req.Runtime
}

func (st *state) execute(ctx context.Context, dir, now, vcmd string, timeout int) (*Response, *core.RefusalError) {
	commander := st.commander()
	if commander == nil {
		return nil, st.withWarnings(&core.RefusalError{
			Code:    codeVerifyFailed,
			Message: "verify failed: no commander in runtime",
		})
	}
	if ref := st.prepareEnv(dir, readcommon.BundleBase(dir, nil)); ref != nil {
		return nil, st.withWarnings(ref)
	}
	if !coreexec.WatchdogEnforced() {
		st.warn("no ps(1) — timeout unenforced for this run")
	}
	// The command runs where the tree is probed, the repo root, as the deep
	// group path does: a receipt's head must describe the tree the command saw.
	root := st.req.RepoRootPath()
	sb, ref := st.sandboxed(ctx, commander, root, root)
	if ref != nil {
		return nil, st.withWarnings(ref)
	}
	// The bundle is pinned before the command can touch it: every write goes
	// through this handle, the finding sig is read now, and the previous
	// receipt is gone before the command starts, so neither a swapped bundle
	// directory, an edited patch.json, nor an aborted run can leave a pass.
	bundle, ref := pinBundle(dir)
	if ref != nil {
		st.releaseSandbox(sb)
		return nil, st.withWarnings(ref)
	}
	defer bundle.close()
	receipt := &core.VerifyReceipt{SchemaVersion: receiptSchema, Command: vcmd, Sig: bundle.sig, At: now}
	before := probeTree(ctx, commander, root)
	st.human(fmt.Sprintf("» verify: %s (timeout %ss)", vcmd, st.req.TimeoutRaw))
	res, ref := runCommand(ctx, commander, vcmd, timeout, root, st.childEnv(), sb)
	st.releaseSandbox(sb)
	if ctx.Err() != nil {
		return nil, st.withWarnings(&core.RefusalError{Code: codeCanceled, Message: msgCanceled})
	}
	if ref != nil {
		return nil, st.withWarnings(ref)
	}
	receipt.Head = st.boundHead(root, before, probeTree(ctx, commander, root))
	receipt.Exit, receipt.TimedOut = res.ExitCode, res.TimedOut
	return st.record(bundle, now, res, sb, receipt)
}

func (st *state) commander() core.Commander {
	var commander core.Commander
	if rt := st.runtime(); rt != nil {
		commander = rt.Commander
	}
	return commander
}

// sandboxed prepares the opt-in wrapper, or returns nil for an unwrapped run.
// The full flag and the network-only opt-in are exclusive, and only the
// network-only path carries the no-mount warning the caller must see.
func (st *state) sandboxed(ctx context.Context, commander core.Commander, workDir, root string) (*sandbox, *core.RefusalError) {
	if ref := sandboxFlagsRefusal(st.req); ref != nil {
		return nil, ref
	}
	if !st.req.Sandbox && !st.req.SandboxNetworkOnly {
		return nil, nil
	}
	sb, ref := prepareSandbox(ctx, commander, workDir, root, st.sandboxInputs(), st.req.SandboxNetworkOnly)
	if ref != nil {
		st.releaseSandbox(sb)
		return nil, ref
	}
	if sb.networkOnly {
		st.human("» sandbox: network-only (unshare --net) — no mount, filesystem, or PID isolation")
		st.warn(msgSandboxNetworkOnly)
	}
	return sb, nil
}

// sandboxInputs is what the credential table resolves against: the child
// environment, the snapshotted verdict environment (a relocation variable the
// allowlist drops still names a store on disk), and the account home.
func (st *state) sandboxInputs() sandboxInputs {
	account := st.req.accountHome
	if account == "" {
		account = sandboxAccountHome()
	}
	return sandboxInputs{childEnv: st.childEnv(), hostEnv: st.req.Environ, accountHome: account}
}

// releaseSandbox removes the mechanism's private state as soon as the command
// is done, and warns when any of it survived, so a stranded directory is
// visible in the same response instead of silently left behind.
func (st *state) releaseSandbox(sb *sandbox) {
	if err := sb.close(); err != nil {
		st.warn("sandbox cleanup: the private directory could not be fully removed (" + err.Error() +
			"); remove it by hand")
	}
}

// sandboxFlagsRefusal keeps the two sandbox opt-ins mutually exclusive: the
// caller states the exact boundary they accept instead of one flag silently
// winning over the other.
func sandboxFlagsRefusal(req *Request) *core.RefusalError {
	if !req.Sandbox || !req.SandboxNetworkOnly {
		return nil
	}
	return &core.RefusalError{Code: codeBadFlags, Message: "--sandbox and --sandbox-network-only are mutually exclusive: " +
		"--sandbox enforces the full bwrap/sandbox-exec shape, --sandbox-network-only accepts network isolation alone; pass one"}
}

func runCommand(ctx context.Context, commander core.Commander, vcmd string, timeout int,
	dir string, env []string, sb *sandbox,
) (core.Result, *core.RefusalError) {
	argv := []string{"bash", "-c", vcmd}
	if sb != nil {
		argv = sb.argv(vcmd)
		env = sb.childEnv(env)
	}
	tctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	res, err := commander.Run(tctx, &core.Command{
		Argv:      argv,
		Env:       env,
		Dir:       dir,
		OutputCap: coreexec.VerifyOutputCap,
	})
	if err != nil {
		return core.Result{}, failed(err)
	}
	return res, nil
}

func failed(err error) *core.RefusalError {
	return &core.RefusalError{Code: codeVerifyFailed, Message: fmt.Sprintf(msgVerifyFailed, err)}
}

func (st *state) record(bundle *bundleHandle, now string, res core.Result, sb *sandbox,
	receipt *core.VerifyReceipt,
) (*Response, *core.RefusalError) {
	dir := bundle.dir
	combined := res.Stdout + res.Stderr
	// Sanitize the whole capture before cutting: a cut first could leave the
	// unrecognizable second half of a credential at the tail's left edge.
	tail := coreexec.TrimTail(lastBytes(sanitizeTail(combined, st.req.Environ), tailCap))
	st.human(fmt.Sprintf("» exit=%d timed_out=%s", res.ExitCode, strconv.FormatBool(res.TimedOut)))
	if ref := bundle.unchanged(); ref != nil {
		return nil, st.withWarnings(ref)
	}
	st.human(fmt.Sprintf("recorded into %s/patch.json", dir))
	if err := atomic.WriteFileModeIn(bundle.root, verifyOutName, []byte(combined), verifyOutputMode); err != nil {
		return nil, st.withWarnings(failed(err))
	}
	if ref := recordTimeoutMarker(bundle, timeoutMarker(st.req.TimeoutRaw, res.TimedOut, res.Decoy)); ref != nil {
		return nil, st.withWarnings(ref)
	}
	if ref := recordOutcome(bundle, now, res.ExitCode, res.TimedOut, tail, sb); ref != nil {
		return nil, st.withWarnings(ref)
	}
	if ref := bundle.writeReceipt(receipt); ref != nil {
		return nil, st.withWarnings(ref)
	}
	st.human(fmt.Sprintf("receipt: %s/%s", dir, ReceiptFile))
	resp := st.response(dir, now, res.ExitCode, res.TimedOut, tail, sb)
	resp.Data.Head, resp.Data.Receipt = receipt.Head, dir+"/"+ReceiptFile
	return resp, nil
}

func (st *state) response(dir, now string, exit int, timedOut bool, tail string, sb *sandbox) *Response {
	next := "proof failed — fix, do not claim"
	if exit == 0 && !timedOut {
		next = "proof holds — revalidator may VERIFY"
	}
	data := &core.VerifyData{
		Exit: exit, TimedOut: timedOut, OutputTail: tail, Patch: dir,
		Sandboxed: sb.full(), SandboxMechanism: sb.name(),
	}
	return &Response{
		Repo:     st.req.Repo,
		Pr:       st.req.Pr,
		At:       now,
		Data:     data,
		Warnings: st.warnings,
		Next:     next,
		Exit:     exit,
		TimedOut: timedOut,
		Tail:     tail,
		PatchDir: dir,
	}
}

func (st *state) childEnv() []string {
	return st.env
}

// readVerifyCmd reads patch.json's verifyCmd with `jq -r` semantics: a
// missing or null verifyCmd reads empty (refusing no-verify-cmd, the
// fallback a corrupt store gets); any other
// non-string renders the way jq -r would print it.
func readVerifyCmd(dir string) (string, *core.RefusalError) {
	raw, err := noblock.ReadFile(patchPath(dir))
	if err != nil {
		return "", missingPatchRefusal(dir)
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return "", &core.RefusalError{Code: codeNoVerifyCmd, Message: noVerifyCmdMsg}
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return "", &core.RefusalError{Code: codeNoVerifyCmd, Message: noVerifyCmdMsg}
	}
	vcmdRaw, ok := m["verifyCmd"]
	if !ok || vcmdRaw == nil {
		return "", &core.RefusalError{Code: codeNoVerifyCmd, Message: noVerifyCmdMsg}
	}
	vcmd := jq.R(vcmdRaw)
	if vcmd == "" {
		return "", &core.RefusalError{Code: codeNoVerifyCmd, Message: noVerifyCmdMsg}
	}
	return vcmd, nil
}

func parseTimeout(s string) (int, bool) {
	if !jq.Digits(s) {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// recordOutcome merges the watchdog result into patch.json, preserving key
// order (existing keys keep position, new keys append).
func recordOutcome(bundle *bundleHandle, now string, exit int, timedOut bool, tail string, sb *sandbox) *core.RefusalError {
	dir := bundle.dir
	raw, err := bundle.read(patchFile)
	if err != nil {
		return missingPatchRefusal(dir)
	}
	doc, err := jq.DecodeOrdered(raw)
	if err != nil {
		return corruptStoreRefusal(dir)
	}
	m, ok := doc.(*jq.Obj)
	if !ok {
		return corruptStoreRefusal(dir)
	}
	m.Set("verifyExit", exit)
	m.Set("verifiedAt", now)
	m.Set("timedOut", timedOut)
	m.Set("verifiedTail", tail)
	// Both keys describe this run alone: `sandboxed` only for the full shape,
	// the mechanism for any wrapper, and neither survives from an earlier run.
	if sb.full() {
		m.Set("sandboxed", true)
	} else {
		m.Delete("sandboxed")
	}
	if name := sb.name(); name != "" {
		m.Set("sandboxMechanism", name)
	} else {
		m.Delete("sandboxMechanism")
	}
	if err := atomic.WriteFileIn(bundle.root, patchFile, []byte(m.MarshalPretty()+"\n")); err != nil {
		return &core.RefusalError{Code: codeVerifyFailed, Message: fmt.Sprintf("could not write %s/patch.json: %v", dir, err)}
	}
	return nil
}

// raw output can carry a printed credential, so it is owner-only like .run.out.
const verifyOutputMode = 0o600

// recordTimeoutMarker writes the run's timeout marker, emptying it after a
// run that did not time out.
func recordTimeoutMarker(bundle *bundleHandle, marker string) *core.RefusalError {
	if err := atomic.WriteFileIn(bundle.root, verifyTimeoutName, []byte(marker)); err != nil {
		return failed(err)
	}
	return nil
}

func timeoutMarker(timeoutS string, timedOut, decoy bool) string {
	if !timedOut {
		return ""
	}
	if decoy {
		return orphanMarker
	}
	return timeoutLine(timeoutS)
}

func timeoutLine(timeoutS string) string {
	return "timeout after " + timeoutS + "s\n"
}

const orphanMarker = "orphaned command tree killed after exit\n"

func lastBytes(s string, n int) string {
	if len(s) > n {
		s = s[len(s)-n:]
	}
	return s
}

// redactSecrets returns the recorded tail with every secret-named environment
// variable's value — when 8+ chars — replaced literally. The full .verify.out
// keeps raw output for operator debugging; only the tail is filtered.
//
// The matching is [envsecret.ScrubValues], shared with the launcher's tail
// scrub so two overlapping credentials are hidden in one pass rather than
// leaking whichever tail survived the first replacement. What lives here is
// only the policy: environment values under a secret-shaped name, and the
// 8-character floor that keeps a short value from shredding the tail.
func redactSecrets(tail string, environ []string) string {
	return envsecret.ScrubValues(tail, secretValues(environ))
}

// secretValues collects the environment values the redactor hides: the value
// of every secret-shaped variable that is long enough to be a credential. The
// tail is matched after its control characters are stripped, so a value that
// itself carries one (a token read from a CRLF file keeps its \r) is listed in
// its stripped form too.
func secretValues(environ []string) []string {
	var values []string
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || len(value) < secretMinLen || !envsecret.IsSecret(name) {
			continue
		}
		values = withStripped(values, value)
	}
	return values
}

// withStripped appends value and, when stripping control characters changes
// it and leaves a credential-length remainder, the stripped form as well.
func withStripped(values []string, value string) []string {
	values = append(values, value)
	if clean := stripControlChars(value); clean != value && len(clean) >= secretMinLen {
		values = append(values, clean)
	}
	return values
}

// StripControlledSecrets returns secrets plus the control-stripped form of
// each one that a control character changes, for matching text that already
// went through [SanitizeTail].
func StripControlledSecrets(secrets []string) []string {
	var out []string
	for _, v := range secrets {
		out = withStripped(out, v)
	}
	return out
}

const secretMinLen = 8

const redacted = envsecret.Redacted

// tailCap is the recorded tail size (tail -c 4000).
const tailCap = 4000
