package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/stainless-code/verdict/internal/atomic"
	"github.com/stainless-code/verdict/internal/config"
	"github.com/stainless-code/verdict/internal/core"
	adoptcore "github.com/stainless-code/verdict/internal/core/adopt"
	calibratecore "github.com/stainless-code/verdict/internal/core/calibrate"
	checktreecore "github.com/stainless-code/verdict/internal/core/checktree"
	doctorcore "github.com/stainless-code/verdict/internal/core/doctor"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	fixcore "github.com/stainless-code/verdict/internal/core/fix"
	indexcore "github.com/stainless-code/verdict/internal/core/index"
	initcore "github.com/stainless-code/verdict/internal/core/init"
	learncore "github.com/stainless-code/verdict/internal/core/learn"
	postcore "github.com/stainless-code/verdict/internal/core/post"
	readycore "github.com/stainless-code/verdict/internal/core/ready"
	recordcore "github.com/stainless-code/verdict/internal/core/record"
	reportcore "github.com/stainless-code/verdict/internal/core/report"
	trendcore "github.com/stainless-code/verdict/internal/core/trend"
	triagecore "github.com/stainless-code/verdict/internal/core/triage"
	verifycore "github.com/stainless-code/verdict/internal/core/verify"
	"github.com/stainless-code/verdict/internal/jq"
)

var fetchRunner = coreexec.Runner{OutputCap: coreexec.FetchOutputCap, MissingTool: true}

const (
	argRepo            = "repo"
	argPr              = "pr"
	argFlags           = "flags"
	argStatus          = "status"
	fieldRepo          = "Repo"
	fieldPr            = "Pr"
	msgRequestMismatch = "tool request type mismatch"
	artifactPerm       = 0o600
	codeBadFlags       = "bad-flags"
)

var (
	requiredRepoPr  = []string{argRepo, argPr}
	omitPositionals = map[string]bool{fieldRepo: true, fieldPr: true}
)

type toolMeta struct {
	title              string
	readOnly           bool
	destructive        bool
	idempotent         bool
	openWorld          bool
	servedWhenReadOnly bool
	readOnlyDryRun     bool
}

type toolSpec struct {
	name        string
	description string
	omit        map[string]bool
	writeArgs   map[string]string
	required    []string
	meta        toolMeta
}

type toolDeps struct {
	commander core.Commander
}

type tool struct { //nolint:govet // the grouping documents the descriptor; alignment is irrelevant
	name               string
	title              string
	description        string
	request            schemaSource
	omit               map[string]bool
	required           []string
	deps               toolDeps
	readOnly           bool
	servedWhenReadOnly bool
	readOnlyDryRun     bool
	requireDryRun      bool
	destructive        bool
	idempotent         bool
	openWorld          bool
	operatorOnly       bool
	operatorWrite      func(any) bool
	writeArgs          map[string]string
	refusedArgs        map[string]bool
	decode             func(json.RawMessage) (any, error)
	wire               func(context.Context, *bindings, any) error
	invoke             func(context.Context, any) (*jq.Obj, *core.RefusalError)
}

