package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/stainless-code/verdict/internal/core"
	adoptcore "github.com/stainless-code/verdict/internal/core/adopt"
	calibratecore "github.com/stainless-code/verdict/internal/core/calibrate"
	checktreecore "github.com/stainless-code/verdict/internal/core/checktree"
	doctorcore "github.com/stainless-code/verdict/internal/core/doctor"
	coreexec "github.com/stainless-code/verdict/internal/core/exec"
	fetchcore "github.com/stainless-code/verdict/internal/core/fetch"
	fixcore "github.com/stainless-code/verdict/internal/core/fix"
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

// Argument keys and adapter constants shared by the registry.
const (
	argRepo            = "repo"
	argPr              = "pr"
	argFlags           = "flags"
	argStatus          = "status"
	fieldRepo          = "Repo"
	fieldPr            = "Pr"
	msgRequestMismatch = "tool request type mismatch"
	artifactPerm       = 0o600
)

// requiredRepoPr is the common required-argument pair, and omitPositionals
// hides repo/pr from verbs that take no positionals.
var (
	requiredRepoPr  = []string{argRepo, argPr}
	omitPositionals = map[string]bool{fieldRepo: true, fieldPr: true}
)

// toolMeta is the descriptor half of a tool: the human title and the four
// MCP behavior hints. readOnly is also the access policy the CLI filters on.
type toolMeta struct {
	title       string
	readOnly    bool
	destructive bool
	idempotent  bool
	openWorld   bool
}

// toolSpec is the policy half of one registry entry.
type toolSpec struct {
	name        string
	description string
	omit        map[string]bool
	required    []string
	meta        toolMeta
}

// toolDeps is the dependency half of one registry entry: the runners the
// verb's CLI adapter would install. Production passes the concrete commander
// per tool; tests substitute fakes.
type toolDeps struct {
	commander core.Commander
}

// tool is one MCP tool: the request type its schema comes from, the field
// policy that hides adapter-owned members, and the explicit decode, inject,
// and service seams for that type.
type tool struct { //nolint:govet // the grouping documents the descriptor; alignment is irrelevant
	name         string
	title        string
	description  string
	request      schemaSource
	omit         map[string]bool
	required     []string
	deps         toolDeps
	readOnly     bool
	destructive  bool
	idempotent   bool
	openWorld    bool
	operatorOnly bool
	decode       func(json.RawMessage) (any, error)
	wire         func(*bindings, any)
	invoke       func(context.Context, any) (*jq.Obj, *core.RefusalError)
}

