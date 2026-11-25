// Package run launches the operator's own agent against an existing bundle.
//
// Each finder and cross batch uses a fresh process so independent reviews
// cannot share session state. The launcher never posts.
package run

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	"github.com/stainless-code/verdict/internal/core/recipe"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/core/repair"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

const (
	// DefaultTimeoutS is the launcher's watchdog budget for a caller that names
	// none: the run verb's --timeout default, and demo's, which may be widened
	// with the same flag.
	DefaultTimeoutS = "300"
	// The launcher's own refusal family. Each names its remedy in the hint table.
	codeAgentMissing     = "agent-missing"
	codeAgentMismatch    = "agent-mismatch"
	codeAgentSpawnFailed = "agent-spawn-failed"
	codeAgentTimeout     = "agent-timeout"
	codeAgentFailed      = "agent-failed"
	codeNoArtifact       = "no-review-artifact"
	codeStaleArtifact    = "stale-review-artifact"
	codeUnknownAgent     = "unknown-agent"
	codeRecordFailed     = "agent-record-failed"
	// codeBadSettings is reused from the settings vocabulary: a prefs-defined
	// agent that collides with a shipped name is a bad settings file.
	codeBadSettings = "bad-settings"
)

const (
	// probeTimeout bounds the identity probe, separately from the agent's own
	// --timeout budget: a wrong binary must never hold a launch for minutes.
	probeTimeout = 5 * time.Second
	// probeOutputCap is the window searched for the identity match. The child
	// gets a wider capture so the head of a noisy help survives to this slice.
	probeOutputCap = 64 * 1024
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
	drainFile  = "drain.replies.json"
	// msgCanceled is the frozen cancel text, matching every other verb.
	msgCanceled = "canceled before run completed"
	// runStateFile is the bundle's typed run record, which run reads only to
	// project a declared budget.
	runStateFile = "run.json"
	// floatBitSize is the precision strconv needs to render a declared dollar
	// amount without inventing digits.
	floatBitSize = 64
)

// Run launches the selected finder topology against captured bundle evidence.
func Run(ctx context.Context, req *core.RunRequest) (resp *core.RunResponse, ref *core.RefusalError) {
	return WithAgents(ctx, req, nil)
}

// WithAgents adds the operator-defined rows read from the prefs file to the
// shipped table for one invocation. Definitions are values: the caller owns
// the slice, and the service never mutates it.
func WithAgents(
	ctx context.Context, req *core.RunRequest, agents []config.PersonalAgent,
) (resp *core.RunResponse, ref *core.RefusalError) {
	seen := make(map[string]bool, len(agents))
	for i := range agents {
		def := &agents[i]
		path := "$.agents." + def.Name
		if _, ok := lookupAgent(def.Name); ok {
			return nil, &core.RefusalError{
				Code:    codeBadSettings,
				Message: path + ": collides with a built-in agent; pick another name",
			}
		}
		if seen[def.Name] {
			return nil, &core.RefusalError{
				Code:    codeBadSettings,
				Message: path + ": defined twice; use one agent per name",
			}
		}
		seen[def.Name] = true
	}
	st := &state{req: req, warnings: append([]string{}, req.Warnings...), defs: agents}
	return execute(ctx, st)
}

func execute(ctx context.Context, st *state) (resp *core.RunResponse, ref *core.RefusalError) {
	defer func() { st.carry(resp, ref) }()
	if err := ctx.Err(); err != nil {
		return nil, st.refuse(codeCanceled, msgCanceled)
	}
	dir, ref := st.launchBundle()
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
	rounds, ref := st.rounds()
	if ref != nil {
		return nil, ref
	}
	args, ref := st.argsFor(&def, dir, timeout)
	if ref != nil {
		return nil, ref
	}
	return st.launchSelected(ctx, args, rounds)
}

// argsFor resolves everything one invocation works from: the declared pipeline,
// the stage a render covers, the bundle and its review path, the exact argv,
// and the agent binary on PATH.
func (st *state) argsFor(def *agentcatalog.Launch, dir string, timeout int) (*launchArgs, *core.RefusalError) {
	return st.argsForTarget(def, timeout, launchTarget{
		evidence: dir, output: dir,
		artifact: st.artifactName(), checker: repair.Check,
	})
}

