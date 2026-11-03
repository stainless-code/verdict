package verifycore

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/bundle"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/envsecret"
	"github.com/stainless-code/verdict/internal/jq"
)

// Refusal codes and store names for the verify verb.
const (
	codeMissingPatch = "missing-patch"
	codeNoVerifyCmd  = "no-verify-cmd"
	codeBadTimeout   = "bad-timeout"
	codeVerifyFailed = "verify-failed"
	codeCanceled     = "canceled"
	patchFile        = "patch.json"
	noVerifyCmdMsg   = "patch.json carries no verifyCmd to execute"
	// msgVerifyFailed formats a runtime failure for the refusal message.
	msgVerifyFailed = "verify failed: %v"
)

// patchPath joins the bundle dir and the patch store name.
func patchPath(dir string) string {
	return dir + "/" + patchFile
}

// missingPatchRefusal builds the missing-store refusal for dir.
func missingPatchRefusal(dir string) *core.RefusalError {
	return &core.RefusalError{
		Code:    codeMissingPatch,
		Message: fmt.Sprintf("missing %s/patch.json (nothing claimed to verify)", dir),
	}
}

// corruptStoreRefusal builds the unreadable-store refusal for dir.
func corruptStoreRefusal(dir string) *core.RefusalError {
	return &core.RefusalError{
		Code:    "corrupt-store",
		Message: fmt.Sprintf("patch store %s/patch.json is unreadable (restore it from backup, then re-run)", dir),
	}
}