// allTools returns the registered tool set for one surface. readOnly keeps
// only non-writing tools; operator adds the human-owned verbs. The CLI
// refuses the two together, and the filter order here makes that impossible
// to violate from the library either. upgrade/skill are distribution
// surfaces, never review tools.
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
		}, toolDeps{commander: coreexec.SpawnCommander{}}, decodeCalibrate, wireCalibrate, calibratecore.Run,
			func(resp *calibratecore.Response) *jq.Obj { return wireData(resp.Data) }),
		directTool(&toolSpec{
			name: "check-tree",
			description: "Gate on a clean git worktree, allowing the paths in expect. " +
				"Call before finalizing when stray edits must fail the review. " +
				"Reads git only; writes nothing.",
			omit: omitPositionals,
			meta: toolMeta{title: "Check worktree", readOnly: true, idempotent: true},
		}, toolDeps{commander: coreexec.SpawnCommander{}}, decodeCheckTree, wireCheckTree, invokeCheckTree),
		dataTool(&toolSpec{
			name: "doctor",
			description: "Preflight gh auth, git, and storage. Call first when a verb " +
				"fails for environmental reasons. Reads local tools and config; writes nothing.",
			omit: omitPositionals,
			meta: toolMeta{title: "Preflight environment", readOnly: true, idempotent: true},
		}, toolDeps{commander: coreexec.SpawnCommander{}}, decodeDoctor, wireDoctor, doctorcore.Run,
			func(resp *doctorcore.Response) *jq.Obj { return wireData(resp.Data) }),
		directTool(&toolSpec{
			name: "fetch",
			description: "Fetch a PR into .review/<pr> (diff, metadata, threads, prompt input). " +
				"Call first for any PR review; an over-cap diff refuses, so re-run with chunk=true. " +
				"Writes the bundle and calls GitHub.",
			required: requiredRepoPr,
			meta:     toolMeta{title: "Fetch pull request", idempotent: true, openWorld: true},
		}, toolDeps{commander: coreexec.SplitCommander{}}, decodeFetch, wireFetch, invokeFetch),
		directTool(&toolSpec{
			name: "fetch-local",
			description: "Fetch a local git diff into .review/local-<name>. Call to review " +
				"uncommitted or branch changes without a PR. Writes the bundle; no network.",
			required: requiredRepoPr,
			meta:     toolMeta{title: "Fetch local diff", idempotent: true},
		}, toolDeps{commander: coreexec.SplitCommander{}}, decodeFetchLocal, wireFetchLocal, invokeFetchLocal),
		dataTool(&toolSpec{
			name: "fix",
			description: "Run one bounded, verify-gated fix pass over an inline finding. " +
				"Call after review to prepare one finding's patch; the ledger caps passes. " +
				"Writes the fix ledger and runs the recorded verify command.",
			required: requiredRepoPr,
			meta:     toolMeta{title: "Prepare a fix"},
		}, toolDeps{commander: coreexec.SplitCommander{}}, decodeFix, wireFix, fixcore.Run,
			func(resp *fixcore.Response) *jq.Obj { return wireData(resp.Data) }),
		dataTool(&toolSpec{
			name: "init",
			description: "Scaffold .review control files from a style preset. Call once in a " +
				"new repository; yes=true overwrites existing controls. Writes controls and " +
				"runs a tree check.",
			omit: omitPositionals,
			meta: toolMeta{title: "Scaffold controls", destructive: true, idempotent: true},
		}, toolDeps{commander: coreexec.SplitCommander{}}, decodeInit, wireInit, initcore.Run,
			func(resp *initcore.Response) *jq.Obj { return wireData(resp.Data) }),
		dataTool(&toolSpec{
			name: "learn",
			description: "Read or update the local knowledge base. Call to list rules or " +
				"add, retire, or restore one. Writes .review/kb.jsonl.",
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
			description: "Post exactly one review to GitHub, or preview with dryRun. Call once " +
				"after review.json is final; never write a review note. Writes post.json and " +
				"sends to GitHub.",
			required: requiredRepoPr,
			meta:     toolMeta{title: "Post review", destructive: true, idempotent: true, openWorld: true},
		}, toolDeps{commander: coreexec.SplitCommander{}}, decodePost, wirePost, postcore.Run,
			func(resp *core.PostResponse) *jq.Obj { return wireData(resp.Data) }),
		dataTool(&toolSpec{
			name: "ready",
			description: "Report merge readiness (checks, threads, draft, conflict) or gate on " +
				"it. Call before merging. Reads GitHub; writes nothing.",
			required: requiredRepoPr,
			meta:     toolMeta{title: "Merge readiness", readOnly: true, idempotent: true, openWorld: true},
		}, toolDeps{commander: coreexec.SpawnCommander{}}, decodeReady, wireReady, readycore.Run,
			func(resp *readycore.Response) *jq.Obj { return wireData(resp.Data) }),
		dataTool(&toolSpec{
			name: "record",
			description: "Validate and merge review.json into the findings and record stores. " +
				"Call for local bundles that cannot post. Writes findings.json and record.json.",
			required: requiredRepoPr,
			meta:     toolMeta{title: "Record review", idempotent: true},
		}, toolDeps{}, decodeRecord, wireRecord, recordcore.Run,
			func(resp *core.RecordResponse) *jq.Obj { return wireData(resp.Data) }),
		directTool(&toolSpec{
			name: "report",
			description: "Render the offline review report (ranked findings, counts, markdown/SARIF). " +
				"Call after fetch or record; the follow-up is next. Reads the bundle and writes " +
				"only artifacts you request.",
			required: requiredRepoPr,
			meta:     toolMeta{title: "Render report", readOnly: true, idempotent: true},
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
				"(.review/<pr>/threads.json) from the host. Call after pushing fixes to re-read " +
				"resolution state before a re-review or merge readiness. Reads the host; writes " +
				"threads.json only.",
			required: requiredRepoPr,
			meta:     toolMeta{title: "Refresh review threads", readOnly: true, idempotent: true, openWorld: true},
		}, toolDeps{}, decodeThreads, wireThreads, fetchcore.RunThreads,
			func(resp *fetchcore.ThreadsResponse) *jq.Obj { return wireData(resp.Data) }),
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
				"Call to prove a fix before post. Writes verification.json and runs the recorded command.",
			required: requiredRepoPr,
			meta:     toolMeta{title: "Run verification", destructive: true, openWorld: true},
		}, toolDeps{commander: coreexec.OSCommander{}}, decodeVerify, wireVerify, verifycore.Run,
			func(resp *core.VerifyResponse) *jq.Obj { return wireData(resp.Data) }),
	}
	kept := make([]*tool, 0, len(tools))
	for _, t := range tools {
		if !exposed(t, readOnly, operator) {
			continue
		}
		kept = append(kept, t)
	}
	return kept
}

// exposed reports whether one tool is on the surface the two flags select.
// The CLI refuses --read-only with --operator, but the predicate stays total
// so the library cannot expose an operator tool on a read-only surface either.
func exposed(t *tool, readOnly, operator bool) bool {
	if readOnly && !t.readOnly {
		return false
	}
	return !t.operatorOnly || (!readOnly && operator)
}

// operatorTool marks one tool as part of the opt-in operator surface.
func operatorTool(t *tool) *tool {
	t.operatorOnly = true
	return t
}

// dataTool builds a tool whose service returns a response carrying a Data
// field, the shape every verb except next/show/check-tree/fetch shares. The
// response accessor is explicit: a renamed Data field is a compile error.
func dataTool[Req, S any](
	spec *toolSpec, deps toolDeps,
	decode func(*Req, *args), wire func(*bindings, *Req),
	svc func(context.Context, *Req) (*S, *core.RefusalError),
	data func(*S) *jq.Obj,
) *tool {
	return newTool(spec, deps, decode, wire, func(ctx context.Context, req *Req) (*jq.Obj, *core.RefusalError) {
		return invokeService(ctx, req, svc, data)
	})
}