func (st *state) argsForTarget(def *agentcatalog.Launch, timeout int, target launchTarget) (*launchArgs, *core.RefusalError) {
	dir := target.evidence
	plan, ref := st.pipeline(dir)
	if ref != nil {
		return nil, ref
	}
	at, ref := st.stage(plan)
	if ref != nil {
		return nil, ref
	}
	args := &launchArgs{
		def:      *def,
		target:   target,
		dir:      target.output,
		plan:     plan,
		staged:   at,
		bundle:   readcommon.DisplayPath(st.req.RepoRootPath(), dir),
		artifact: readcommon.DisplayPath(st.req.RepoRootPath(), filepath.Join(target.output, target.artifact)),
		abs:      filepath.Join(target.output, target.artifact),
		timeout:  timeout,
	}
	bin, ref := st.resolve(&args.def)
	if ref != nil {
		return nil, ref
	}
	args.bin = bin
	args.restriction = discoveryRestriction(def, st.req.PassThrough, st.req.AllowShell)
	artifact := st.promptPath(args.abs, args.artifact)
	st.compose(args, st.req, st.promptBundle(dir), artifact, readcommon.IsLocalBundleDir(dir))
	if target.procedure != "" {
		procedure := st.promptPath(target.procedure, readcommon.DisplayPath(st.req.RepoRootPath(), target.procedure))
		args.prompt = strings.ReplaceAll(args.prompt, st.promptBundle(dir)+"/procedure.md", procedure)
	}
	args.argv = argvFor(&args.def, args.model(), args.restriction, st.req.PassThrough, args.prompt)
	return args, nil
}

// compose builds the prompt and argv one invocation works from: the
// whole-pipeline directive, or the one stage --stage asked to render, whose
// declared model hint becomes the agent's model argument.
func (st *state) compose(a *launchArgs, req *core.RunRequest, bundle, artifact string, local bool) {
	evidenceDir := a.target.evidence
	if evidenceDir == "" {
		evidenceDir = a.dir
	}
	hasContext := contextInBundle(evidenceDir)
	switch {
	case st.drainDigest != "":
		a.prompt = drainDirective(req.Repo, req.Pr, bundle, artifact, st.drainDigest)
	case a.staged == nil:
		a.prompt = directive(req.Repo, req.Pr, bundle, artifact, local, hasContext, a.plan)
	default:
		a.prompt = stageDirective(req.Repo, req.Pr, bundle, artifact, local, hasContext, a.plan, a.staged)
	}
	a.argv = argvFor(&a.def, a.model(), a.restriction, req.PassThrough, a.prompt)
}

// contextInBundle reports whether the bundle carries the handed context text.
func contextInBundle(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "context.txt"))
	return err == nil && info.Mode().IsRegular()
}

// model is the model argument the argv composes: the rendered stage's declared
// hint, and nothing on a launch, which leaves model choice to the operator.
func (a *launchArgs) model() string {
	if a.staged == nil {
		return ""
	}
	return a.staged.stage.Model
}

// staged is the one pipeline stage a --print render covers: the 1-based
// position the operator named and the resolved row there.
type staged struct {
	stage recipe.Stage
	index int
}

// stage validates --stage and resolves the row it names. A stage render needs
// --print (verdict launches one process and chains no stages) and --recipe (the
// built-in default declares no lens hint, cap, or model, so a stage of it has
// nothing to render), and it owns the model argument, so a pass-through model
// would silently override the declared hint. The operand must be the canonical
// spelling of the index it resolves: `02` and `+2` are the same position as
// `2`, and one index spelled three ways is three things to match in a CI log.
func (st *state) stage(plan *recipe.Plan) (*staged, *core.RefusalError) {
	if st.req.StageRaw == "" {
		return nil, nil
	}
	if !st.req.Print {
		return nil, st.refuse(codeBadFlags, "run --stage renders one stage's command for --print: "+
			"verdict launches one process and chains no stages")
	}
	if st.req.Recipe == "" {
		return nil, st.refuse(codeBadFlags, "run --stage renders one stage of a declared recipe: "+
			"--recipe is missing (the built-in default declares no lens hints or caps)")
	}
	index, err := strconv.Atoi(st.req.StageRaw)
	if err != nil || strconv.Itoa(index) != st.req.StageRaw || index < 1 || index > len(plan.Stages) {
		return nil, st.refuse(codeBadFlags, fmt.Sprintf("--stage must be one of %s, got '%s'",
			stageNames(plan.Stages), st.req.StageRaw))
	}
	at := staged{index: index, stage: plan.Stages[index-1]}
	if at.stage.Model != "" && passThroughModel(st.req.PassThrough) {
		return nil, st.refuse(codeBadFlags, fmt.Sprintf(
			"--print --stage %d renders the declared model hint '%s'; drop --model/-m after '--'",
			index, at.stage.Model))
	}
	return &at, nil
}

