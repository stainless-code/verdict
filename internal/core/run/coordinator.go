package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stainless-code/verdict/internal/agentcatalog"
	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	panelcore "github.com/stainless-code/verdict/internal/core/panel"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	"github.com/stainless-code/verdict/internal/core/repair"
	"github.com/stainless-code/verdict/internal/jq"
	"github.com/stainless-code/verdict/internal/readcommon"
)

type coordinator struct {
	st       *state
	base     *launchArgs
	snapshot executionSnapshot
	review   *jq.Obj
	plan     *jq.Obj
	binding  *jq.Obj
	response *core.RunResponse
	dir      string
	source   string
	id       string
	reviews  []*jq.Obj
	failures []any
	rounds   int
}

// TwoFinders opts into the coordinator without changing the public CLI.
func TwoFinders(ctx context.Context, req *core.RunRequest) (*core.RunResponse, *core.RefusalError) {
	return execute(ctx, &state{req: req, twoFinders: true, warnings: append([]string{}, req.Warnings...)})
}

func (st *state) coordinate(ctx context.Context, base *launchArgs, rounds int) (*core.RunResponse, *core.RefusalError) {
	c := &coordinator{
		st: st, base: base, dir: base.target.evidence, id: repair.Nonce(), rounds: rounds,
		reviews: make([]*jq.Obj, finderCount), failures: []any{},
	}
	ref := c.run(ctx)
	c.executionResponse()
	return c.response, ref
}

func (c *coordinator) run(ctx context.Context) *core.RefusalError {
	if ref := c.initialize(ctx); ref != nil {
		return ref
	}
	if ref := c.runDiscovery(ctx); ref != nil {
		return ref
	}
	if ref := c.freeze(); ref != nil {
		return ref
	}
	if ref := c.runCross(ctx); ref != nil {
		return ref
	}
	return c.finish()
}

func (c *coordinator) initialize(ctx context.Context) *core.RefusalError {
	if ref := c.capture(); ref != nil {
		return ref
	}
	if ref := c.st.authorizeSpend(ctx, c.dir, c.rounds); ref != nil {
		return ref
	}
	var ref *core.RefusalError
	c.snapshot, ref = captureSnapshot(c.dir)
	return ref
}

// sessionFlags is the shared session-continuation vocabulary: flag names every
// shipped CLI reading that way continues a previous conversation. A row whose
// CLI spells continuation differently, or reuses one of these names for
// something else, declares it in Launch.ResumeArgs.
var sessionFlags = []string{"--resume", "--continue", "--session", "--session-id", "-r"}

// resumeArguments reports whether the operator's pass-through asks the agent to
// continue an earlier session, which would let a later finder read an earlier
// review's context. A bare subcommand matches too: Codex spells continuation
// `exec resume`.
func resumeArguments(def *agentcatalog.Launch, args []string) bool {
	for _, arg := range args {
		name, _, _ := strings.Cut(arg, "=")
		if slices.Contains(sessionFlags, name) || slices.Contains(def.ResumeArgs, name) {
			return true
		}
	}
	return false
}

func (c *coordinator) discover(ctx context.Context, index int) *core.RefusalError {
	name := fmt.Sprintf("x-finder-%d", index+1)
	output, ref := phaseDirectory(c.dir, name)
	if ref != nil {
		return ref
	}
	args, ref := c.discoveryArgs(output, index)
	if ref != nil {
		return ref
	}
	if ref := c.snapshot.check(c.dir); ref != nil {
		return ref
	}
	c.response, ref = c.st.invoke(ctx, args, c.rounds)
	if drift := c.phaseEnded(ctx); drift != nil {
		return drift
	}
	if ref != nil {
		c.failed(name, ref.Code)
	}
	c.reviews[index] = readDiscovery(args.abs)
	if c.reviews[index] == nil && ref == nil {
		c.failed(name, codeNoArtifact)
	}
	return c.snapshot.bindPhase(c.dir, name)
}

func readDiscovery(path string) *jq.Obj {
	raw, ok := crashReviewBytes(path)
	if !ok {
		return nil
	}
	normalized, _ := recordcore.NormalizeReview(raw)
	value, err := jq.DecodeOrdered(normalized)
	if err != nil {
		return nil
	}
	obj, ok := value.(*jq.Obj)
	if !ok {
		return nil
	}
	return obj
}

