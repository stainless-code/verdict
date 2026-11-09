// Package run launches the operator's own agent against an existing bundle.
//
// One process, one bundle, one shot: the service resolves the agent binary,
// writes a launch record, spawns the agent through the injected commander with
// the same watchdog verify uses, then asserts exactly one fresh review artifact
// and hands off. It never retries, re-prompts, repairs, or reads the agent's
// prose, and it never posts. A missing binary, a spawn failure, a watchdog
// timeout, a non-zero exit, an absent artifact, and a stale one are typed
// refusals, each with a hint; the agent's own non-zero exit is also the
// process exit code.
package run

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	// The launcher's own refusal family. Each names its remedy in the hint table.
	codeAgentMissing     = "agent-missing"
	codeAgentSpawnFailed = "agent-spawn-failed"
	codeAgentTimeout     = "agent-timeout"
	codeAgentFailed      = "agent-failed"
	codeNoArtifact       = "no-review-artifact"
	codeStaleArtifact    = "stale-review-artifact"
	codeUnknownAgent     = "unknown-agent"
	codeRecordFailed     = "agent-record-failed"
)

// Reused refusal codes: the shared vocabulary for "you canceled", "there is no
// such bundle", "this flag combination is wrong", and "that timeout is not a
// number". Reusing them keeps one meaning per code across verbs.
const (
	codeCanceled      = "canceled"
	codeMissingBundle = "missing-bundle"
	codeBadFlags      = "bad-flags"
	codeBadTimeout    = "bad-timeout"
)

const (
	// reviewFile is the single artifact contract: record and post read this
	// exact file from the bundle, and the launcher asserts nothing else.
	reviewFile = "review.json"
	// msgCanceled is the frozen cancel text, matching every other verb.
	msgCanceled = "canceled before run completed"
)

// Run launches the configured agent against the bundle for one shot.
func Run(ctx context.Context, req *core.RunRequest) (resp *core.RunResponse, ref *core.RefusalError) {
	st := &state{req: req, warnings: append([]string{}, req.Warnings...)}
	defer func() { st.carry(resp, ref) }()
	if err := ctx.Err(); err != nil {
		return nil, st.refuse(codeCanceled, msgCanceled)
	}
	dir, ref := st.bundle()
	if ref != nil {
		return nil, ref
	}
	def, ref := st.agent()
	if ref != nil {
		return nil, ref
	}
	timeout, ref := st.timeout()
	if ref != nil {
		return nil, ref
	}
	args := launchArgs{
		def:      def,
		dir:      dir,
		bundle:   readcommon.DisplayPath(st.req.RepoRootPath(), dir),
		artifact: readcommon.DisplayPath(st.req.RepoRootPath(), dir+"/"+reviewFile),
		abs:      dir + "/" + reviewFile,
		timeout:  timeout,
	}
	args.prompt = directive(st.req.Repo, st.req.Pr, args.bundle, args.artifact, readcommon.IsLocalBundleDir(dir))
	args.argv = argvFor(def, st.req.PassThrough, args.prompt)
	if st.req.Print {
		return st.printed(&args), nil
	}
	return st.launch(ctx, &args)
}

// state carries the accumulated warnings plus the request every step reads.
type state struct {
	req      *core.RunRequest
	warnings []string
}

// launchArgs is the resolved invocation one launch works from: the definition,
// the bundle and its review path, the timeout, and the exact argv.
type launchArgs struct {
	def      agentDef
	dir      string
	bundle   string
	artifact string
	abs      string
	prompt   string
	argv     []string
	timeout  int
}

// carry replays the accumulated warnings onto the refusal or the response, so a
// refusal's envelope carries the same warning list a success run would.
func (st *state) carry(resp *core.RunResponse, ref *core.RefusalError) {
	if ref != nil {
		ref.Warnings = st.warnings
	}
	if resp != nil {
		resp.Warnings = st.warnings
	}
}