//nolint:revive // the registry reads as one block
func allTools(readOnly, operator bool) []*tool {
	tools := []*tool{
		dataTool(&toolSpec{
			name: "adopt",
			description: "Copy findings from a local bundle into the target PR bundle " +
				"and re-anchor them. Call after a local review to post it on a PR. " +
				"Writes the target bundle's findings store.",
			required: []string{argRepo, argPr, "from"},
			meta:     toolMeta{title: "Adopt findings", idempotent: true},
		}, toolDeps{}, decodeAdopt, wireAdopt, adoptcore.Run,
			func(resp *adoptcore.Response) *jq.Obj { return wireData(resp.Data) }),
		dataTool(&toolSpec{
			name: "calibrate",
			description: "Join stored findings against live thread and file state, " +
				"or grade the offline probe. Call after posting to measure precision. " +
				"Reads GitHub live; the probe rewrites kb-grades.jsonl.",
			required: requiredRepoPr,
			meta:     toolMeta{title: "Calibrate findings", idempotent: true, openWorld: true},
		}, toolDeps{commander: coreexec.Runner{}}, decodeCalibrate, wireCalibrate, calibratecore.Run,
			func(resp *calibratecore.Response) *jq.Obj { return wireData(resp.Data) }),
		directTool(&toolSpec{
			name: "check-tree",
			description: "Gate on a clean git worktree, allowing the paths in expect. " +
				"Call before finalizing when stray edits must fail the review. " +
				"Reads git only; writes nothing.",
			omit: omitPositionals,
			meta: toolMeta{title: "Check worktree", readOnly: true, idempotent: true},
		}, toolDeps{commander: coreexec.Runner{}}, decodeCheckTree, wireCheckTree, invokeCheckTree),
		directTool(&toolSpec{
			name: "config",
			description: "Explain the stored effective policy of a fetched run, or preview " +
				"a proposed configuration file with current settings and environment. " +
				"Call before acting on policy to see values, requirements, plan, and spend. " +
				"Reads the run bundle only; writes nothing and never approves.",
			required: requiredRepoPr,
			meta:     toolMeta{title: "Explain policy", readOnly: true, idempotent: true},
		}, toolDeps{}, decodeConfig, wireConfig, invokeConfig),
		dataTool(&toolSpec{
			name: "doctor",
			description: "Preflight the configured code host's CLI and auth, git, and storage. " +
				"Call first when a verb fails for environmental reasons; provider picks the host " +
				"(default github). Reads local tools and config; writes nothing.",
			omit: omitPositionals,
			meta: toolMeta{title: "Preflight environment", readOnly: true, idempotent: true},
		}, toolDeps{commander: coreexec.Runner{}}, decodeDoctor, wireDoctor, doctorcore.Run,
			func(resp *doctorcore.Response) *jq.Obj { return wireData(resp.Data) }),
		directTool(&toolSpec{
			name: "fetch",
			description: "Fetch a PR into .verdict/state/<pr> (diff, metadata, threads, prompt input). " +
				"Call first for any PR review; an over-cap diff refuses, so re-run with chunk=true. " +
				"Writes the bundle and calls the PR's host.",
			required: requiredRepoPr,
			meta:     toolMeta{title: "Fetch pull request", idempotent: true, openWorld: true},
		}, toolDeps{commander: fetchRunner}, decodeFetch, wireFetch, invokeFetch),
		directTool(&toolSpec{
			name: "fetch-local",
			description: "Fetch a local git diff into .verdict/state/local-<name>. Call to review " +
				"uncommitted or branch changes without a PR. Writes the bundle; no network.",
			required: requiredRepoPr,
			meta:     toolMeta{title: "Fetch local diff", idempotent: true},
		}, toolDeps{commander: fetchRunner}, decodeFetchLocal, wireFetchLocal, invokeFetchLocal),
		dataTool(&toolSpec{
			name: "fix",
			description: "Run one bounded, verify-gated fix pass over an inline finding. " +
				"Call after review to prepare one finding's patch; the ledger caps passes. " +
				"Writes the fix ledger and runs the recorded verify command.",
			required: requiredRepoPr,
			meta:     toolMeta{title: "Prepare a fix"},
		}, toolDeps{commander: fetchRunner}, decodeFix, wireFix, fixcore.Run,
			func(resp *fixcore.Response) *jq.Obj { return wireData(resp.Data) }),
		directTool(&toolSpec{
			name: "index",
			description: "Query the local Go index under .verdict/index for callers, candidate tests, " +
				"co-change partners, CODEOWNERS owners, or stored trees. Call before editing code to " +
				"find callers and the tests to read. Reads the index and git objects only; never " +
				"builds or prunes (those are CLI-only) and writes nothing.",
			omit: omitPositionals,
			meta: toolMeta{title: "Query code index", readOnly: true, idempotent: true},
		}, toolDeps{commander: coreexec.Runner{}}, decodeIndex, wireIndex, invokeIndex),
		dataTool(&toolSpec{
			name: "init",
			description: "Scaffold .verdict control files from a style preset. Call once in a " +
				"new repository; yes=true overwrites existing controls. Writes controls and " +
				"runs a tree check.",
			omit: omitPositionals,
			meta: toolMeta{title: "Scaffold controls", destructive: true, idempotent: true},
		}, toolDeps{commander: fetchRunner}, decodeInit, wireInit, initcore.Run,
			func(resp *initcore.Response) *jq.Obj { return wireData(resp.Data) }),
		dataTool(&toolSpec{
			name: "learn",
			description: "Read or update the local knowledge base. Call to list rules or " +
				"add, retire, or restore one. Writes .verdict/kb.jsonl.",
			omit: omitPositionals,
			meta: toolMeta{title: "Manage knowledge base", idempotent: true},
		}, toolDeps{}, decodeLearn, wireLearn, learncore.Run,
			func(resp *learncore.Response) *jq.Obj { return wireData(resp.Data) }),
		directTool(&toolSpec{
			name: "next",
			description: "Return the top actionable finding. Call after fetch to pick the next " +
				"review or fix target. Reads the bundle; writes nothing.",
			required: requiredRepoPr,
			meta:     toolMeta{title: "Next finding", readOnly: true, idempotent: true},
		}, toolDeps{}, decodeNext, wireNext, invokeNext),
		dataTool(&toolSpec{
			name: "post",
			description: "Post exactly one review to GitHub, GitLab, or Forgejo, or preview with dryRun. " +
				"Call once after review.json is final; never write a review note. A secret-shaped " +
				"value refuses post-secret-shaped (allowSecretValues publishes it). Writes post.json; " +
				"GitLab posts a bound request-changes as a comment, recorded in withheld[].",
			required: requiredRepoPr,
			meta:     toolMeta{title: "Post review", destructive: true, idempotent: true, openWorld: true},
		}, toolDeps{commander: fetchRunner}, decodePost, wirePost, postcore.Run,
			func(resp *core.PostResponse) *jq.Obj { return wireData(resp.Data) }),
		dataTool(&toolSpec{
			name: "ready",
			description: "Report merge readiness (checks, threads, draft, conflict) or gate on " +
				"it. Call before merging. Reads GitHub; writes nothing.",
			required: requiredRepoPr,
			meta:     toolMeta{title: "Merge readiness", readOnly: true, idempotent: true, openWorld: true},
		}, toolDeps{commander: coreexec.Runner{}}, decodeReady, wireReady, readycore.Run,
			func(resp *readycore.Response) *jq.Obj { return wireData(resp.Data) }),
		dataTool(&toolSpec{
			name: "record",
			description: "Validate review.json and merge it into the findings and record stores. " +
				"Call for local bundles that cannot post; writes findings.json and record.json. " +
				"Set dry_run to repeat every check without writing, fix each problem, and after 3 rounds report any that remain instead of recording.",
			required: requiredRepoPr,
			meta:     toolMeta{title: "Record review", idempotent: true, servedWhenReadOnly: true, readOnlyDryRun: true},
		}, toolDeps{}, decodeRecord, wireRecord, recordcore.Run,
			func(resp *core.RecordResponse) *jq.Obj { return wireData(resp.Data) }),
		directTool(&toolSpec{
			name: "report",
			description: "Render the offline review report (ranked findings, counts, markdown/SARIF). " +
				"Call after fetch or record; the follow-up is next. Reads the bundle and writes " +
				"only artifacts you request.",
			writeArgs: map[string]string{"Output": "output", "Sarif": "sarif"},
			required:  requiredRepoPr,
			meta:      toolMeta{title: "Render report", idempotent: true, servedWhenReadOnly: true},
		}, toolDeps{}, decodeReport, wireReport, invokeReport),
		directTool(&toolSpec{
			name: "show",
			description: "Return one full finding by signature or unique prefix. Call to inspect " +
				"a finding named by report or next. Reads the bundle; writes nothing.",
			required: []string{argRepo, argPr, "sig"},
			meta:     toolMeta{title: "Show finding", readOnly: true, idempotent: true},
		}, toolDeps{}, decodeShow, wireShow, invokeShow),
		dataTool(&toolSpec{
			name: "threads",
			description: "Refresh the normalized review-thread feed " +
				"(.verdict/state/<pr>/threads.json) from the host, or with resolve close verdict-authored " +
				"threads. Call after pushing fixes to re-read resolution state before a re-review or merge readiness. " +
				"Reads the host; writes threads.json and, on resolve, threads-resolve.json.",
			required: requiredRepoPr,
			meta:     toolMeta{title: "Refresh review threads", idempotent: true, openWorld: true},
		}, toolDeps{commander: fetchRunner}, decodeThreads, wireThreads, fetchcore.RunThreads,
			wireThreadsData),
		dataTool(&toolSpec{
			name: "trend",
			description: "Roll up review outcomes across fetched bundles. Call for a cross-PR " +
				"summary. Reads local bundles; writes nothing.",
			omit:     map[string]bool{fieldPr: true},
			required: []string{argRepo},
			meta:     toolMeta{title: "Review trend", readOnly: true, idempotent: true},
		}, toolDeps{}, decodeTrend, wireTrend, trendcore.Run,
			func(resp *trendcore.Response) *jq.Obj { return wireData(resp.Data) }),
		operatorTool(dataTool(&toolSpec{
			name: "triage",
			description: "Operator-only: flip one finding's status with a " +
				"rationale. Never call while reviewing a PR; propose triage[] in review.json " +
				"instead. Writes findings.json and stages KB credit.",
			required: []string{argRepo, argPr, "sig", argStatus},
			meta:     toolMeta{title: "Triage finding", destructive: true},
		}, toolDeps{}, decodeTriage, wireTriage, triagecore.Run,
			func(resp *triagecore.Response) *jq.Obj { return wireData(resp.Data) })),
		dataTool(&toolSpec{
			name: "verify",
			description: "Run the recorded verifyCmd under the watchdog and record the proof. " +
				"Pass group and command together to validate one deep plan group at the reviewed root, " +
				"or set sandbox true for enforced bubblewrap isolation on Linux (refused where unenforceable). " +
				"Never fabricates a patch or a command from configuration.",
			required: requiredRepoPr,
			meta:     toolMeta{title: "Run verification", destructive: true, openWorld: true},
		}, toolDeps{commander: coreexec.Watchdog{}}, decodeVerify, wireVerify, verifycore.Run,
			func(resp *core.VerifyResponse) *jq.Obj { return wireData(resp.Data) }),
	}
	for _, t := range tools {
		if t.name == "threads" {
			t.operatorWrite = threadsResolveWrite
		}
	}
	kept := make([]*tool, 0, len(tools))
	for _, t := range tools {
		if readOnly {
			t.narrowReadOnly()
			if t.readOnlyDryRun {
				t.requireDryRun = true
			}
		}
		if !exposed(t, readOnly, operator) {
			continue
		}
		kept = append(kept, t)
	}
	return kept
}