func (c *coordinator) phaseEnded(ctx context.Context) *core.RefusalError {
	if ctx.Err() != nil {
		return c.st.refuse(codeCanceled, msgCanceled)
	}
	return c.snapshot.check(c.dir)
}

func (c *coordinator) failed(phase, code string) {
	c.failures = append(c.failures, jq.NewObj("phase", phase, "code", code))
}

func (c *coordinator) freeze() *core.RefusalError {
	diff, err := os.ReadFile(filepath.Join(c.dir, "full.diff"))
	if err != nil {
		return c.st.refuse(codeMissingBundle, "diff missing")
	}
	var unionErr error
	c.review, c.plan, unionErr = discoveryUnion(c.id, diff, c.reviews)
	if unionErr != nil {
		return c.st.refuse(codeBadFlags, unionErr.Error())
	}
	if _, ref := phaseDirectory(c.dir, "verification"); ref != nil {
		return ref
	}
	if ref := writeCoordinator(c.dir, reviewFile, c.review); ref != nil {
		return ref
	}
	c.plan.Set("schema_version", "2")
	c.plan.Set("execution_id", c.id)
	c.plan.Set("head_sha", readcommon.OpenBundle(c.dir).Head())
	c.plan.Set("diff_sha256", shaHex(diff))
	c.plan.Set("review_sha256", shaHex(coordinatorBytes(c.review)))
	planPath := filepath.Join("verification", "plan.json")
	if ref := writeCoordinator(c.dir, planPath, c.plan); ref != nil {
		return ref
	}
	c.binding = jq.NewObj("schema_version", "2", "execution_id", c.id, "head_sha", readcommon.OpenBundle(c.dir).Head(),
		"diff_sha256", shaHex(diff), "review_sha256", shaHex(coordinatorBytes(c.review)),
		"plan_sha256", shaHex(coordinatorBytes(c.plan)), "results", []any{})
	return c.snapshot.bind(c.dir, reviewFile, planPath)
}

func (c *coordinator) finish() *core.RefusalError {
	if ref := writeCoordinator(c.dir, filepath.Join("verification", "verdicts.json"), c.binding); ref != nil {
		return ref
	}
	manifest := cloneUnionRow(c.binding)
	manifest.Set("finders", 2)
	manifest.Set("finder_source", c.source)
	setManifestBudget(manifest, c.st.budget)
	manifest.Set("results", nil)
	manifest.Set("failures", c.failures)
	manifest.Set("artifacts", c.snapshot)
	manifest.Set("profiles", []any{
		jq.NewObj("finder", "finder-1", "profile", "contract-first"),
		jq.NewObj("finder", "finder-2", "profile", "behavior-first"),
	})
	status := "complete"
	if len(c.failures) > 0 {
		status = "partial"
	}
	manifest.Set("status", status)
	if ref := c.snapshot.check(c.dir); ref != nil {
		return ref
	}
	if ref := writeCoordinator(c.dir, launcherFile, manifest); ref != nil {
		return ref
	}
	return c.validateAggregate()
}

// setManifestBudget carries the review-budget decision when one participated.
// A nil budget leaves the key out entirely, so a launch that named no depth
// keeps its manifest byte-identical.
func setManifestBudget(manifest *jq.Obj, recorded *core.ReviewBudget) {
	if recorded == nil {
		return
	}
	manifest.Set("budget", core.Wire(recorded))
}

func (c *coordinator) validateAggregate() *core.RefusalError {
	execution := panelcore.CheckExecution(c.dir, nil)
	if execution.Problem() != "" {
		return c.st.refuse("review-insufficient", execution.Problem())
	}
	check, ref := repair.Check(c.dir, coordinatorBytes(c.review))
	if ref != nil {
		return ref
	}
	if ref := recordcore.ReviewRefusal(check, false); ref != nil {
		return ref
	}
	if ref := repair.InsufficientRefusal(check, ""); ref != nil {
		return ref
	}
	c.aggregateResponse()
	return nil
}

func (c *coordinator) runDiscovery(ctx context.Context) *core.RefusalError {
	for i := range finderCount {
		if ref := c.discover(ctx, i); ref != nil {
			return ref
		}
	}
	return nil
}

func (c *coordinator) runCross(ctx context.Context) *core.RefusalError {
	for i := range finderCount {
		if ref := c.cross(ctx, i); ref != nil {
			return ref
		}
	}
	return nil
}

