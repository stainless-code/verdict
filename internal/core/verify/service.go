package verifycore

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/envsecret"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	codeMissingPatch = "missing-patch"
	codeNoVerifyCmd  = "no-verify-cmd"
	codeBadTimeout   = "bad-timeout"
	codeVerifyFailed = "verify-failed"
	codeCanceled     = "canceled"
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
	dir := readcommon.ResolveBundleDir(req.RepoRootPath(), req.Pr)
	if req.Group != "" || req.Command != "" {
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
	req      *core.VerifyRequest
	warnings []string
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
	st.human(fmt.Sprintf("» verify: %s (timeout %ss)", vcmd, st.req.TimeoutRaw))
	res, ref := runCommand(ctx, commander, vcmd, timeout, "", st.childEnv())
	if ctx.Err() != nil {
		return nil, st.withWarnings(&core.RefusalError{Code: codeCanceled, Message: "canceled before verify completed"})
	}
	if ref != nil {
		return nil, st.withWarnings(ref)
	}
	return st.record(dir, now, outPath, timeoutPath, res)
}

func (st *state) commander() core.Commander {
	var commander core.Commander
	if rt := st.runtime(); rt != nil {
		commander = rt.Commander
	}
	return commander
}

func runCommand(ctx context.Context, commander core.Commander, vcmd string, timeout int,
	dir string, env []string,
) (core.Result, *core.RefusalError) {
	tctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	res, err := commander.Run(tctx, &core.Command{
		Argv:      []string{"bash", "-c", vcmd},
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
// missing or null verifyCmd reads empty (refusing no-verify-cmd, the
// fallback a corrupt store gets); any other
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

func writeOutputs(outPath, combined string) error {
	return atomic.WriteFile(outPath, []byte(combined))
}

func recordTimeoutMarker(timeoutPath, marker string) *core.RefusalError {
	if err := atomic.WriteFile(timeoutPath, []byte(marker)); err != nil {
		return &core.RefusalError{Code: codeVerifyFailed, Message: fmt.Sprintf(msgVerifyFailed, err)}
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
// of every secret-shaped variable that is long enough to be a credential.
func secretValues(environ []string) []string {
	var values []string
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || len(value) < secretMinLen || !envsecret.IsSecret(name) {
			continue
		}
		values = append(values, value)
	}
	return values
}

const secretMinLen = 8

const redacted = envsecret.Redacted

// tailCap is the recorded tail size (tail -c 4000).
const tailCap = 4000