func (t *tool) narrowReadOnly() {
	if len(t.writeArgs) == 0 {
		return
	}
	omit := make(map[string]bool, len(t.omit)+len(t.writeArgs))
	maps.Copy(omit, t.omit)
	for field, arg := range t.writeArgs {
		omit[field] = true
		t.refusedArgs[arg] = true
	}
	t.omit = omit
}

func exposed(t *tool, readOnly, operator bool) bool {
	if readOnly && !t.readOnly && !t.servedWhenReadOnly {
		return false
	}
	return !t.operatorOnly || (!readOnly && operator)
}

func operatorTool(t *tool) *tool {
	t.operatorOnly = true
	return t
}

func dataTool[Req, S any](
	spec *toolSpec, deps toolDeps,
	decode func(*Req, *args), wire func(context.Context, *bindings, *Req) error,
	svc func(context.Context, *Req) (*S, *core.RefusalError),
	data func(*S) *jq.Obj,
) *tool {
	return newTool(spec, deps, decode, wire, func(ctx context.Context, req *Req) (*jq.Obj, *core.RefusalError) {
		return invokeService(ctx, req, svc, data)
	})
}

func directTool[Req any](
	spec *toolSpec, deps toolDeps,
	decode func(*Req, *args), wire func(context.Context, *bindings, *Req) error,
	run func(context.Context, *Req) (*jq.Obj, *core.RefusalError),
) *tool {
	return newTool(spec, deps, decode, wire, run)
}