// stageNames renders the accepted --stage values in pipeline order, so the
// refusal names what the plan actually resolved.
func stageNames(stages []recipe.Stage) string {
	names := make([]string, 0, len(stages))
	for i, stage := range stages {
		names = append(names, fmt.Sprintf("%d %s", i+1, stage.Kind))
	}
	return strings.Join(names, ", ")
}

// passThroughModel reports whether the operator already selects a model after
// `--`, in either the spaced or the inline spelling.
func passThroughModel(args []string) bool {
	for _, arg := range args {
		if name, _, _ := strings.Cut(arg, "="); name == modelFlag || name == "-m" {
			return true
		}
	}
	return false
}

// promptBundle is the bundle path the directive names: the repository-relative
// display path, or the absolute one when the agent starts in another checkout.
func (st *state) promptBundle(dir string) string {
	return st.promptPath(dir, readcommon.DisplayPath(st.req.RepoRootPath(), dir))
}

// promptPath keeps the display path for a launch in the repository root. A
// launch from another checkout names the path absolutely, because the bundle
// lives under the repository root, outside that checkout.
func (st *state) promptPath(abs, display string) string {
	if st.req.Checkout != "" {
		return abs
	}
	return display
}

// invoke runs the print escape hatch, the repair loop, or the one-shot,
// after the request is fully resolved.
func (st *state) invoke(ctx context.Context, args *launchArgs, rounds int) (*core.RunResponse, *core.RefusalError) {
	if st.req.Print {
		if ref := st.printRoundsRefusal(rounds); ref != nil {
			return nil, ref
		}
		if !printablePassThrough(st.req.PassThrough, st.req.Environ) {
			return nil, st.refuse(codeBadFlags, "--print accepts only model and cost options with safe values; "+
				"supply credentials through the environment")
		}
		return st.printed(args), nil
	}
	if ref := st.prepareLaunch(ctx, args, rounds); ref != nil {
		return nil, ref
	}

	st.warnLaunch(&args.def)
	if st.roundLoop(rounds) {
		return st.repairLoop(ctx, args, rounds)
	}
	return st.launch(ctx, args)
}

func (st *state) prepareLaunch(ctx context.Context, args *launchArgs, rounds int) *core.RefusalError {
	ref := st.phaseSpend(ctx, args.target.evidence, rounds)
	if ctx.Err() != nil {
		return st.refuse(codeCanceled, msgCanceled)
	}
	if ref != nil {
		return ref
	}
	return st.probe(ctx, args)
}

func (st *state) warnLaunch(def *agentcatalog.Launch) {
	if def.LaunchWarning != "" {
		st.warn(def.LaunchWarning)
	}
}

// printRoundsRefusal refuses a render asked for a repair loop it cannot run: a
// render launches nothing, so the absent flag's default two rounds are ignored,
// while an explicit count above one is an operator error.
func (st *state) printRoundsRefusal(rounds int) *core.RefusalError {
	if rounds <= 1 || st.req.RoundsRaw == "" {
		return nil
	}
	return st.refuse(codeBadFlags, "run --print renders one command: re-run without --print "+
		"to launch --rounds "+st.req.RoundsRaw)
}

// roundLoop reports whether this invocation runs the launch-to-check loop. A
// drain launch writes drain.replies.json, not a review: there is nothing for
// the loop's review checker to read, so it stays one shot whatever the round
// count says.
func (st *state) roundLoop(rounds int) bool {
	return rounds > 1 && st.drainDigest == ""
}

// probe confirms a binary is the agent the row claims before any launch. It
// runs the declared harmless command with the launch's environment and working
// directory, no stdin, a five-second budget, and a capped capture. A wrong,
// silent, or hanging binary refuses agent-mismatch without spawning the agent.
func (st *state) probe(ctx context.Context, args *launchArgs) *core.RefusalError {
	if len(args.def.ProbeArgs) == 0 {
		return nil
	}
	if ctx.Err() != nil {
		return st.refuse(codeCanceled, msgCanceled)
	}
	commander := st.commander()
	if commander == nil {
		return st.refuse(codeAgentMismatch, fmt.Sprintf(
			"agent '%s' at %s could not be probed: no commander in runtime", args.def.Name, args.bin))
	}
	pctx, cancel := context.WithTimeout(ctx, st.probeBudget())
	defer cancel()
	res, err := commander.Run(pctx, &core.Command{
		Argv:      append([]string{args.bin}, args.def.ProbeArgs...),
		Env:       st.childEnv(),
		Dir:       st.workDir(),
		Stdin:     strings.NewReader(""),
		OutputCap: coreexec.VerifyOutputCap,
	})
	if ctx.Err() != nil {
		return st.refuse(codeCanceled, msgCanceled)
	}
	out := res.Stdout + res.Stderr
	if len(out) > probeOutputCap {
		out = out[:probeOutputCap]
	}
	if result := probeFailure(pctx, out, res, err, args.def.ProbeMatch); result != "" {
		return st.refuse(codeAgentMismatch, fmt.Sprintf(
			"agent '%s' at %s failed its identity probe: %s", args.def.Name, args.bin, result))
	}
	args.probe = &launcherProbe{Args: slices.Clone(args.def.ProbeArgs), OK: true}
	return nil
}