// Run executes the verify verb: bundle gates, the allowlist gate, the
// watchdog spawn, and the patch record. It never writes stdout/stderr or
// exits; human lines stream through the runtime progress observer, stores
// go through the shared atomic path, and the envelope inputs come back in
// the response for the adapter to render.
func Run(ctx context.Context, req *core.VerifyRequest) (*core.VerifyResponse, *core.RefusalError) {
	st := &state{req: req, warnings: append([]string{}, req.Warnings...)}
	if err := ctx.Err(); err != nil {
		return nil, &core.RefusalError{Code: codeCanceled, Message: "canceled before verify completed"}
	}
	dir := bundle.Dir(req.Pr)
	if !bundle.IsFile(patchPath(dir)) {
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

// state carries one verify run: the request plus the warnings accumulated
// before the response or refusal.
type state struct {
	req      *core.VerifyRequest
	warnings []string
}

// withWarnings attaches the accumulated warnings to a refusal so the
// adapter's envelope stays byte-identical to a success run's warning list.
func (st *state) withWarnings(ref *core.RefusalError) *core.RefusalError {
	ref.Warnings = st.warnings
	return ref
}

// warn records and streams one warning.
func (st *state) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	if st.req.Runtime != nil {
		st.req.Warn(msg)
	}
}

// human streams one human output line.
func (st *state) human(line string) {
	if st.req.Runtime != nil {
		st.req.Human(line)
	}
}

// runtime returns the injected runtime (nil when the caller set none).
func (st *state) runtime() *core.Runtime {
	return st.req.Runtime
}

// execute runs the watchdog spawn and records the patch outcome.
func (st *state) execute(ctx context.Context, dir, now, vcmd string, timeout int) (*core.VerifyResponse, *core.RefusalError) {
	commander := st.commander()
	if commander == nil {
		return nil, st.withWarnings(&core.RefusalError{
			Code:    codeVerifyFailed,
			Message: "verify failed: no commander in runtime",
		})
	}
	if !coreexec.WatchdogEnforced() {
		st.warn("no ps(1) — timeout unenforced for this run")
	}
	outPath, timeoutPath := dir+"/.verify.out", dir+"/.verify.timeout"
	if err := truncateOutputs(outPath, timeoutPath); err != nil {
		return nil, st.withWarnings(failed(err))
	}
	st.human(fmt.Sprintf("» verify: %s (timeout %ss)", vcmd, st.req.TimeoutRaw))
	res, ref := runCommand(ctx, commander, vcmd, timeout, st.childEnv())
	if ref != nil {
		return nil, st.withWarnings(ref)
	}
	return st.record(dir, now, outPath, timeoutPath, res)
}

// commander returns the injected commander (nil when the caller set none).
func (st *state) commander() core.Commander {
	var commander core.Commander
	if rt := st.runtime(); rt != nil {
		commander = rt.Commander
	}
	return commander
}

// runCommand spawns one command under the watchdog timeout. The capture cap
// is explicit: verify records the proof tail, and a noisy command must not
// grow the process without bound.
func runCommand(ctx context.Context, commander core.Commander, vcmd string, timeout int, env []string) (core.Result, *core.RefusalError) {
	tctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	res, err := commander.Run(tctx, core.Command{
		Argv:      []string{"bash", "-c", vcmd},
		Env:       env,
		OutputCap: coreexec.VerifyOutputCap,
	})
	if err != nil {
		return core.Result{}, failed(err)
	}
	return res, nil
}

// failed wraps a spawn/write failure for the edge to render.
func failed(err error) *core.RefusalError {
	return &core.RefusalError{Code: codeVerifyFailed, Message: fmt.Sprintf(msgVerifyFailed, err)}
}

// record writes the patch outcome and builds the typed success response.
func (st *state) record(dir, now, outPath, timeoutPath string, res core.Result) (*core.VerifyResponse, *core.RefusalError) {
	combined := res.Stdout + res.Stderr
	tail := sanitizeTail(coreexec.TrimTail(lastBytes(combined, tailCap)), st.req.Environ)
	st.human(fmt.Sprintf("» exit=%d timed_out=%s", res.ExitCode, strconv.FormatBool(res.TimedOut)))
	st.human(fmt.Sprintf("recorded into %s/patch.json", dir))
	if err := writeOutputs(outPath, combined); err != nil {
		return nil, st.withWarnings(failed(err))
	}
	if ref := recordTimeoutMarker(timeoutPath, timeoutMarker(st.req.TimeoutRaw, res.TimedOut, res.Decoy)); ref != nil {
		return nil, st.withWarnings(ref)
	}
	if ref := recordOutcome(dir, now, res.ExitCode, res.TimedOut, tail); ref != nil {
		return nil, st.withWarnings(ref)
	}
	return st.response(dir, now, res.ExitCode, res.TimedOut, tail), nil
}

// response builds the typed success response.
func (st *state) response(dir, now string, exit int, timedOut bool, tail string) *core.VerifyResponse {
	next := "proof failed — fix, do not claim"
	if exit == 0 && !timedOut {
		next = "proof holds — revalidator may VERIFY"
	}
	return &core.VerifyResponse{
		Repo:     st.req.Repo,
		Pr:       st.req.Pr,
		At:       now,
		Data:     &core.VerifyData{Exit: exit, TimedOut: timedOut, OutputTail: tail, Patch: dir},
		Warnings: st.warnings,
		Next:     next,
		Exit:     exit,
		TimedOut: timedOut,
		Tail:     tail,
		PatchDir: dir,
	}
}

// childEnv builds the spawn environment from the ordered snapshot plus the
// NO_COLOR default in JSON mode (mirroring json_redirect's export), so the
// verify child inherits the convention.
func (st *state) childEnv() []string {
	env := st.req.Environ
	if !st.req.JSONMode {
		return env
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "NO_COLOR=") {
			return env
		}
	}
	return append(env, "NO_COLOR=1")
}