func newTool[Req any](
	spec *toolSpec, deps toolDeps,
	decode func(*Req, *args), wire func(context.Context, *bindings, *Req) error,
	run func(context.Context, *Req) (*jq.Obj, *core.RefusalError),
) *tool {
	t := &tool{
		name:               spec.name,
		title:              spec.meta.title,
		description:        spec.description,
		request:            schemaOf[Req](),
		omit:               spec.omit,
		writeArgs:          spec.writeArgs,
		required:           spec.required,
		deps:               deps,
		readOnly:           spec.meta.readOnly,
		servedWhenReadOnly: spec.meta.servedWhenReadOnly,
		readOnlyDryRun:     spec.meta.readOnlyDryRun,
		destructive:        spec.meta.destructive,
		idempotent:         spec.meta.idempotent,
		openWorld:          spec.meta.openWorld,
		operatorOnly:       false,
		refusedArgs:        map[string]bool{},
	}
	t.decode = func(raw json.RawMessage) (any, error) {
		return decodeRequest[Req](raw, spec.required, decode, t.refusedArgs)
	}
	t.wire = func(ctx context.Context, b *bindings, raw any) error {
		if req, ok := raw.(*Req); ok {
			return wire(ctx, b, req)
		}
		return nil
	}
	t.invoke = func(ctx context.Context, raw any) (*jq.Obj, *core.RefusalError) {
		req, ok := raw.(*Req)
		if !ok {
			return nil, internalRefusal()
		}
		if t.requireDryRun && !requestDryRun(raw) {
			return nil, &core.RefusalError{Code: codeBadFlags, Message: "record writes on a read-only server; re-run with dry_run=true"}
		}
		return run(ctx, req)
	}
	return t
}