// probeBudget returns the shipped probe deadline unless a caller supplied its
// own, so process fixtures never race the five-second product constant.
func (st *state) probeBudget() time.Duration {
	if st.req.ProbeTimeout > 0 {
		return st.req.ProbeTimeout
	}
	return probeTimeout
}

// probeFailure names why an identity probe cannot vouch for the binary, and
// stays empty when the match is present in the captured window.
func probeFailure(pctx context.Context, out string, res core.Result, err error, match string) string {
	switch {
	case pctx.Err() != nil || res.TimedOut:
		return "timed out"
	case err != nil:
		return fmt.Sprintf("could not run the probe: %v", err)
	case res.ExitCode != 0:
		return fmt.Sprintf("exited %d", res.ExitCode)
	case !strings.Contains(out, match):
		return fmt.Sprintf("probe output did not contain '%s'", match)
	default:
		return ""
	}
}

type state struct { //nolint:govet // request first: the service reads one invocation
	req         *core.RunRequest
	progress    *syncProgress
	drainDigest string
	warnings    []string
	defs        []config.PersonalAgent
	budget      *core.ReviewBudget
	twoFinders  bool
}

// syncProgress is the one lock the parallel phases share: it serializes their
// human lines, while each worker keeps its warnings to itself for a
// deterministic replay after the join.
type syncProgress struct {
	mu sync.Mutex
}

// phaseState gives one discovery or cross worker a copy of the run state: its
// own warning buffer and the shared progress lock, so neither worker appends to
// the other's slice or to the coordinator's.
func (st *state) phaseState(progress *syncProgress) *state {
	worker := *st
	worker.warnings = nil
	worker.progress = progress
	return &worker
}

// replay folds one worker's buffered warnings into the run's, in worker order.
func (st *state) replay(worker *state) {
	if worker == nil {
		return
	}
	for _, warning := range worker.warnings {
		st.warn(warning)
	}
}

type launchTarget struct {
	checker   func(string, []byte) (recordcore.CheckResult, *core.RefusalError)
	evidence  string
	output    string
	procedure string
	artifact  string
}

// launchArgs is the resolved invocation one launch works from: the definition,
// the declared pipeline, the one stage a render covers (nil otherwise), the
// bundle and its review path, the timeout, the discovery restriction this argv
// carries, and the exact argv.
type launchArgs struct {
	guard       func() *core.RefusalError
	target      launchTarget
	def         agentcatalog.Launch
	bin         string
	probe       *launcherProbe
	plan        *recipe.Plan
	staged      *staged
	dir         string
	bundle      string
	artifact    string
	abs         string
	prompt      string
	argv        []string
	restriction []string
	timeout     int
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

func (st *state) artifactName() string {
	if st.drainDigest != "" {
		return drainFile
	}
	return reviewFile
}

func (st *state) refuse(code, msg string) *core.RefusalError {
	return &core.RefusalError{Code: code, Message: msg}
}

// pipeline resolves the run's declared pipeline and folds in the warnings it
// carries: the recipe control of the base snapshot, the bundle's loaded lens
// ids, and the bundle's own spend limit. It is the one adapter between the
// service and the bundle facts the plan depends on.
func (st *state) pipeline(dir string) (*recipe.Plan, *core.RefusalError) {
	name := st.req.Recipe
	if name == "" {
		name = recipe.DefaultName
	}
	plan, ref := recipe.Load(dir, name)
	if ref != nil {
		return nil, ref
	}
	for _, warning := range plan.Warnings {
		st.warn(warning)
	}
	st.warnOverBudget(dir, plan)
	return plan, nil
}

// warnOverBudget projects the declared budget against the bundle's resolved
// personal limit. It warns and never refuses: verdict cannot meter an agent's
// spend, so a cap it cannot keep is not a refusal it may claim. A run bundle it
// cannot read loses the projection, and that loss is worth one line naming the
// file: silence would read as "no limit to cross". A bundle without a limit
// projects nothing, because record and post are the verbs that refuse a broken
// run bundle.
func (st *state) warnOverBudget(dir string, plan *recipe.Plan) {
	rs, found, err := config.ReadRunState(dir)
	if err != nil {
		st.warn(fmt.Sprintf("could not read %s, so the recipe's declared budget "+
			"is not projected against the bundle's spend limit — re-fetch to rebuild it",
			st.display(filepath.Join(dir, runStateFile))))
		return
	}
	if !found || rs.Resolved == nil || rs.Resolved.Spend.PersonalLimit == nil {
		return
	}
	limit := *rs.Resolved.Spend.PersonalLimit
	if plan.BudgetUSD <= limit {
		return
	}
	st.warn(fmt.Sprintf("recipe %s declares budget_usd %s, above the bundle's personal spend limit $%s — "+
		"run enforces nothing", plan.Name, formatUSD(plan.BudgetUSD), formatUSD(limit)))
}

// formatUSD renders one dollar amount without an exponent or trailing zeros.
func formatUSD(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, floatBitSize)
}

