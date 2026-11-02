package verify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/bundle"
	"github.com/stainless-code/verdict/internal/cli"
	"github.com/stainless-code/verdict/internal/jq"
)

// Refusal codes and store names for the verify verb.
const (
	codeMissingPatch = "missing-patch"
	codeNoVerifyCmd  = "no-verify-cmd"
	codeBadTimeout   = "bad-timeout"
	patchFile        = "patch.json"
	noVerifyCmdMsg   = "patch.json carries no verifyCmd to execute"
)

// patchPath joins the bundle dir and the patch store name.
func patchPath(dir string) string {
	return dir + "/" + patchFile
}

// missingPatchRefusal builds the missing-store refusal for dir.
func missingPatchRefusal(dir string) *cli.RefusalError {
	return &cli.RefusalError{
		Code:    codeMissingPatch,
		Message: fmt.Sprintf("missing %s/patch.json (nothing claimed to verify)", dir),
	}
}

// corruptStoreRefusal builds the unreadable-store refusal for dir.
func corruptStoreRefusal(dir string) *cli.RefusalError {
	return &cli.RefusalError{
		Code:    "corrupt-store",
		Message: fmt.Sprintf("patch store %s/patch.json is unreadable (restore it from backup, then re-run)", dir),
	}
}

// Request carries the resolved verify invocation.
type Request struct {
	Env  map[string]string
	Repo string
	Pr   string
	Root string
}

// Run executes `verify <owner/repo> <slug> [--timeout <secs>] [--json]`,
// returning the process exit code.
func Run(ctx context.Context, r *cli.Runner, args []string, req Request) int {
	r.Repo, r.Pr = req.Repo, req.Pr
	r.Rentry = ""
	timeoutS, code, done := parseFlags(r, args)
	if done {
		return code
	}
	if err := ctx.Err(); err != nil {
		return r.RefuseExit(&cli.RefusalError{Code: "canceled", Message: "canceled before verify completed"}, cli.ExitBroke)
	}
	if err := runVerify(ctx, r, req, timeoutS); err != nil {
		if ref, ok := errors.AsType[*cli.RefusalError](err); ok {
			return r.Refuse(ref)
		}
		return r.Refuse(&cli.RefusalError{Code: "verify-failed", Message: fmt.Sprintf("verify failed: %v", err)})
	}
	return 0
}

// defaultTimeoutS is the watchdog budget when --timeout is absent.
const defaultTimeoutS = "300"

// parseFlags strips verify's flags, reporting unknown ones.
func parseFlags(r *cli.Runner, args []string) (string, int, bool) {
	timeoutS := defaultTimeoutS
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--timeout":
			timeoutS, i = parseTimeoutFlag(args, i)
		case "--json":
		default:
			return "", r.ReportUsage(&cli.UsageError{Message: fmt.Sprintf(
				"unknown flag '%s' for verify (want --timeout <secs>, --json)", a)}), true
		}
	}
	return timeoutS, 0, false
}

// parseTimeoutFlag reads the --timeout value at args[i], returning the
// value and the index of the last consumed argument.
func parseTimeoutFlag(args []string, i int) (string, int) {
	if i+1 >= len(args) {
		return defaultTimeoutS, i
	}
	// Mirror ${2:-300}: an explicit empty value falls back
	// to the default rather than refusing.
	if v := args[i+1]; v != "" {
		return v, i + 1
	}
	return defaultTimeoutS, i + 1
}

// runVerify mirrors the verify block: bundle gates, the allowlist gate,
// the watchdog spawn, and the patch record.
func runVerify(ctx context.Context, r *cli.Runner, req Request, timeoutS string) error {
	dir := bundle.Dir(req.Pr)
	if !bundle.IsFile(patchPath(dir)) {
		return missingPatchRefusal(dir)
	}
	now := cli.NowUTC(r.Now())
	vcmd, err := readVerifyCmd(dir)
	if err != nil {
		return err
	}
	timeout, ok := parseTimeout(timeoutS)
	if !ok {
		return &cli.RefusalError{Code: codeBadTimeout, Message: fmt.Sprintf(
			"timeout must be a positive number of seconds, got '%s'", timeoutS)}
	}
	if ref := Check(vcmd); ref != nil {
		return ref
	}
	fmt.Fprintf(r.Human, "» verify: %s (timeout %ss)\n", vcmd, timeoutS)
	result, err := execute(ctx, dir, vcmd, timeout, r.Warn, childEnv(r))
	if err != nil {
		return err
	}
	fmt.Fprintf(r.Human, "» exit=%d timed_out=%s\n", result.exit, boolStr(result.timedOut))
	fmt.Fprintf(r.Human, "recorded into %s/patch.json\n", dir)
	if err := recordOutcome(dir, now, result); err != nil {
		return err
	}
	if r.JSONMode {
		emitVerifyEnvelope(r, now, result, dir)
	}
	return nil
}