// wireThreadsData renders either half of the threads tool: the refreshed feed
// tally or the resolve receipt profile.
func wireThreadsData(resp *fetchcore.ThreadsResponse) *jq.Obj {
	if resp.Resolve != nil {
		return resp.Resolve.Wire()
	}
	return wireData(resp.Data)
}

// threadsResolveWrite reports whether one threads request is the operator-only
// resolve mode, so the server can refuse it without --operator.
func threadsResolveWrite(raw any) bool {
	req, ok := raw.(*fetchcore.ThreadsRequest)
	return ok && req.Resolve
}

func invokeService[R, S any](
	ctx context.Context, req *R,
	svc func(context.Context, *R) (*S, *core.RefusalError),
	data func(*S) *jq.Obj,
) (*jq.Obj, *core.RefusalError) {
	resp, ref := svc(ctx, req)
	if ref != nil {
		return nil, ref
	}
	if resp == nil || data == nil {
		return jq.NewObj(), nil
	}
	return data(resp), nil
}

func invokeReport(ctx context.Context, req *core.ReportRequest) (*jq.Obj, *core.RefusalError) {
	resp, ref := reportcore.Run(ctx, req)
	if ref != nil {
		return nil, ref
	}
	if ref := writeArtifact(resp.Output); ref != nil {
		return nil, ref
	}
	if ref := writeArtifact(resp.Sarif); ref != nil {
		return nil, ref
	}
	return wireData(resp.Data), nil
}

func invokeIndex(ctx context.Context, req *indexcore.Request) (*jq.Obj, *core.RefusalError) {
	if !slices.Contains(indexcore.MCPQueries, req.Query) {
		return nil, &core.RefusalError{
			Code:    codeBadFlags,
			Message: "index serves callers, status, tests-for, co-changed, and owners; build and prune are CLI-only",
		}
	}
	resp, ref := indexcore.Run(ctx, req)
	if ref != nil {
		return nil, ref
	}
	return resp.Data.Wire(), nil
}