func (st *state) warn(msg string) {
	st.warnings = append(st.warnings, msg)
	if st.progress != nil || st.req.Runtime == nil {
		return
	}
	st.req.Warn(msg)
}

// human emits one operator-facing line, serialized while parallel phases run so
// two agents' lines never interleave mid-line.
func (st *state) human(line string) {
	if st.req.Runtime == nil {
		return
	}
	if st.progress != nil {
		st.progress.mu.Lock()
		defer st.progress.mu.Unlock()
	}
	st.req.Human(line)
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
	dir, ref := readcommon.ResolveBundleDir(st.req.RepoRootPath(), st.req.Pr)
	if ref != nil {
		return "", st.refuse(ref.Code, ref.Message)
	}
	if readcommon.IsDir(dir) {
		return dir, nil
	}
	return "", st.refuse(codeMissingBundle, fmt.Sprintf(
		"missing bundle %s — fetch it first (verdict fetch %s %s, or verdict fetch-local for a local bundle)",
		readcommon.DisplayPath(st.req.RepoRootPath(), dir), st.req.Repo, st.req.Pr))
}

// lookupAgent resolves one name against the shipped table, then the prefs
// definitions read for this invocation. Collisions refuse before this point.
func (st *state) lookupAgent(name string) (agentcatalog.Launch, bool) {
	if def, ok := lookupAgent(name); ok {
		return def, true
	}
	for i := range st.defs {
		if st.defs[i].Name == name {
			return operatorAgent(&st.defs[i]), true
		}
	}
	return agentcatalog.Launch{}, false
}

// operatorAgent projects one prefs row onto the same data shape as a shipped
// row. The source marker travels to launcher.json so provenance can say where
// the definition came from.
func operatorAgent(def *config.PersonalAgent) agentcatalog.Launch {
	return agentcatalog.Launch{
		Name: def.Name, Binary: def.Binary, Install: def.Install,
		ModelFlag: def.ModelFlag, PromptFlag: def.PromptFlag,
		ProbeMatch: def.ProbeMatch, Args: slices.Clone(def.Args),
		ProbeArgs: slices.Clone(def.ProbeArgs), Source: agentSourcePrefs,
	}
}

// knownAgents labels both sources in the refusal and the missing-name hint.
func (st *state) knownAgents() string {
	message := "known: " + knownAgents() + "; operator-defined in prefs: "
	if len(st.defs) == 0 {
		return message + "none"
	}
	names := make([]string, 0, len(st.defs))
	for i := range st.defs {
		names = append(names, st.defs[i].Name)
	}
	return message + strings.Join(names, ", ")
}