// readVerifyCmd reads patch.json's verifyCmd with `jq -r` semantics: a
// missing or null verifyCmd reads empty (refusing no-verify-cmd, exactly
// like the shell's `// empty` fallback on corrupt stores per R1); any other
// non-string renders the way jq -r would print it.
func readVerifyCmd(dir string) (string, *core.RefusalError) {
	raw, err := os.ReadFile(patchPath(dir))
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

// parseTimeout ports the timeout gate: positive integers only, refusing an
// overflow instead of wrapping into a negative budget.
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

// recordOutcome merges the watchdog result into patch.json, preserving the
// shell's key order (existing keys keep position, new keys append).
func recordOutcome(dir, now string, exit int, timedOut bool, tail string) *core.RefusalError {
	raw, err := os.ReadFile(patchPath(dir))
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
	if err := atomic.WriteFile(dir+"/"+patchFile, []byte(m.MarshalPretty()+"\n")); err != nil {
		return &core.RefusalError{Code: codeVerifyFailed, Message: fmt.Sprintf("could not write %s/patch.json: %v", dir, err)}
	}
	return nil
}

// truncateOutputs empties the watchdog outputs before a run through the
// shared atomic path, mirroring the shell's `: >` truncations.
func truncateOutputs(paths ...string) error {
	for _, p := range paths {
		if err := atomic.WriteFile(p, nil); err != nil {
			return err
		}
	}
	return nil
}

// writeOutputs persists the combined command output for operator debugging
// through the shared atomic path, so an interrupted write never leaves a
// torn record.
func writeOutputs(outPath, combined string) error {
	return atomic.WriteFile(outPath, []byte(combined))
}

// recordTimeoutMarker writes the timeout marker for a timed-out run through
// the shared atomic path (the clean path leaves the truncated file empty,
// like the shell).
func recordTimeoutMarker(timeoutPath, marker string) *core.RefusalError {
	if marker == "" {
		return nil
	}
	if err := atomic.WriteFile(timeoutPath, []byte(marker)); err != nil {
		return &core.RefusalError{Code: codeVerifyFailed, Message: fmt.Sprintf(msgVerifyFailed, err)}
	}
	return nil
}

// timeoutMarker renders the timeout marker: empty for a clean run, the
// decoy marker for a sweep-fired run, the seconds line for a budget
// timeout.
func timeoutMarker(timeoutS string, timedOut, decoy bool) string {
	if !timedOut {
		return ""
	}
	if decoy {
		return orphanMarker
	}
	return timeoutLine(timeoutS)
}

// timeoutLine renders the timeout marker.
func timeoutLine(timeoutS string) string {
	return "timeout after " + timeoutS + "s\n"
}

// orphanMarker records a decoy exit: the parent finished while its tree
// outlived it, so the proof is refused.
const orphanMarker = "orphaned command tree killed after exit\n"

// lastBytes reads the last n bytes of a string (the `tail -c` shape).
func lastBytes(s string, n int) string {
	if len(s) > n {
		s = s[len(s)-n:]
	}
	return s
}

// redactSecrets ports the known-secret redaction of the recorded tail:
// every snapshot variable whose name matches the secret suffix list
// (case-insensitive, trailing rotation digits/underscores stripped) has
// its value — when 8+ chars — replaced literally. The full .verify.out
// keeps raw output for operator debugging; only the tail is filtered.
func redactSecrets(tail string, environ []string) string {
	for _, name := range secretNames(environ) {
		for _, kv := range environ {
			if k, v, ok := strings.Cut(kv, "="); ok && k == name && len(v) >= secretMinLen {
				tail = strings.ReplaceAll(tail, v, redacted)
				break
			}
		}
	}
	return tail
}

// secretNames lists environment names matching the secret suffix list in
// alphabetical order.
func secretNames(environ []string) []string {
	var out []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if isSecretName(name) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// isSecretName ports the nocasematch suffix match with trailing
// rotation-digit/underscore stripping. The classifier lives in envsecret so
// the test harnesses drop the same names this redactor rewrites.
func isSecretName(name string) bool {
	return envsecret.IsSecret(name)
}

// secretMinLen skips empty and short (<8 char) values, like the shell.
const secretMinLen = 8

// redacted is the tail replacement marker.
const redacted = "***REDACTED***"

// tailCap is the recorded tail size (tail -c 4000).
const tailCap = 4000