// directTool builds a tool with a hand-written response mapping.
func directTool[Req any](
	spec *toolSpec, deps toolDeps,
	decode func(*Req, *args), wire func(*bindings, *Req),
	run func(context.Context, *Req) (*jq.Obj, *core.RefusalError),
) *tool {
	return newTool(spec, deps, decode, wire, run)
}

// newTool assembles one registry entry. The request type parameter is the
// single source of the arg schema and of the decode/wire/invoke type
// assertions, so none of the three can name a different request type.
func newTool[Req any](
	spec *toolSpec, deps toolDeps,
	decode func(*Req, *args), wire func(*bindings, *Req),
	run func(context.Context, *Req) (*jq.Obj, *core.RefusalError),
) *tool {
	return &tool{
		name:         spec.name,
		title:        spec.meta.title,
		description:  spec.description,
		request:      schemaOf[Req](),
		omit:         spec.omit,
		required:     spec.required,
		deps:         deps,
		readOnly:     spec.meta.readOnly,
		destructive:  spec.meta.destructive,
		idempotent:   spec.meta.idempotent,
		openWorld:    spec.meta.openWorld,
		operatorOnly: false,
		decode: func(raw json.RawMessage) (any, error) {
			return decodeRequest[Req](raw, spec.required, decode)
		},
		wire: func(b *bindings, raw any) {
			if req, ok := raw.(*Req); ok {
				wire(b, req)
			}
		},
		invoke: func(ctx context.Context, raw any) (*jq.Obj, *core.RefusalError) {
			req, ok := raw.(*Req)
			if !ok {
				return nil, internalRefusal()
			}
			return run(ctx, req)
		},
	}
}

// invokeService adapts one typed core service call.
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

// invokeReport runs report and writes its requested artifacts (the CLI
// adapter owns the same writes; the transport returns data, not files).
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

// invokeNext maps the next response to the CLI envelope's data shape.
func invokeNext(ctx context.Context, req *core.NextRequest) (*jq.Obj, *core.RefusalError) {
	resp, ref := reportcore.RunNext(ctx, req)
	if ref != nil {
		return nil, ref
	}
	var item any
	if resp.Item != nil {
		item = resp.Item.Wire()
	}
	return jq.NewObj("item", item), nil
}

// invokeShow maps the show response to the CLI envelope's data shape.
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

// invokeCheckTree maps the not-a-repo machine refusal to a tool refusal.
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

// invokeFetch calls the network-backed fetch service; its error is the
// adapter's missing-tool/refusal seam.
func invokeFetch(ctx context.Context, req *core.FetchRequest) (*jq.Obj, *core.RefusalError) {
	resp, err := fetchcore.Run(ctx, req)
	if err != nil {
		return nil, commandRefusal(err)
	}
	return wireData(resp.Data), nil
}

// invokeFetchLocal calls the local fetch service.
func invokeFetchLocal(ctx context.Context, req *core.FetchLocalRequest) (*jq.Obj, *core.RefusalError) {
	resp, err := fetchcore.RunLocal(ctx, req)
	if err != nil {
		return nil, commandRefusal(err)
	}
	return wireData(resp.Data), nil
}

// commandRefusal maps the command runner's error forms to a tool refusal.
func commandRefusal(err error) *core.RefusalError {
	if ref, ok := errors.AsType[*core.RefusalError](err); ok {
		return ref
	}
	if missing, ok := errors.AsType[*core.MissingToolError](err); ok {
		return &core.RefusalError{Code: "missing-tool", Message: "missing: " + missing.Tool}
	}
	return &core.RefusalError{Code: "fetch-failed", Message: err.Error()}
}

// writeArtifact writes one requested report artifact (0600: reports can
// carry reviewed-diff excerpts).
func writeArtifact(a *core.FileArtifact) *core.RefusalError {
	if a == nil {
		return nil
	}
	if err := os.WriteFile(a.Path, a.Content, artifactPerm); err != nil {
		return &core.RefusalError{
			Code:    "report-failed",
			Message: fmt.Sprintf("could not write %s: %v", a.Path, err),
		}
	}
	return nil
}

// wireData renders a typed data profile through core's ordered wire
// mapping, so the MCP result equals the CLI --json envelope's data.
func wireData(v any) *jq.Obj {
	if v == nil {
		return jq.NewObj()
	}
	if o, ok := core.Wire(v).(*jq.Obj); ok {
		return o
	}
	return jq.NewObj()
}

// internalRefusal builds a sealed internal refusal for impossible request
// mismatches.
func internalRefusal() *core.RefusalError {
	return &core.RefusalError{Code: "internal", Message: msgRequestMismatch}
}

// rawMarshal is kept next to wireData so result construction uses the same
// compact encoder as the CLI envelope.
func rawMarshal(o *jq.Obj) json.RawMessage { return json.RawMessage(o.MarshalCompact()) }