// agent resolves the --agent value against the shipped table and the operator
// definitions. An absent or unknown name refuses with both sources labeled: the
// message is the only place an operator learns the table can grow.
func (st *state) agent() (agentcatalog.Launch, *core.RefusalError) {
	if st.req.Agent == "" {
		return agentcatalog.Launch{}, st.refuse(codeBadFlags, "run needs --agent <name> ("+st.knownAgents()+")")
	}
	def, ok := st.lookupAgent(st.req.Agent)
	if !ok {
		return agentcatalog.Launch{}, st.refuse(codeUnknownAgent, fmt.Sprintf(
			"unknown agent '%s' (%s)", st.req.Agent, st.knownAgents()))
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

// printed is the envelope of a --print render. data.stage names the one stage a
// --stage render covers, and stays absent for the whole pipeline, so a plain
// --print keeps the envelope shape it always had.
func (st *state) printed(args *launchArgs) *core.RunResponse {
	data := &core.RunData{
		Agent:    args.def.Name,
		Binary:   args.def.Binary,
		Argv:     args.argv,
		Bundle:   args.bundle,
		Artifact: args.artifact,
		Recipe:   args.plan,
		Timeout:  args.timeout,
		Printed:  true,
		Budget:   st.budget,
	}
	next := "re-run without --print to launch it, or run the printed command yourself"
	if args.staged != nil {
		data.Stage = &core.RecipeStageRender{Index: args.staged.index, Kind: args.staged.stage.Kind}
		next = st.stageNext(args.staged, len(args.plan.Stages), args.dir)
	}
	return &core.RunResponse{
		Repo: st.req.Repo,
		Pr:   st.req.Pr,
		At:   core.NowUTC(st.now()),
		Data: data,
		Next: next,
	}
}

// stageNext names what follows one rendered stage. A stage render cannot become
// a launch — --stage requires --print — so the whole-pipeline advice to re-run
// without --print walks the operator into a refusal: the next step is the
// command just printed, then the following stage, and after the last stage the
// record or post that closes the pipeline.
func (st *state) stageNext(at *staged, stages int, dir string) string {
	if at.index >= stages {
		return fmt.Sprintf("run the printed command; stage %d closes the pipeline, and the "+
			"review it writes is ready — %s", stages, st.next(dir))
	}
	return fmt.Sprintf("run the printed command, then render stage %d with verdict run %s %s "+
		"--agent %s --recipe %s --print --stage %d",
		at.index+1, st.req.Repo, st.req.Pr, st.req.Agent, st.req.Recipe, at.index+1)
}

// launchRecord is the one place a launch record is built, so a field the record
// gained — the review budget, for one — cannot reach the one-shot launch and
// not the repair loop's.
func (st *state) launchRecord(args *launchArgs, bin string, started time.Time) *launcher {
	return &launcher{
		SchemaVersion: launcherSchema,
		Agent:         args.def.Name,
		AgentSource:   args.def.Source,
		Binary:        bin,
		Recipe:        args.plan,
		Bundle:        args.bundle,
		Artifact:      args.artifact,
		StartedAt:     core.NowUTC(started),
		Timeout:       args.timeout,
		Probe:         args.probe,
		Budget:        st.budget,
	}
}

// launch runs the one process of a one-shot run: resolve, record the launch,
// spawn with the watchdog, then assert the artifact and hand off.
func (st *state) launch(ctx context.Context, args *launchArgs) (*core.RunResponse, *core.RefusalError) {
	if ref := args.checkpoint(); ref != nil {
		return nil, ref
	}
	bin := args.bin
	started := st.now()
	rec := st.launchRecord(args, bin, started)
	argv, argSecrets := st.displayArgv(args)
	rec.Argv = argv
	launched, ref := writeLauncher(filepath.Join(args.dir, launcherFile), rec)
	if ref != nil {
		return nil, ref
	}
	st.human(fmt.Sprintf("» recipe: %s@%s (%s)", args.plan.Name, args.plan.Version, args.plan.Source))
	st.human(fmt.Sprintf("» run: %s (timeout %ds)", strings.Join(rec.Argv, " "), args.timeout))
	res, err := st.spawn(ctx, args.argv, args.timeout)
	if ref := args.checkpoint(); ref != nil {
		return nil, ref
	}
	if salvageAllowed(ctx, res, err) && st.salvageReview(args, res.Stdout) {
		rec.Salvaged = true
	}
	return st.finish(ctx, args, rec, launched, started, res, err, nil, argSecrets)
}

// resolve finds the agent binary on PATH. "Not installed" is the launcher's
// typed refusal, not a spawn error: the distinction tells the operator whether
// to install something or to fix what is installed.
func (st *state) resolve(def *agentcatalog.Launch) (string, *core.RefusalError) {
	look := st.req.LookPath
	if look == nil {
		look = exec.LookPath
	}
	bin, err := look(def.Binary)
	if err != nil {
		message := fmt.Sprintf("agent '%s' is not installed: no '%s' on PATH", def.Name, def.Binary)
		if def.Install != "" {
			message += "; " + def.Install
		} else {
			message += "; put it on PATH"
		}
		return "", st.refuse(codeAgentMissing, message)
	}
	return bin, nil
}

// spawn runs the agent under the watchdog: its own process group, a tree
// kill on the budget, and a combined bounded capture. The agent inherits the
// operator's environment (that is how it authenticates), minus color when
// the run is machine-only. The commander records the watchdog's kill in
// Result.TimedOut; this call never infers one from its own clock, so an agent
// that exits non-zero just before the budget stays a failure.
func (st *state) spawn(ctx context.Context, argv []string, timeout int) (*core.Result, error) {
	commander := st.commander()
	if commander == nil {
		return nil, errNoCommander
	}
	tctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	res, err := commander.Run(tctx, &core.Command{
		Argv:        argv,
		Env:         st.childEnv(),
		Dir:         st.workDir(),
		OutputCap:   coreexec.VerifyOutputCap,
		GracePeriod: agentGracePeriod,
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // spawn failures are mapped by the caller, never reworded
	}
	return &res, nil
}

// agentGracePeriod is how long an agent's process group gets to finish after
// the watchdog's stop signal, so a child that flushes its review on SIGTERM is
// not killed mid-write. Every other verb keeps the immediate kill.
const agentGracePeriod = 5 * time.Second

// errNoCommander is the launcher's own wiring failure: no scripted or
// production commander reached the service.
var errNoCommander = jq.NewErr("no commander in runtime")

// workDir is the directory the agent process starts in: the caller's checkout
// when it named one, the reviewed repository root otherwise.
func (st *state) workDir() string {
	if st.req.Checkout != "" {
		return st.req.Checkout
	}
	return st.req.RepoRootPath()
}

func (st *state) commander() core.Commander {
	if st.req.Runtime == nil {
		return nil
	}
	return st.req.Commander
}

// childEnv is the ordered process environment the agent inherits, with NO_COLOR
// forced under --json so a machine-only run cannot gain escape codes. The clone
// keeps the append off the request's backing array, which parallel phases read.
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
	return append(slices.Clone(env), "NO_COLOR=1")
}

// finish writes the outcome record and turns the observations into the run's
// answer: the cancel, the agent's failure, the watchdog, then the artifact
// assertion. res is nil when no process ever started. rep carries the
// repair-loop attempts, nil on a one-shot run.
func (st *state) finish(ctx context.Context, args *launchArgs, rec *launcher, launched, started time.Time,
	res *core.Result, spawnErr error, rep *core.RunRepair, argSecrets []string,
) (*core.RunResponse, *core.RefusalError) {
	art := inspectArtifact(args.abs, launched)
	ref := st.verdict(ctx, args, res, spawnErr, art)
	ended := st.now()
	if writeRef := st.recordOutcome(rec, args.dir, ended, started, res, art, rep, argSecrets); writeRef != nil {
		if ref == nil {
			ref = writeRef
		} else {
			st.warn(writeRef.Message)
		}
	}
	if ref == nil && st.drainDigest == "" {
		ref = st.reviewShapeRefusal(args, res)
	}
	if ref != nil {
		return st.refused(ref, res, ended), ref
	}
	return st.response(args, rec, res, art, started, ended, rep), nil
}

// reviewShapeRefusal validates the artifact with the same bundle-level check
// record uses. A run must never succeed on a document record would refuse,
// whether the agent wrote it or the salvage scanner copied it from stdout, and
// a one-shot run carries the same evidence refusal a repaired one does: no
// round to fix a gap is not permission to publish it.
func (st *state) reviewShapeRefusal(args *launchArgs, res *core.Result) *core.RefusalError {
	review, readRef := readRoundReview(args, res)
	if readRef != nil {
		return readRef
	}
	check, checkRef := args.checkReview(review)
	if checkRef != nil {
		return checkRef
	}
	if ref := recordcore.ReviewRefusal(check, false); ref != nil {
		return ref
	}
	return repair.InsufficientRefusal(check, "")
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
		return st.refuse(codeAgentFailed, fmt.Sprintf("%s exited %d", args.def.Name, res.ExitCode)+st.agentOutput(args, res))
	}
	return st.artifactRefusal(args, art, res)
}

// messageTailCap bounds the agent output a refusal message carries. The full
// stream stays in .run.out and the recorded tail in launcher.json; the message
// only has to name the cause (an HTTP 402, a refused write) at a glance.
const messageTailCap = 600

// agentOutput is the tail of what the agent printed, formatted to append to a
// refusal message. The agent's own error — a provider's out-of-credit reply, a
// refused write — is printed there, and without it every failure reads as the
// same bare exit code. It is empty when the agent printed nothing. The whole
// capture (the watchdog caps it at 1 MiB) runs through verify's sanitizer
// (escapes stripped first, then environment values, token shapes, bearer and
// authorization headers, userinfo URLs), the argv secrets are scrubbed from
// the escape-free text, and only then is it cut. Cutting first would let a
// credential split by the cut keep a recognizable half, and a bounded window
// is no safer: sanitizing the rest of it can shrink the text until a token at
// the window's left edge lands in the tail.
func (st *state) agentOutput(args *launchArgs, res *core.Result) string {
	_, argSecrets := st.displayArgv(args)
	clean := scrubSecrets(verifycore.SanitizeTail(res.Stdout+res.Stderr, st.req.Environ),
		st.req.Environ, verifycore.StripControlledSecrets(argSecrets))
	tail := strings.TrimSpace(strings.ToValidUTF8(tailOf(clean, messageTailCap), ""))
	if tail == "" {
		return ""
	}
	return "\nagent output (tail, redacted):\n" + tail
}

// artifactRefusal is the artifact contract: exactly one fresh review file,
// at the bundle's review path, written after the launch record.
func (st *state) artifactRefusal(args *launchArgs, art artifactState, res *core.Result) *core.RefusalError {
	switch {
	case !art.exists:
		return st.refuse(codeNoArtifact, fmt.Sprintf(
			"%s exited 0 but wrote no %s", args.def.Name, args.artifact)+st.agentOutput(args, res))
	case !art.regular:
		return st.refuse(codeNoArtifact, args.artifact+" is not a regular file — the artifact must be one JSON file")
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
// for the operator. A non-nil rep adds the repair-loop profile and attempts.
func (st *state) recordOutcome(rec *launcher, dir string, ended, started time.Time,
	res *core.Result, art artifactState, rep *core.RunRepair, argSecrets []string,
) *core.RefusalError {
	if res != nil {
		output := res.Stdout + res.Stderr
		if err := writeRunOutput(filepath.Join(dir, runOutputFile), output); err != nil {
			st.warn(err.Error())
		}
		rec.OutputTail = st.tail(output, argSecrets)
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
	if rep != nil {
		rec.Repair = &launcherRepair{Rounds: rep.Rounds, RoundsToValid: rep.RoundsToValid, FinalValid: rep.FinalValid}
		rec.Attempts = rep.Attempts
	}
	_, ref := writeLauncher(filepath.Join(dir, launcherFile), rec)
	return ref
}

// tail is the recorded slice of the child's output: the last bytes, with the
// operator's own secret values scrubbed - the environment's and the ones the
// recorded argv hid, because a child that echoes its own argv prints the very
// credential the operator typed after `--`. The full stream stays in .run.out,
// the way verify keeps .verify.out raw for the operator.
func (st *state) tail(output string, argSecrets []string) string {
	return scrubSecrets(tailOf(output, tailCap), st.req.Environ, argSecrets)
}

// response is the successful run's envelope data plus the handoff line.
func (st *state) response(args *launchArgs, rec *launcher, res *core.Result, art artifactState,
	started, ended time.Time, rep *core.RunRepair,
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
			Recipe:     args.plan,
			StartedAt:  rec.StartedAt,
			EndedAt:    rec.EndedAt,
			DurationMS: durationMS(rec.DurationMS),
			Exit:       res.ExitCode,
			TimedOut:   res.TimedOut,
			Timeout:    rec.Timeout,
			Fresh:      art.fresh,
			Record:     st.display(rec.path(args.dir)),
			Repair:     rep,
			Budget:     st.budget,
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
	if st.drainDigest != "" {
		return "inspect the drafts in " + st.display(filepath.Join(dir, drainFile))
	}
	if readcommon.IsLocalBundleDir(dir) {
		return "record the review with verdict record " + st.req.Repo + " " + readcommon.BundleName(dir)
	}
	return fmt.Sprintf("post the review with verdict post %s %s", st.req.Repo, st.req.Pr)
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

func (a *launchArgs) checkReview(review []byte) (recordcore.CheckResult, *core.RefusalError) {
	if a.target.checker != nil {
		return a.target.checker(a.target.evidence, review)
	}
	dir := a.target.evidence
	if dir == "" {
		dir = a.dir
	}
	return repair.Check(dir, review)
}

func (st *state) phaseSpend(ctx context.Context, dir string, rounds int) *core.RefusalError {
	if st.twoFinders {
		return nil
	}
	return st.authorizeSpend(ctx, dir, rounds)
}

func (a *launchArgs) checkpoint() *core.RefusalError {
	if a.guard != nil {
		return a.guard()
	}
	return nil
}