func (st *state) refuse(code, msg string) *core.RefusalError {
	return &core.RefusalError{Code: code, Message: msg}
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

// now reads the injected clock, defaulting to the wall clock.
func (st *state) now() time.Time {
	if st.req.Runtime != nil && st.req.Now != nil {
		return st.req.Now()
	}
	return time.Now()
}

// bundle resolves the existing bundle directory. The launcher never fetches: an
// absent bundle is a typed refusal naming the command that writes one.
func (st *state) bundle() (string, *core.RefusalError) {
	dir := readcommon.ResolveBundleDir(st.req.RepoRootPath(), st.req.Pr)
	if readcommon.IsDir(dir) {
		return dir, nil
	}
	return "", st.refuse(codeMissingBundle, fmt.Sprintf(
		"missing bundle %s — fetch it first (verdict fetch %s %s, or verdict fetch-local for a local bundle)",
		readcommon.DisplayPath(st.req.RepoRootPath(), dir), st.req.Repo, st.req.Pr))
}

// agent resolves the --agent value against the shipped definition table. An
// absent or unknown name refuses with the known list: the table is data, so the
// message is the only place an operator learns it can grow.
func (st *state) agent() (agentDef, *core.RefusalError) {
	if st.req.Agent == "" {
		return agentDef{}, st.refuse(codeBadFlags, "run needs --agent <name> (known: "+knownAgents()+")")
	}
	def, ok := lookupAgent(st.req.Agent)
	if !ok {
		return agentDef{}, st.refuse(codeUnknownAgent, fmt.Sprintf(
			"unknown agent '%s' (known: %s)", st.req.Agent, knownAgents()))
	}
	return def, nil
}

// timeout parses the raw --timeout value with the same rule verify uses.
func (st *state) timeout() (int, *core.RefusalError) {
	n, err := strconv.Atoi(st.req.TimeoutRaw)
	if err != nil || n <= 0 || !digits(st.req.TimeoutRaw) {
		return 0, st.refuse(codeBadTimeout, fmt.Sprintf(
			"timeout must be a positive number of seconds, got '%s'", st.req.TimeoutRaw))
	}
	return n, nil
}

// digits reports whether s is a run of ASCII digits (so "+5"/" 5" never parse).
func digits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// printed builds the print-only response: the exact command this run would
// spawn, rendered by the adapter, with nothing resolved, spawned, or written.
func (st *state) printed(args *launchArgs) *core.RunResponse {
	return &core.RunResponse{
		Repo: st.req.Repo,
		Pr:   st.req.Pr,
		At:   core.NowUTC(st.now()),
		Data: &core.RunData{
			Agent:    args.def.Name,
			Binary:   args.def.Binary,
			Argv:     args.argv,
			Bundle:   args.bundle,
			Artifact: args.artifact,
			Timeout:  args.timeout,
			Printed:  true,
		},
		Next: "re-run without --print to launch it, or run the printed command yourself",
	}
}

// launch runs the one process: resolve, record the launch, spawn with the
// watchdog, then assert the artifact and hand off.
func (st *state) launch(ctx context.Context, args *launchArgs) (*core.RunResponse, *core.RefusalError) {
	bin, ref := st.resolve(args.def)
	if ref != nil {
		return nil, ref
	}
	started := st.now()
	rec := &launcher{
		SchemaVersion: launcherSchema,
		Agent:         args.def.Name,
		Binary:        bin,
		Argv:          argvShape(args.argv, args.prompt, st.req.Environ),
		Bundle:        args.bundle,
		Artifact:      args.artifact,
		StartedAt:     core.NowUTC(started),
		Timeout:       args.timeout,
	}
	launched, ref := writeLauncher(args.dir+"/"+launcherFile, rec)
	if ref != nil {
		return nil, ref
	}
	st.human(fmt.Sprintf("» run: %s (timeout %ds)", strings.Join(rec.Argv, " "), args.timeout))
	res, err := st.spawn(ctx, args.argv, args.timeout)
	return st.finish(ctx, args, rec, launched, started, res, err)
}

// resolve finds the agent binary on PATH. "Not installed" is the launcher's
// typed refusal, not a spawn error: the distinction tells the operator whether
// to install something or to fix what is installed.
func (st *state) resolve(def agentDef) (string, *core.RefusalError) {
	look := st.req.LookPath
	if look == nil {
		look = exec.LookPath
	}
	bin, err := look(def.Binary)
	if err != nil {
		return "", st.refuse(codeAgentMissing, fmt.Sprintf(
			"agent '%s' is not installed: no '%s' on PATH", def.Name, def.Binary))
	}
	return bin, nil
}

// spawn runs the agent under the watchdog: its own process group, a tree
// kill on the budget, and a combined bounded capture. The agent inherits the
// operator's environment (that is how it authenticates), minus color when
// the run is machine-only.
func (st *state) spawn(ctx context.Context, argv []string, timeout int) (*core.Result, error) {
	commander := st.commander()
	if commander == nil {
		return nil, errNoCommander
	}
	tctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	res, err := commander.Run(tctx, &core.Command{
		Argv:      argv,
		Env:       st.childEnv(),
		Dir:       st.req.RepoRootPath(),
		OutputCap: coreexec.VerifyOutputCap,
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // spawn failures are mapped by the caller, never reworded
	}
	return &res, nil
}

// errNoCommander is the launcher's own wiring failure: no scripted or
// production commander reached the service.
var errNoCommander = jq.NewErr("no commander in runtime")

func (st *state) commander() core.Commander {
	if st.req.Runtime == nil {
		return nil
	}
	return st.req.Commander
}

// childEnv is the ordered process environment the agent inherits, with NO_COLOR
// forced under --json so a machine-only run cannot gain escape codes.
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

// finish writes the outcome record and turns the observations into the run's
// answer: the cancel, the agent's failure, the watchdog, then the artifact
// assertion. res is nil when no process ever started.
func (st *state) finish(ctx context.Context, args *launchArgs, rec *launcher, launched, started time.Time,
	res *core.Result, spawnErr error,
) (*core.RunResponse, *core.RefusalError) {
	art := inspectArtifact(args.abs, launched)
	ref := st.verdict(ctx, args, res, spawnErr, art)
	ended := st.now()
	if writeRef := st.recordOutcome(rec, args.dir, ended, started, res, art); writeRef != nil {
		if ref == nil {
			ref = writeRef
		} else {
			st.warn(writeRef.Message)
		}
	}
	if ref != nil {
		return st.refused(ref, res, ended), ref
	}
	return st.response(args, rec, res, art, started, ended), nil
}

// refused is the response that accompanies a refusal: it exists only so a
// non-zero agent exit can become the process exit code.
func (st *state) refused(ref *core.RefusalError, res *core.Result, ended time.Time) *core.RunResponse {
	resp := &core.RunResponse{Repo: st.req.Repo, Pr: st.req.Pr, At: core.NowUTC(ended)}
	if res != nil && ref.Code == codeAgentFailed {
		resp.Exit = res.ExitCode
	}
	return resp
}

// verdict picks the refusal the run reports, in the order the operator can act
// on: a cancel outranks everything (the command never finished), then a spawn
// failure, the watchdog, the non-zero exit, and finally the artifact assertion.
func (st *state) verdict(ctx context.Context, args *launchArgs, res *core.Result, spawnErr error,
	art artifactState,
) *core.RefusalError {
	switch {
	case ctx.Err() != nil:
		return st.refuse(codeCanceled, msgCanceled)
	case spawnErr != nil:
		return st.refuse(codeAgentSpawnFailed, fmt.Sprintf(
			"could not start agent '%s': %v", args.def.Name, spawnErr))
	case res == nil:
		return st.refuse(codeAgentSpawnFailed, fmt.Sprintf("could not start agent '%s'", args.def.Name))
	case res.TimedOut:
		return st.refuse(codeAgentTimeout, fmt.Sprintf(
			"%s ran past the %ds watchdog and was killed (raise --timeout, or pass its fast flags after '--')",
			args.def.Name, args.timeout))
	case res.ExitCode != 0:
		return st.refuse(codeAgentFailed, fmt.Sprintf("%s exited %d", args.def.Name, res.ExitCode))
	}
	return st.artifactRefusal(args, art)
}

// artifactRefusal is the artifact contract: exactly one fresh review file,
// at the bundle's review path, written after the launch record.
func (st *state) artifactRefusal(args *launchArgs, art artifactState) *core.RefusalError {
	switch {
	case !art.exists:
		return st.refuse(codeNoArtifact, fmt.Sprintf(
			"%s exited 0 but wrote no %s", args.def.Name, args.artifact))
	case !art.regular:
		return st.refuse(codeNoArtifact, args.artifact+" is not a regular file — the review artifact must be one JSON file")
	case !art.fresh:
		return st.refuse(codeStaleArtifact, fmt.Sprintf(
			"%s predates this run (written %s, launched %s): the agent did not write it",
			args.artifact, core.NowUTC(art.mtime), core.NowUTC(art.launched)))
	}
	return nil
}

// recordOutcome writes the log, then the completed provenance record. The
// record carries the agent name, the resolved binary, the redacted argv
// shape, the timings, and the exit code; the full stream stays in .run.out
// for the operator.
func (st *state) recordOutcome(rec *launcher, dir string, ended, started time.Time,
	res *core.Result, art artifactState,
) *core.RefusalError {
	if res != nil {
		output := res.Stdout + res.Stderr
		if err := writeRunOutput(filepath.Join(dir, runOutputFile), output); err != nil {
			st.warn(err.Error())
		}
		rec.OutputTail = st.tail(output)
		rec.Exit = &res.ExitCode
		rec.TimedOut = &res.TimedOut
	}
	ms := int(ended.Sub(started).Milliseconds())
	rec.DurationMS = &ms
	rec.EndedAt = core.NowUTC(ended)
	rec.ArtifactFresh = &art.fresh
	if art.exists && art.regular {
		rec.ArtifactBytes = &art.size
	}
	_, ref := writeLauncher(filepath.Join(dir, launcherFile), rec)
	return ref
}

// tail is the recorded slice of the child's output: the last bytes, with the
// operator's own secret values scrubbed. The full stream stays in .run.out, the
// way verify keeps .verify.out raw for the operator.
func (st *state) tail(output string) string {
	return scrubSecrets(tailOf(output, tailCap), st.req.Environ)
}

// response is the successful run's envelope data plus the handoff line.
func (st *state) response(args *launchArgs, rec *launcher, res *core.Result, art artifactState,
	started, ended time.Time,
) *core.RunResponse {
	st.human(fmt.Sprintf("» exit=%d timed_out=%s duration=%s",
		res.ExitCode, strconv.FormatBool(res.TimedOut), ended.Sub(started).Round(time.Millisecond)))
	st.human(fmt.Sprintf("» wrote %s (fresh, %d bytes)", args.artifact, art.size))
	st.human("» recorded " + st.display(rec.path(args.dir)))
	return &core.RunResponse{
		Repo: st.req.Repo,
		Pr:   st.req.Pr,
		At:   core.NowUTC(ended),
		Data: &core.RunData{
			Agent:      args.def.Name,
			Binary:     rec.Binary,
			Argv:       rec.Argv,
			Bundle:     rec.Bundle,
			Artifact:   rec.Artifact,
			StartedAt:  rec.StartedAt,
			EndedAt:    rec.EndedAt,
			DurationMS: durationMS(rec.DurationMS),
			Exit:       res.ExitCode,
			TimedOut:   res.TimedOut,
			Timeout:    rec.Timeout,
			Fresh:      art.fresh,
			Record:     st.display(rec.path(args.dir)),
		},
		Next: st.next(args.dir),
	}
}

// durationMS reads the recorded duration for the response. The response is only
// built after the process exited, so an outcome write always set it; the nil
// guard keeps that ordering assumption from becoming a panic.
func durationMS(ms *int) int {
	if ms == nil {
		return 0
	}
	return *ms
}

// display renders a path for operator-facing output: repository-relative
// when it lives under the reviewed root.
func (st *state) display(path string) string {
	return readcommon.DisplayPath(st.req.RepoRootPath(), path)
}

// next names the operator's next step, unchanged: the launcher itself never
// records or posts.
func (st *state) next(dir string) string {
	if readcommon.IsLocalBundleDir(dir) {
		return "record the review with verdict record " + st.req.Repo + " " + readcommon.BundleName(dir)
	}
	return "post the review with verdict post " + st.req.Repo + " " + st.req.Pr
}

// artifactState is the one freshness assertion the launcher makes.
type artifactState struct {
	launched time.Time
	mtime    time.Time
	size     int64
	exists   bool
	regular  bool
	fresh    bool
}

// inspectArtifact stats the contract path and compares its modification time
// with the launch record's. Both stamps come from the same filesystem, so the
// comparison is not sensitive to clock skew. It is a second-granularity
// comparison on filesystems that store whole seconds: a leftover artifact
// stamped in the same second as the launch record reads as fresh. The failure
// direction is deliberate - it fails open, so a same-tick leftover is reviewed
// rather than silently refused - but the guarantee is "older than the launch",
// not "written by this agent".
func inspectArtifact(path string, launched time.Time) artifactState {
	art := artifactState{launched: launched}
	info, err := os.Stat(path)
	if err != nil {
		return art
	}
	art.exists = true
	art.regular = info.Mode().IsRegular()
	art.mtime = info.ModTime()
	art.size = info.Size()
	art.fresh = art.regular && !art.mtime.Before(launched)
	return art
}