func invokeNext(ctx context.Context, req *core.NextRequest) (*jq.Obj, *core.RefusalError) {
	// coverage is a read mode, not a decoration: dispatching it to RunNext
	// would return the findings item and silently ignore the request.
	var resp *core.NextResponse
	var ref *core.RefusalError
	if req.Coverage {
		resp, ref = reportcore.RunNextCoverage(req)
	} else {
		resp, ref = reportcore.RunNext(ctx, req)
	}
	if ref != nil {
		return nil, ref
	}
	if req.Coverage {
		return jq.NewObj("coverage", resp.Coverage, "human", resp.Human,
			"warnings", resp.Warnings), nil
	}
	var item any
	if resp.Item != nil {
		item = resp.Item.Wire()
	}
	return jq.NewObj("item", item), nil
}

func invokeShow(ctx context.Context, req *core.ShowRequest) (*jq.Obj, *core.RefusalError) {
	resp, ref := reportcore.RunShow(ctx, req)
	if ref != nil {
		return nil, ref
	}
	var item any
	if resp.Item != nil {
		item = resp.Item.Raw
	}
	data := jq.NewObj("item", item)
	if resp.Fix != nil {
		data.Set("fix", resp.Fix.Wire())
	}
	return data, nil
}

func invokeConfig(ctx context.Context, req *core.ConfigRequest) (*jq.Obj, *core.RefusalError) {
	if req.Flags.Explain == req.Flags.Preview {
		return nil, &core.RefusalError{Code: codeBadFlags, Message: "config needs exactly one of explain or preview"}
	}
	if req.Flags.File != "" && !req.Flags.Preview {
		return nil, &core.RefusalError{Code: codeBadFlags, Message: "config file needs preview"}
	}
	resp, ref := config.Run(ctx, req)
	if ref != nil {
		return nil, ref
	}
	return wireData(resp.Data), nil
}

func invokeCheckTree(ctx context.Context, req *checktreecore.Request) (*jq.Obj, *core.RefusalError) {
	resp, ref := checktreecore.Run(ctx, req)
	if ref != nil {
		return nil, ref
	}
	if resp.Error != nil {
		return nil, &core.RefusalError{Code: resp.Error.Code, Message: resp.Error.Message}
	}
	return wireData(resp.Data), nil
}

func invokeFetch(ctx context.Context, req *core.FetchRequest) (*jq.Obj, *core.RefusalError) {
	resp, err := fetchcore.Run(ctx, req)
	if err != nil {
		return nil, commandRefusal(err)
	}
	return wireData(resp.Data), nil
}

func invokeFetchLocal(ctx context.Context, req *core.FetchLocalRequest) (*jq.Obj, *core.RefusalError) {
	resp, err := fetchcore.RunLocal(ctx, req)
	if err != nil {
		return nil, commandRefusal(err)
	}
	return wireData(resp.Data), nil
}

func commandRefusal(err error) *core.RefusalError {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return ref
	}
	if missing, ok := errors.AsType[*core.MissingToolError](err); ok {
		return &core.RefusalError{Code: "missing-tool", Message: "missing: " + missing.Tool}
	}
	return &core.RefusalError{Code: "fetch-failed", Message: err.Error()}
}

func writeArtifact(a *core.FileArtifact) *core.RefusalError {
	if a == nil {
		return nil
	}
	if err := atomic.WriteThrough(a.Path, a.Content, artifactPerm); err != nil {
		return &core.RefusalError{
			Code:    "report-failed",
			Message: fmt.Sprintf("could not write %s: %v", a.Path, err),
		}
	}
	return nil
}

func wireData(v any) *jq.Obj {
	if v == nil {
		return jq.NewObj()
	}
	if o, ok := core.Wire(v).(*jq.Obj); ok {
		return o
	}
	return jq.NewObj()
}

func requestDryRun(raw any) bool {
	req, ok := raw.(*core.RecordRequest)
	return ok && req.DryRun
}

func internalRefusal() *core.RefusalError {
	return &core.RefusalError{Code: "internal", Message: msgRequestMismatch}
}

func rawMarshal(o *jq.Obj) json.RawMessage { return json.RawMessage(o.MarshalCompact()) }

// ToolNames lists the tool names a server with this surface exposes, in
// registry order. `verdict mcp --help` is checked against it.
func ToolNames(readOnly, operator bool) []string {
	tools := allTools(readOnly, operator)
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.name)
	}
	return out
}