func (c *coordinator) capture() *core.RefusalError {
	captured, readRef := readFinderCapture(c.dir)
	if readRef != nil {
		return readRef
	}
	if captured == nil {
		return c.st.refuse(codeMissingBundle, "two finders require a captured run")
	}
	_, source, ref := c.st.finderPolicy(captured)
	if ref != nil {
		return ref
	}
	c.source = source
	captured.Finders = 2
	captured.FinderSource = source
	captured.Execution = &core.ExecutionReference{ID: c.id, Finders: finderCount}
	if err := config.WriteRunState(c.dir, captured); err != nil {
		return c.st.refuse(codeRecordFailed, fmt.Sprintf("capture execution: %v", err))
	}
	manifest := jq.NewObj("schema_version", "2", "execution_id", c.id, "status", "in-progress",
		"finders", 2, "finder_source", captured.FinderSource)
	setManifestBudget(manifest, c.st.budget)
	return writeCoordinator(c.dir, launcherFile, manifest)
}

func (c *coordinator) discoveryArgs(output string, index int) (*launchArgs, *core.RefusalError) {
	procedure, ref := finderProcedure(c.dir, output, index)
	if ref != nil {
		return nil, ref
	}
	if err := atomic.WriteFileMode(filepath.Join(output, "procedure.md"), procedure, roundEvidenceMode); err != nil {
		return nil, c.st.refuse(codeRecordFailed, fmt.Sprintf("write finder procedure: %v", err))
	}
	target := launchTarget{
		evidence: c.dir, output: output, procedure: filepath.Join(output, "procedure.md"),
		artifact: reviewFile, checker: recordcore.CheckDiscovery,
	}
	if ref := c.snapshot.bind(c.dir, filepath.Join(filepath.Base(output), "procedure.md")); ref != nil {
		return nil, ref
	}
	args, ref := c.st.argsForTarget(&c.base.def, c.base.timeout, target)
	if ref == nil {
		args.guard = c.checkpoint(output)
	}
	return args, ref
}

// aggregateResponse points the response at the aggregate artifacts the
// coordinator wrote and recomputes the handoff from the bundle itself: the
// retained child response named a finder's phase directory, which is not the
// operator's next step.
func (c *coordinator) aggregateResponse() {
	if c.response == nil || c.response.Data == nil {
		return
	}
	c.response.Next = c.st.next(c.dir)
	c.response.Data.Artifact = readcommon.DisplayPath(c.st.req.RepoRootPath(), filepath.Join(c.dir, reviewFile))
	c.response.Data.Record = readcommon.DisplayPath(c.st.req.RepoRootPath(), filepath.Join(c.dir, launcherFile))
}

func (c *coordinator) checkpoint(output string) func() *core.RefusalError {
	return func() *core.RefusalError {
		if ref := c.snapshot.check(c.dir); ref != nil {
			return ref
		}
		return rejectPhaseLinks(output)
	}
}

func (c *coordinator) executionResponse() {
	if c.snapshot == nil {
		return
	}
	if c.response == nil {
		c.response = &core.RunResponse{Repo: c.st.req.Repo, Pr: c.st.req.Pr, At: core.NowUTC(c.st.now())}
	}
	if c.response.Data == nil {
		argv, _ := c.st.displayArgv(c.base)
		c.response.Data = &core.RunData{
			Agent: c.base.def.Name, Binary: c.base.bin, Argv: argv,
			Bundle: readcommon.DisplayPath(c.st.req.RepoRootPath(), c.dir), Timeout: c.base.timeout,
			Budget: c.st.budget,
		}
	}
	execution := panelcore.CheckExecution(c.dir, nil)
	counts := execution.Attach(nil).Finders
	failures := make([]core.RunPhaseFailure, 0, len(c.failures))
	for _, row := range c.failures {
		failures = append(failures, core.RunPhaseFailure{
			Phase: jq.StrOf(readcommon.FieldOf(row, "phase")), Code: jq.StrOf(readcommon.FieldOf(row, "code")),
		})
	}
	c.response.Data.Execution = &core.RunExecution{
		ID: c.id, Status: readcommon.JSONStr(filepath.Join(c.dir, launcherFile), "status"),
		Counts: counts, Failures: failures,
	}
	c.aggregateResponse()
}