// emitVerifyEnvelope prints the one-doc --json envelope for a recorded
// proof: null error with the recorded exit, or the failure next step.
func emitVerifyEnvelope(r *cli.Runner, now string, result outcome, dir string) {
	next := "proof failed — fix, do not claim"
	if result.exit == 0 && !result.timedOut {
		next = "proof holds — revalidator may VERIFY"
	}
	env := jq.NewObj(
		"schema_version", "1",
		"verb", "verify",
		"repo", r.Repo,
		"pr", cli.PrJSON(r.Pr),
		"at", now,
		"next", next,
		"warnings", cliStrings(r.Warnings),
		"data", jq.NewObj(
			"exit", result.exit,
			"timedOut", result.timedOut,
			"outputTail", result.tail,
			"patch", dir,
		),
		"error", nil,
	)
	fmt.Fprintln(r.Out, env.MarshalPretty())
}

// readVerifyCmd reads patch.json's verifyCmd with `jq -r` semantics: a
// missing or null verifyCmd reads empty (refusing no-verify-cmd, exactly
// like the shell's `// empty` fallback on corrupt stores per R1); any other
// non-string renders the way jq -r would print it.
func readVerifyCmd(dir string) (string, error) {
	raw, err := os.ReadFile(patchPath(dir))
	if err != nil {
		return "", missingPatchRefusal(dir)
	}
	doc, err := jq.Decode(raw)
	if err != nil {
		return "", &cli.RefusalError{Code: codeNoVerifyCmd, Message: noVerifyCmdMsg}
	}
	m, ok := jq.AsMap(doc)
	if !ok {
		return "", &cli.RefusalError{Code: codeNoVerifyCmd, Message: noVerifyCmdMsg}
	}
	vcmdRaw, ok := m["verifyCmd"]
	if !ok || vcmdRaw == nil {
		return "", &cli.RefusalError{Code: codeNoVerifyCmd, Message: noVerifyCmdMsg}
	}
	vcmd := jq.R(vcmdRaw)
	if vcmd == "" {
		return "", &cli.RefusalError{Code: codeNoVerifyCmd, Message: noVerifyCmdMsg}
	}
	return vcmd, nil
}

// parseTimeout ports the timeout gate: positive integers only.
func parseTimeout(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*decimalBase + int(s[i]-'0')
	}
	if n <= 0 {
		return 0, false
	}
	return n, true
}

// recordOutcome merges the watchdog result into patch.json, preserving the
// shell's key order (existing keys keep position, new keys append).
func recordOutcome(dir, now string, result outcome) error {
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
	m.Set("verifyExit", result.exit)
	m.Set("verifiedAt", now)
	m.Set("timedOut", result.timedOut)
	m.Set("verifiedTail", result.tail)
	if err := atomic.WriteFile(dir+"/"+patchFile, []byte(m.MarshalPretty()+"\n")); err != nil {
		return &cli.RefusalError{Code: "verify-failed", Message: fmt.Sprintf("could not write %s/patch.json: %v", dir, err)}
	}
	return nil
}

// childEnv builds the spawn environment: the full process environment plus
// the NO_COLOR default in JSON mode (mirroring json_redirect's export), so
// the verify child inherits the convention per F1.
func childEnv(r *cli.Runner) []string {
	env := os.Environ()
	if !r.JSONMode {
		return env
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "NO_COLOR=") {
			return env
		}
	}
	return append(env, "NO_COLOR=1")
}

// boolStr renders timed_out the shell way (true/false, lowercase).
func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// cliStrings converts warnings to JSON string values.
func cliStrings(in []string) []any {
	out := make([]any, 0, len(in))
	for _, s := range in {
		out = append(out, s)
	}
	return out
}
